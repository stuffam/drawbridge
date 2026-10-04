package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/store"
)

// ErrSchemaNewer means the database in a backup is from a newer Drawbridge than this one.
var ErrSchemaNewer = errors.New("this backup's database is from a newer Drawbridge; upgrade Drawbridge first")

// RestoreOptions says what to restore, and where to.
type RestoreOptions struct {
	// File is the backup, and Passphrase opens it.
	File       string
	Passphrase string
	// DBPath and KeyPath are where the database and the secret key live.
	DBPath, KeyPath string
	// Chown gives an installed file its owner; key is true for the secret key and false for
	// the database. Nil leaves both as the account running the restore made them.
	Chown func(path string, key bool) error
	// Actor is who the restored database's event log says restored it.
	Actor string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Restored is what a restore did.
type Restored struct {
	// Manifest is what the backup said about itself. For a snapshot, which says nothing, it has
	// the file's time and nothing else.
	Manifest Manifest
	// Snapshot is true when what was restored was a snapshot, a plain database file, and the
	// host's own key stayed in place.
	Snapshot bool
	// DBAside and KeyAside are where the files it replaced went, empty when there weren't any.
	// A snapshot restore leaves the key where it is, so KeyAside is empty.
	DBAside, KeyAside string
	// SessionsEnded is how many logins the backup held, all of which were ended.
	SessionsEnded int64
	// Migrated is true when the backup's schema was older and was brought up to date.
	Migrated bool
}

var sqliteMagic = []byte("SQLite format 3\x00")

// IsSnapshot says whether the file is a snapshot (a plain SQLite database, like the ones the
// host keeps in its backups directory) and not an encrypted backup, so a caller knows whether to
// ask for a passphrase.
func IsSnapshot(path string) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the file the admin named.
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, len(sqliteMagic))
	n, _ := io.ReadFull(f, head)
	return n == len(head) && bytes.Equal(head, sqliteMagic), nil
}

// Restore puts a backup in place of the database and the secret key, or a snapshot in place of
// the database alone. The daemon must not be running: nothing here can stop it from writing to
// the database it's about to lose.
//
// Everything is checked before anything is touched. The backup is decrypted into a temporary
// file beside the database, and that file has to be intact, no newer than this Drawbridge
// understands, free of SQLite integrity problems, and open with the key that came with it (a
// snapshot is a copy of the file, and has to open with the host's own key). Then the logins in
// it are ended, and the restore is recorded in its event log. Only then are the current files
// moved aside (with a time in their names, so an earlier restore's are never overwritten) and
// the new ones moved in. A failure while moving puts the old ones back.
func Restore(ctx context.Context, o RestoreOptions) (Restored, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	dir := filepath.Dir(o.DBPath)

	f, err := os.Open(o.File)
	if err != nil {
		return Restored{}, err
	}
	defer f.Close()
	snap, err := IsSnapshot(o.File)
	if err != nil {
		return Restored{}, err
	}
	tmp, err := os.CreateTemp(dir, ".restore-*.db")
	if err != nil {
		return Restored{}, fmt.Errorf("making a file beside the database: %w", err)
	}
	tmpDB := tmp.Name()
	defer removeDB(tmpDB)

	var (
		res    = Restored{Snapshot: snap}
		key    []byte
		sealer *keys.Sealer
		whose  = "the backup's key"
	)
	if snap {
		// The snapshot is copied, so the file the admin gave stays as it is.
		_, err = io.Copy(tmp, f)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return Restored{}, err
		}
		if fi, err := f.Stat(); err == nil {
			res.Manifest.CreatedAt = fi.ModTime()
		}
		if res.Manifest.Schema, err = schemaOf(tmpDB); err != nil {
			return Restored{}, fmt.Errorf("%w: %w", ErrNotABackup, err)
		}
		hostKey, err := os.ReadFile(o.KeyPath)
		if err != nil {
			return Restored{}, fmt.Errorf("a snapshot opens with the host's own key, and reading it failed: %w", err)
		}
		if sealer, err = keys.NewSealer(hostKey); err != nil {
			return Restored{}, fmt.Errorf("the host's secret key is unusable: %w", err)
		}
		whose = "this host's key"
	} else {
		man, k, err := Read(f, o.Passphrase, tmp)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return Restored{}, err
		}
		res.Manifest, key = man, k
		if sealer, err = keys.NewSealer(key); err != nil {
			return Restored{}, fmt.Errorf("%w: its key is the wrong size", ErrNotABackup)
		}
	}
	if res.Manifest.Schema > store.LatestSchema() {
		return Restored{}, ErrSchemaNewer
	}
	if err := prepare(ctx, tmpDB, sealer, whose, o, now(), &res); err != nil {
		return Restored{}, err
	}

	// The key goes to a file beside the old one, mode 0640 like the package makes it. A
	// snapshot has none: the host's stays.
	tmpKey := ""
	if !snap {
		tmpKey = o.KeyPath + ".restore"
		_ = os.Remove(tmpKey)
		if err := os.WriteFile(tmpKey, key, 0o640); err != nil { //nolint:gosec // G306: root:drawbridge 0640, as the package installs it.
			return Restored{}, fmt.Errorf("writing the secret key: %w", err)
		}
		defer func() { _ = os.Remove(tmpKey) }()
		if err := os.Chmod(tmpKey, 0o640); err != nil { //nolint:gosec // G302, see above.
			return Restored{}, err
		}
	}
	if o.Chown != nil {
		if tmpKey != "" {
			if err := o.Chown(tmpKey, true); err != nil {
				return Restored{}, err
			}
		}
		if err := o.Chown(tmpDB, false); err != nil {
			return Restored{}, err
		}
	}

	suffix := ".before-restore-" + now().UTC().Format("20060102-150405")
	sw := &swap{}
	for _, ext := range []string{"", "-wal", "-shm"} {
		// The suffix goes before the extension, so the kept files still read as one database:
		// SQLite finds a WAL by adding -wal to the database's name.
		if err := sw.aside(o.DBPath+ext, o.DBPath+suffix+ext); err != nil {
			sw.undo()
			return Restored{}, err
		}
	}
	if tmpKey != "" {
		if err := sw.aside(o.KeyPath, o.KeyPath+suffix); err != nil {
			sw.undo()
			return Restored{}, err
		}
		if err := sw.move(tmpKey, o.KeyPath); err != nil {
			sw.undo()
			return Restored{}, err
		}
	}
	if err := sw.move(tmpDB, o.DBPath); err != nil {
		sw.undo()
		return Restored{}, err
	}
	syncDir(dir)
	if sw.asided[o.DBPath] {
		res.DBAside = o.DBPath + suffix
	}
	if sw.asided[o.KeyPath] {
		res.KeyAside = o.KeyPath + suffix
	}
	return res, nil
}

// schemaOf reads the schema version of the SQLite file at path without migrating it.
func schemaOf(path string) (int, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// prepare opens the restored database as the key says, brings it up to date, checks it, ends
// its logins, and records the restore in its log.
func prepare(ctx context.Context, path string, sealer *keys.Sealer, whose string, o RestoreOptions, now time.Time, res *Restored) error {
	st, err := store.Open(ctx, path, sealer)
	if err != nil {
		return fmt.Errorf("opening the backup's database: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()
	v, err := st.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if v > store.LatestSchema() {
		return ErrSchemaNewer
	}
	res.Migrated = res.Manifest.Schema < store.LatestSchema()
	if err := st.CheckIntegrity(ctx); err != nil {
		return err
	}
	// The server's private key is the first thing the database seals. If the key that came with
	// the backup opens it, the rest were sealed with the same one.
	if _, err := st.Settings(ctx); err != nil {
		return fmt.Errorf("%s doesn't open its database: %w", whose, err)
	}
	if res.SessionsEnded, err = st.DeleteAllSessions(ctx); err != nil {
		return err
	}
	data := map[string]string{"made": res.Manifest.CreatedAt.UTC().Format(time.RFC3339)}
	if res.Manifest.Version != "" {
		data["version"] = res.Manifest.Version
	}
	if res.Snapshot {
		data["source"] = "snapshot"
	}
	if res.SessionsEnded > 0 {
		data["sessions_ended"] = fmt.Sprint(res.SessionsEnded)
	}
	if _, err := st.AddEvent(ctx, store.Event{Time: now, Kind: "backup.restored", Category: "admin",
		Actor: o.Actor, Via: "cli", Data: data}); err != nil {
		return err
	}
	closed = true
	if err := st.Close(); err != nil {
		return err
	}
	// Closing the last connection folds the WAL into the file. If one is left, the database
	// isn't whole in the file alone, and moving the file would lose what's in the WAL.
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		return errors.New("the restored database was left with unwritten changes")
	}
	return nil
}

// removeDB deletes a temporary database and any WAL files it grew.
func removeDB(path string) {
	for _, ext := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + ext)
	}
}

// swap moves files, and can put them all back.
type swap struct {
	done   [][2]string
	asided map[string]bool
}

// aside moves a file that may not exist out of the way.
func (s *swap) aside(from, to string) error {
	if _, err := os.Lstat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := s.move(from, to); err != nil {
		return err
	}
	if s.asided == nil {
		s.asided = map[string]bool{}
	}
	s.asided[from] = true
	return nil
}

func (s *swap) move(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("moving %s: %w", from, err)
	}
	s.done = append(s.done, [2]string{from, to})
	return nil
}

// undo puts every file moved so far back, newest first.
func (s *swap) undo() {
	for i := len(s.done) - 1; i >= 0; i-- {
		_ = os.Rename(s.done[i][1], s.done[i][0])
	}
	s.done = nil
}

// syncDir makes the renames durable, as far as the platform lets it. A failure isn't one worth
// failing the restore for: the files are in place.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil { //nolint:gosec // G304: the directory holding the admin's database.
		_ = d.Sync()
		_ = d.Close()
	}
}
