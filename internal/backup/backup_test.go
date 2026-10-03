package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

const passphrase = "correct horse battery"

// cheapKDF makes the key derivation fast enough to run many times; the settings travel in the
// file, so Read uses whatever the writer did.
func cheapKDF(t *testing.T) {
	t.Helper()
	tm, mem, th := kdfTime, kdfMemory, kdfThreads
	kdfTime, kdfMemory, kdfThreads = 1, 64, 1
	t.Cleanup(func() { kdfTime, kdfMemory, kdfThreads = tm, mem, th })
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// makeBackup writes a backup of a database of n random bytes, and returns it with what went in.
func makeBackup(t *testing.T, n int) (file, db, key []byte) {
	t.Helper()
	cheapKDF(t)
	db, key = randomBytes(t, n), randomBytes(t, 32)
	path := filepath.Join(t.TempDir(), "drawbridge.db")
	if err := os.WriteFile(path, db, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m := Manifest{CreatedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), Version: "0.9.0", Schema: 8}
	if err := Write(&out, passphrase, m, key, path); err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), db, key
}

func read(file []byte, pass string) (Manifest, []byte, []byte, error) {
	var db bytes.Buffer
	m, key, err := Read(bytes.NewReader(file), pass, &db)
	return m, key, db.Bytes(), err
}

func TestRoundTrip(t *testing.T) {
	// Sizes around the chunk boundary, where the last chunk is easiest to get wrong. The tar and
	// the compression change what each one is by the time it's chunked, and random bytes don't
	// compress, so the largest spans several chunks.
	for _, n := range []int{0, 1, 1000, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 17} {
		file, db, key := makeBackup(t, n)
		m, gotKey, gotDB, err := read(file, passphrase)
		if err != nil {
			t.Fatalf("%d bytes: %v", n, err)
		}
		if !bytes.Equal(gotDB, db) || !bytes.Equal(gotKey, key) {
			t.Errorf("%d bytes: the database or the key came back different", n)
		}
		if m.Format != FormatVersion || m.Schema != 8 || m.Version != "0.9.0" || !m.CreatedAt.Equal(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)) {
			t.Errorf("%d bytes: manifest %+v", n, m)
		}
	}
}

func TestTheFileIsEncrypted(t *testing.T) {
	cheapKDF(t)
	key := bytes.Repeat([]byte("K"), 32)
	secret := []byte(strings.Repeat("a database full of private keys ", 100))
	path := filepath.Join(t.TempDir(), "db")
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Write(&out, passphrase, Manifest{}, key, path); err != nil {
		t.Fatal(err)
	}
	for what, needle := range map[string][]byte{"the key": key, "the database": secret[:40], "the passphrase": []byte(passphrase), "the file name": []byte("drawbridge.db")} {
		if bytes.Contains(out.Bytes(), needle) {
			t.Errorf("%s is in the file in the clear", what)
		}
	}
	if !bytes.HasPrefix(out.Bytes(), []byte(magic)) {
		t.Error("the file doesn't start with the magic string")
	}
}

func TestEachFileIsDifferent(t *testing.T) {
	// The same backup twice differs (a fresh salt), so two files can't be told to hold
	// the same data, and a nonce is never reused under one key.
	cheapKDF(t)
	path := filepath.Join(t.TempDir(), "db")
	_ = os.WriteFile(path, []byte("db"), 0o600)
	var a, b bytes.Buffer
	key := bytes.Repeat([]byte{1}, 32)
	_ = Write(&a, passphrase, Manifest{}, key, path)
	_ = Write(&b, passphrase, Manifest{}, key, path)
	if bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("two backups are identical")
	}
}

func TestPassphrase(t *testing.T) {
	cheapKDF(t)
	path := filepath.Join(t.TempDir(), "db")
	_ = os.WriteFile(path, []byte("db"), 0o600)
	key := bytes.Repeat([]byte{1}, 32)
	for _, weak := range []string{"", "short", "elevenchars"} {
		if err := Write(io.Discard, weak, Manifest{}, key, path); !errors.Is(err, ErrWeakPassphrase) {
			t.Errorf("%q: err %v, want ErrWeakPassphrase", weak, err)
		}
	}
	// Characters, not bytes: twelve of them is enough, however they're written.
	if err := Write(io.Discard, "ñññññññññññ"+"ñ", Manifest{}, key, path); err != nil {
		t.Errorf("twelve characters: %v", err)
	}

	file, _, _ := makeBackup(t, 100)
	for _, wrong := range []string{"", "Correct horse battery", passphrase + " ", "something else entirely"} {
		if _, _, _, err := read(file, wrong); !errors.Is(err, ErrPassphrase) {
			t.Errorf("%q: err %v, want ErrPassphrase", wrong, err)
		}
	}
}

func TestNotABackup(t *testing.T) {
	file, _, _ := makeBackup(t, 100)
	for name, in := range map[string][]byte{
		"empty":        nil,
		"text":         []byte("hello, this is not a backup file at all, no it is not, really not"),
		"a bare short": file[:10],
		"an SQLite db": append([]byte("SQLite format 3\x00"), make([]byte, 200)...),
		"no body":      file[:headerSize-1],
	} {
		if _, _, _, err := read(in, passphrase); !errors.Is(err, ErrNotABackup) {
			t.Errorf("%s: err %v, want ErrNotABackup", name, err)
		}
	}
}

func TestANewerFormatIsSaidSo(t *testing.T) {
	file, _, _ := makeBackup(t, 100)
	file[len(magic)] = FormatVersion + 1
	if _, _, _, err := read(file, passphrase); !errors.Is(err, ErrNewer) {
		t.Errorf("err %v, want ErrNewer", err)
	}
}

// A file chooses its own key derivation settings, so a crafted one mustn't be able to make
// restore allocate whatever it likes.
func TestAbsurdSettingsAreRefusedUnread(t *testing.T) {
	for name, edit := range map[string]func(h []byte){
		"memory":  func(h []byte) { binary.BigEndian.PutUint32(h[len(magic)+5:], 1<<30) },
		"time":    func(h []byte) { binary.BigEndian.PutUint32(h[len(magic)+1:], 1<<20) },
		"threads": func(h []byte) { h[len(magic)+9] = 200 },
		"no time": func(h []byte) { binary.BigEndian.PutUint32(h[len(magic)+1:], 0) },
		"chunk":   func(h []byte) { binary.BigEndian.PutUint32(h[headerSize-4:], 1<<30) },
		"tiny":    func(h []byte) { binary.BigEndian.PutUint32(h[headerSize-4:], 1) },
	} {
		file, _, _ := makeBackup(t, 100)
		edit(file)
		start := time.Now()
		if _, _, _, err := read(file, passphrase); !errors.Is(err, ErrNotABackup) {
			t.Errorf("%s: err %v, want ErrNotABackup", name, err)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%s: took %v, so it spent the cost before refusing", name, time.Since(start))
		}
	}
}

// Every byte of a backup matters: changing any one is refused, and so is every way of cutting
// or lengthening it. Nothing reads as a smaller, valid backup.
func TestDamageIsAlwaysCaught(t *testing.T) {
	file, _, _ := makeBackup(t, 3*chunkSize)
	if len(file) < 3*chunkSize {
		t.Fatalf("the file is %d bytes: too small to span chunks", len(file))
	}

	t.Run("a changed byte", func(t *testing.T) {
		// Every byte of the header and the first and last chunks' edges, and a sample of the rest.
		for i := 0; i < len(file); i += 1 + i/100 {
			bad := bytes.Clone(file)
			bad[i] ^= 0x01
			if _, _, _, err := read(bad, passphrase); err == nil {
				t.Fatalf("a change at byte %d of %d was accepted", i, len(file))
			}
		}
		for i := len(file) - 40; i < len(file); i++ {
			bad := bytes.Clone(file)
			bad[i] ^= 0x80
			if _, _, _, err := read(bad, passphrase); err == nil {
				t.Fatalf("a change at byte %d of %d was accepted", i, len(file))
			}
		}
	})

	t.Run("cut short", func(t *testing.T) {
		full := chunkSize + overhead
		cuts := []int{headerSize, headerSize + 1, headerSize + overhead, headerSize + full - 1,
			headerSize + full,                          // exactly after one chunk
			headerSize + full + 1, headerSize + 2*full, // exactly after two
			len(file) - 1, len(file) - overhead, len(file) - overhead - 1}
		for _, n := range cuts {
			if n >= len(file) {
				continue
			}
			if _, _, _, err := read(file[:n], passphrase); err == nil {
				t.Errorf("a file cut to %d of %d bytes was accepted", n, len(file))
			} else if !errors.Is(err, ErrPassphrase) && !errors.Is(err, ErrDamaged) && !errors.Is(err, ErrNotABackup) {
				t.Errorf("cut to %d: err %v isn't one of the backup errors", n, err)
			}
		}
	})

	t.Run("added to", func(t *testing.T) {
		for _, extra := range [][]byte{{0}, bytes.Repeat([]byte{7}, 100), bytes.Repeat([]byte{7}, overhead), randomBytes(t, chunkSize+overhead)} {
			if _, _, _, err := read(append(bytes.Clone(file), extra...), passphrase); err == nil {
				t.Errorf("a file with %d bytes added was accepted", len(extra))
			}
		}
	})

	t.Run("chunks swapped", func(t *testing.T) {
		full := chunkSize + overhead
		bad := bytes.Clone(file)
		a := bad[headerSize+full : headerSize+2*full]
		b := bad[headerSize+2*full : headerSize+3*full]
		tmp := bytes.Clone(a)
		copy(a, b)
		copy(b, tmp)
		if _, _, _, err := read(bad, passphrase); !errors.Is(err, ErrDamaged) {
			t.Errorf("err %v, want ErrDamaged", err)
		}
	})

	t.Run("a chunk dropped", func(t *testing.T) {
		full := chunkSize + overhead
		bad := append(bytes.Clone(file[:headerSize+full]), file[headerSize+2*full:]...)
		if _, _, _, err := read(bad, passphrase); !errors.Is(err, ErrDamaged) {
			t.Errorf("err %v, want ErrDamaged", err)
		}
	})
}

// A backup is only as good as what it restores, and a file with the wrong contents, however
// well it's encrypted, isn't one.
func TestPayloadIsChecked(t *testing.T) {
	cheapKDF(t)
	entry := func(name string, size int, typ byte) func(*tar.Writer) error {
		return func(tw *tar.Writer) error {
			h := &tar.Header{Name: name, Mode: 0o600, Size: int64(size), Typeflag: typ}
			if typ == tar.TypeDir || typ == tar.TypeSymlink {
				h.Size = 0
				h.Linkname = "/etc/passwd"
			}
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			if h.Size > 0 {
				_, err := tw.Write(make([]byte, size))
				return err
			}
			return nil
		}
	}
	manifest := `{"format":1,"schema":8}`
	good := func(tw *tar.Writer) error {
		for _, e := range []struct {
			n string
			b []byte
		}{{"manifest.json", []byte(manifest)}, {"secret.key", make([]byte, 32)}, {"drawbridge.db", []byte("db")}} {
			if err := tw.WriteHeader(&tar.Header{Name: e.n, Mode: 0o600, Size: int64(len(e.b)), Typeflag: tar.TypeReg}); err != nil {
				return err
			}
			if _, err := tw.Write(e.b); err != nil {
				return err
			}
		}
		return nil
	}
	cases := map[string]func(*tar.Writer) error{
		"no files": func(*tar.Writer) error { return nil },
		"no key": func(tw *tar.Writer) error {
			return seq(tw, entry("manifest.json", 2, tar.TypeReg), entry("drawbridge.db", 2, tar.TypeReg))
		},
		"a short key": func(tw *tar.Writer) error {
			return seq(tw, entry("manifest.json", 2, tar.TypeReg), entry("secret.key", 16, tar.TypeReg), entry("drawbridge.db", 2, tar.TypeReg))
		},
		"another file":    func(tw *tar.Writer) error { return seq(tw, good, entry("../etc/passwd", 3, tar.TypeReg)) },
		"a repeated file": func(tw *tar.Writer) error { return seq(tw, good, entry("drawbridge.db", 3, tar.TypeReg)) },
		"a symlink":       func(tw *tar.Writer) error { return seq(tw, entry("manifest.json", 2, tar.TypeSymlink)) },
		"a huge manifest": func(tw *tar.Writer) error { return seq(tw, entry("manifest.json", maxManifest+1, tar.TypeReg)) },
		"a junk manifest": func(tw *tar.Writer) error {
			return seq(tw, entry("manifest.json", 2, tar.TypeReg), entry("secret.key", 32, tar.TypeReg), entry("drawbridge.db", 2, tar.TypeReg))
		},
	}
	for name, fill := range cases {
		var buf bytes.Buffer
		if err := encrypt(&buf, passphrase, fill); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, _, _, err := read(buf.Bytes(), passphrase); !errors.Is(err, ErrNotABackup) {
			t.Errorf("%s: err %v, want ErrNotABackup", name, err)
		}
	}
	// The control: the same helper makes one that reads.
	var buf bytes.Buffer
	if err := encrypt(&buf, passphrase, good); err != nil {
		t.Fatal(err)
	}
	if _, _, db, err := read(buf.Bytes(), passphrase); err != nil || string(db) != "db" {
		t.Fatalf("the control backup: %v, %q", err, db)
	}
	// A manifest of another format is a newer backup, not a broken one.
	buf.Reset()
	if err := encrypt(&buf, passphrase, func(tw *tar.Writer) error {
		for _, e := range []struct {
			n string
			b []byte
		}{{"manifest.json", []byte(`{"format":2}`)}, {"secret.key", make([]byte, 32)}, {"drawbridge.db", []byte("db")}} {
			_ = tw.WriteHeader(&tar.Header{Name: e.n, Mode: 0o600, Size: int64(len(e.b)), Typeflag: tar.TypeReg})
			_, _ = tw.Write(e.b)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := read(buf.Bytes(), passphrase); !errors.Is(err, ErrNewer) {
		t.Errorf("a format 2 manifest: err %v, want ErrNewer", err)
	}
}

func seq(tw *tar.Writer, fns ...func(*tar.Writer) error) error {
	for _, fn := range fns {
		if err := fn(tw); err != nil {
			return err
		}
	}
	return nil
}

func TestWriteFailsWithoutADatabase(t *testing.T) {
	cheapKDF(t)
	err := Write(io.Discard, passphrase, Manifest{}, make([]byte, 32), filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("a backup of a database that isn't there")
	}
	if err := Write(io.Discard, passphrase, Manifest{}, nil, os.DevNull); err == nil {
		t.Fatal("a backup without a key")
	}
}

// The chunk layer on its own, because the gzip inside it would catch most of what's wrong with a
// cut file, and the chunks mustn't depend on that.
func TestChunksCannotBeCutOrExtended(t *testing.T) {
	aead, err := chacha20poly1305.NewX(randomBytes(t, 32))
	if err != nil {
		t.Fatal(err)
	}
	ad := []byte("header")
	seal := func(plain []byte) []byte {
		var out bytes.Buffer
		cw := &chunkWriter{w: &out, aead: aead, ad: ad}
		if _, err := cw.Write(plain); err != nil {
			t.Fatal(err)
		}
		if err := cw.close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	open := func(sealed []byte) ([]byte, error) {
		return io.ReadAll(&chunkReader{r: bufio.NewReader(bytes.NewReader(sealed)), aead: aead, ad: ad, size: chunkSize})
	}
	full := chunkSize + overhead

	for _, n := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 2 * chunkSize, 2*chunkSize + 5, 3 * chunkSize} {
		plain := randomBytes(t, n)
		sealed := seal(plain)
		got, err := open(sealed)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes: %v, and the same back: %v", n, err, bytes.Equal(got, plain))
		}
		// Cut after any whole number of chunks: it must not read as a shorter message.
		for k := 0; k*full < len(sealed); k++ {
			if _, err := open(sealed[:k*full]); err == nil {
				t.Errorf("%d bytes cut to %d chunks was accepted", n, k)
			}
		}
		// Extended with anything, a whole chunk or a few bytes.
		for _, extra := range [][]byte{{1}, randomBytes(t, overhead), randomBytes(t, full), randomBytes(t, 2*full)} {
			if _, err := open(append(bytes.Clone(sealed), extra...)); err == nil {
				t.Errorf("%d bytes plus %d was accepted", n, len(extra))
			}
		}
	}
}

// When the payload's last bytes (gzip's trailer) fall in a chunk of their own, the tar is
// done before that chunk is read. Reading on to it is what notices what comes after.
func TestDataAfterTheLastChunkIsCaught(t *testing.T) {
	full := chunkSize + overhead
	// A database's size sets where the payload ends. Random bytes grow it by one byte per byte,
	// so a few tries land the end just past a chunk boundary.
	n := chunkSize + 1000
	for try := 0; try < 12; try++ {
		file, _, _ := makeBackup(t, n)
		last := (len(file)-headerSize)%full - overhead
		if last < 1 || last > 8 {
			// Move by the shortest way round to a last chunk of 4 bytes.
			delta := ((4-last)%full + full) % full
			if delta > full/2 {
				delta -= full
			}
			n += delta
			continue
		}
		if _, _, _, err := read(file, passphrase); err != nil {
			t.Fatalf("the backup that ends in a %d-byte chunk: %v", last, err)
		}
		for _, extra := range [][]byte{{1}, randomBytes(t, 40), randomBytes(t, full)} {
			if _, _, _, err := read(append(bytes.Clone(file), extra...), passphrase); err == nil {
				t.Errorf("a %d-byte last chunk with %d bytes added was accepted", last, len(extra))
			}
		}
		return
	}
	t.Fatal("no database size made a backup with a tiny last chunk")
}

// What follows the tar is a few bytes of gzip's trailer. A payload that goes on past that is
// refused rather than read to its end.
func TestAPayloadThatGoesOnIsRefused(t *testing.T) {
	cheapKDF(t)
	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	for _, e := range []struct {
		n string
		b []byte
	}{{"manifest.json", []byte(`{"format":1}`)}, {"secret.key", make([]byte, 32)}, {"drawbridge.db", []byte("db")}} {
		_ = tw.WriteHeader(&tar.Header{Name: e.n, Mode: 0o600, Size: int64(len(e.b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(e.b)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	build := func(extra int) []byte {
		var buf bytes.Buffer
		if err := encryptStream(&buf, passphrase, func(w io.Writer) error {
			if _, err := w.Write(tarball.Bytes()); err != nil {
				return err
			}
			_, err := w.Write(make([]byte, extra))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	// The control: a little padding after the tar is nothing.
	if _, _, db, err := read(build(512), passphrase); err != nil || string(db) != "db" {
		t.Fatalf("a payload with a little padding: %v, %q", err, db)
	}
	if _, _, _, err := read(build(2<<20), passphrase); !errors.Is(err, ErrDamaged) {
		t.Errorf("a payload that goes on for 2 MiB: err %v, want ErrDamaged", err)
	}
}
