// Package backup writes and reads Drawbridge's backup file (docs/PLAN.md §6.6).
//
// A backup holds a snapshot of the database and the key that opens the database's encrypted
// columns, so it restores onto a host that has neither. The key sits beside what it unlocks,
// so the file is encrypted with a passphrase, and one is required.
//
// The format: a plain header (a magic string, the format version, the passphrase's key
// derivation settings, the salt, and the chunk size), then the payload in chunks. The payload
// is a gzipped tar of manifest.json, secret.key, and drawbridge.db. Each chunk is
// XChaCha20-Poly1305 under a key derived from the passphrase with Argon2id. A chunk's nonce
// counts its position and marks the last chunk, and the header is authenticated with every
// chunk, so a file that was edited, reordered, or cut short (even exactly between chunks)
// is refused instead of read as a shorter backup.
package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// MinPassphrase is the shortest passphrase a backup accepts, in characters. A stolen file can
// be guessed at offline, with no rate limit, so it asks for more than a password does.
const MinPassphrase = 12

// FormatVersion is the version of the file layout this package writes and reads.
const FormatVersion = 1

const (
	magic      = "drawbridge-backup\n"
	headerSize = len(magic) + 1 + 4 + 4 + 1 + saltSize + 4
	saltSize   = 16
	chunkSize  = 64 << 10
	overhead   = chacha20poly1305.Overhead

	// What a file may ask of the reader, so a crafted one can't make restore allocate gigabytes.
	maxKDFTime    = 16
	maxKDFMemory  = 256 << 10 // KiB
	maxKDFThreads = 8
	minChunk      = 4 << 10
	maxChunk      = 1 << 20

	maxManifest = 64 << 10
	maxDatabase = 2 << 30
)

// The settings new files are written with. They're variables so the tests can make them cheap;
// a file records the ones it was made with.
var (
	kdfTime    uint32 = 4
	kdfMemory  uint32 = 64 << 10 // KiB
	kdfThreads uint8  = 1
)

var (
	// ErrWeakPassphrase means the passphrase is shorter than MinPassphrase.
	ErrWeakPassphrase = fmt.Errorf("a backup passphrase needs at least %d characters", MinPassphrase)
	// ErrNotABackup means the file isn't one this package wrote.
	ErrNotABackup = errors.New("this isn't a Drawbridge backup")
	// ErrNewer means the file was made by a newer Drawbridge than this one reads.
	ErrNewer = errors.New("this backup was made by a newer Drawbridge; upgrade Drawbridge first")
	// ErrPassphrase means the first chunk didn't open. A wrong passphrase and a damaged file
	// look the same here, and the message says both.
	ErrPassphrase = errors.New("wrong passphrase, or the backup is damaged")
	// ErrDamaged means the file opened at first, and then it didn't: it was edited, or cut short.
	ErrDamaged = errors.New("the backup is damaged or cut short")
)

// Manifest says what a backup holds.
type Manifest struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	// Version is the Drawbridge that made it.
	Version string `json:"version"`
	// Schema is the database's schema version, so a restore can tell a backup from a newer
	// Drawbridge before it opens the database.
	Schema int `json:"schema"`
}

// Check says whether a passphrase is acceptable, so a caller can refuse a short one before it
// spends anything on the backup. It returns the passphrase's length in characters.
func Check(passphrase string) (int, error) {
	n := utf8.RuneCountInString(passphrase)
	if n < MinPassphrase {
		return n, ErrWeakPassphrase
	}
	return n, nil
}

// Write encrypts a backup to w: the manifest, the key, and the database file at dbPath. The
// database should be a snapshot, which nothing is writing to. It sets m's Format.
func Write(w io.Writer, passphrase string, m Manifest, key []byte, dbPath string) error {
	if len(key) == 0 {
		return errors.New("a backup needs the secret key")
	}
	m.Format = FormatVersion
	manifest, err := json.Marshal(m)
	if err != nil {
		return err
	}
	db, err := os.Open(dbPath) //nolint:gosec // G304: the snapshot the service just made.
	if err != nil {
		return err
	}
	defer db.Close()
	info, err := db.Stat()
	if err != nil {
		return err
	}
	return encrypt(w, passphrase, func(tw *tar.Writer) error {
		for _, e := range []struct {
			name string
			size int64
			r    io.Reader
		}{
			{"manifest.json", int64(len(manifest)), bytes.NewReader(manifest)},
			{"secret.key", int64(len(key)), bytes.NewReader(key)},
			{"drawbridge.db", info.Size(), db},
		} {
			if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o600, Size: e.size, ModTime: m.CreatedAt}); err != nil {
				return err
			}
			if _, err := io.Copy(tw, e.r); err != nil {
				return err
			}
		}
		return nil
	})
}

// encrypt writes the header, then the tar that fill writes into the payload.
func encrypt(w io.Writer, passphrase string, fill func(*tar.Writer) error) error {
	return encryptStream(w, passphrase, func(out io.Writer) error {
		tw := tar.NewWriter(out)
		if err := fill(tw); err != nil {
			return err
		}
		return tw.Close()
	})
}

// encryptStream writes the header, then whatever fill writes into the gzipped payload.
func encryptStream(w io.Writer, passphrase string, fill func(io.Writer) error) error {
	if _, err := Check(passphrase); err != nil {
		return err
	}
	header := make([]byte, 0, headerSize)
	header = append(header, magic...)
	header = append(header, FormatVersion)
	header = binary.BigEndian.AppendUint32(header, kdfTime)
	header = binary.BigEndian.AppendUint32(header, kdfMemory)
	header = append(header, kdfThreads)
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, chunkSize)
	aead, err := newAEAD(passphrase, header)
	if err != nil {
		return err
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	cw := &chunkWriter{w: w, aead: aead, ad: header}
	gz := gzip.NewWriter(cw)
	if err := fill(gz); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return cw.close()
}

// newAEAD derives the file's key from the passphrase and the header's settings and salt.
func newAEAD(passphrase string, header []byte) (cipher.AEAD, error) {
	o := len(magic) + 1
	t := binary.BigEndian.Uint32(header[o:])
	m := binary.BigEndian.Uint32(header[o+4:])
	p := header[o+8]
	salt := header[o+9 : o+9+saltSize]
	return chacha20poly1305.NewX(argon2.IDKey([]byte(passphrase), salt, t, m, p, chacha20poly1305.KeySize))
}

// nonce is a chunk's position, with a flag for the last one.
func nonce(index uint64, final bool) []byte {
	n := make([]byte, chacha20poly1305.NonceSizeX)
	binary.BigEndian.PutUint64(n, index)
	if final {
		n[8] = 1
	}
	return n
}

// chunkWriter encrypts what's written to it in chunks. It holds back the newest bytes, so the
// last chunk is known (and marked) when close is called.
type chunkWriter struct {
	w     io.Writer
	aead  cipher.AEAD
	ad    []byte
	buf   []byte
	index uint64
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	for len(c.buf) > chunkSize {
		if err := c.flush(c.buf[:chunkSize], false); err != nil {
			return 0, err
		}
		c.buf = c.buf[chunkSize:]
	}
	return len(p), nil
}

func (c *chunkWriter) flush(plain []byte, final bool) error {
	sealed := c.aead.Seal(nil, nonce(c.index, final), plain, c.ad)
	c.index++
	_, err := c.w.Write(sealed)
	return err
}

func (c *chunkWriter) close() error { return c.flush(c.buf, true) }

// Read decrypts a backup from r, and returns its manifest and key. The database is copied to
// db as it's read, before the end of the file has been checked, so db can hold part of a
// database when Read fails: the caller writes it somewhere it can throw away.
func Read(r io.Reader, passphrase string, db io.Writer) (Manifest, []byte, error) {
	br := bufio.NewReader(r)
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(br, header); err != nil || string(header[:len(magic)]) != magic {
		return Manifest{}, nil, ErrNotABackup
	}
	o := len(magic)
	switch v := header[o]; {
	case v == 0:
		return Manifest{}, nil, ErrNotABackup
	case v > FormatVersion:
		return Manifest{}, nil, ErrNewer
	}
	t := binary.BigEndian.Uint32(header[o+1:])
	m := binary.BigEndian.Uint32(header[o+5:])
	p := header[o+9]
	chunk := binary.BigEndian.Uint32(header[headerSize-4:])
	if t == 0 || t > maxKDFTime || m < 8 || m > maxKDFMemory || p == 0 || p > maxKDFThreads ||
		chunk < minChunk || chunk > maxChunk {
		return Manifest{}, nil, ErrNotABackup
	}
	aead, err := newAEAD(passphrase, header)
	if err != nil {
		return Manifest{}, nil, err
	}
	co := &chunkReader{r: br, aead: aead, ad: header, size: int(chunk)}
	gz, err := gzip.NewReader(co)
	if err != nil {
		return Manifest{}, nil, co.fail(err)
	}
	man, key, err := readPayload(tar.NewReader(gz), db)
	if err != nil {
		return Manifest{}, nil, co.fail(err)
	}
	// The tar's end isn't the file's: reading on until the chunks end is what checks the last
	// one, and that nothing was added after it.
	// What's left after the tar is gzip's trailer and a little padding, so a limit that a real
	// backup never reaches stops a payload that goes on, and reaching it is damage.
	const trailer = 1 << 20
	n, err := io.Copy(io.Discard, io.LimitReader(gz, trailer))
	if err != nil {
		return Manifest{}, nil, co.fail(err)
	}
	if n == trailer {
		return Manifest{}, nil, ErrDamaged
	}
	return man, key, nil
}

var (
	errMissing    = fmt.Errorf("%w: it's missing a file", ErrNotABackup)
	errUnexpected = fmt.Errorf("%w: it has a file it shouldn't", ErrNotABackup)
	errBadSize    = fmt.Errorf("%w: a file in it is the wrong size", ErrNotABackup)
)

// readPayload reads the tar's three files, in any order.
func readPayload(tr *tar.Reader, db io.Writer) (Manifest, []byte, error) {
	var (
		man                   Manifest
		key                   []byte
		gotMan, gotKey, gotDB bool
	)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, nil, err
		}
		if h.Typeflag != tar.TypeReg {
			return Manifest{}, nil, errUnexpected
		}
		switch {
		case h.Name == "manifest.json" && !gotMan:
			if h.Size > maxManifest {
				return Manifest{}, nil, errBadSize
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return Manifest{}, nil, err
			}
			if err := json.Unmarshal(b, &man); err != nil {
				return Manifest{}, nil, fmt.Errorf("%w: its manifest is unreadable", ErrNotABackup)
			}
			gotMan = true
		case h.Name == "secret.key" && !gotKey:
			// The sealer's key is 32 bytes; this package doesn't import it to say so.
			if h.Size != chacha20poly1305.KeySize {
				return Manifest{}, nil, errBadSize
			}
			if key, err = io.ReadAll(tr); err != nil {
				return Manifest{}, nil, err
			}
			gotKey = true
		case h.Name == "drawbridge.db" && !gotDB:
			if h.Size > maxDatabase {
				return Manifest{}, nil, errBadSize
			}
			if _, err := io.Copy(db, tr); err != nil {
				return Manifest{}, nil, err
			}
			gotDB = true
		default:
			return Manifest{}, nil, errUnexpected
		}
	}
	if !gotMan || !gotKey || !gotDB {
		return Manifest{}, nil, errMissing
	}
	if man.Format != FormatVersion {
		return Manifest{}, nil, ErrNewer
	}
	return man, key, nil
}

// chunkReader decrypts chunks, one at a time, as the payload is read.
type chunkReader struct {
	r     *bufio.Reader
	aead  cipher.AEAD
	ad    []byte
	size  int
	index uint64
	out   []byte
	buf   []byte
	// done is set once the last chunk has opened.
	done bool
	// err is the error that ended it: a failure to open a chunk.
	err error
}

func (c *chunkReader) Read(p []byte) (int, error) {
	for len(c.out) == 0 {
		if c.done {
			return 0, io.EOF
		}
		if err := c.next(); err != nil {
			c.err = err
			return 0, err
		}
	}
	n := copy(p, c.out)
	c.out = c.out[n:]
	return n, nil
}

func (c *chunkReader) next() error {
	if c.buf == nil {
		c.buf = make([]byte, c.size+overhead)
	}
	n, err := io.ReadFull(c.r, c.buf)
	final := false
	switch {
	case err == nil:
		// A full chunk is the last one when nothing follows it.
		if _, perr := c.r.Peek(1); errors.Is(perr, io.EOF) {
			final = true
		} else if perr != nil {
			return perr
		}
	case errors.Is(err, io.ErrUnexpectedEOF):
		final = true
	case errors.Is(err, io.EOF):
		return c.damaged()
	default:
		return err
	}
	if n < overhead {
		return c.damaged()
	}
	plain, err := c.aead.Open(c.buf[:0], nonce(c.index, final), c.buf[:n], c.ad)
	if err != nil {
		return c.damaged()
	}
	c.index++
	c.done = final
	c.out = plain
	return nil
}

// damaged is the error for a chunk that doesn't open: the passphrase's fault when it's the
// first, because then nothing opened, and the file's otherwise.
func (c *chunkReader) damaged() error {
	if c.index == 0 {
		return ErrPassphrase
	}
	return ErrDamaged
}

// fail says why reading stopped: a chunk that didn't open, or a failure to read the file, if
// that's the cause, and else err.
func (c *chunkReader) fail(err error) error {
	if c.err != nil {
		return c.err
	}
	if errors.Is(err, ErrNotABackup) || errors.Is(err, ErrNewer) {
		return err
	}
	// gzip and tar errors on authenticated bytes mean the payload isn't what Write makes.
	return fmt.Errorf("%w: %w", ErrNotABackup, err)
}
