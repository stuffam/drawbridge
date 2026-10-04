package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/store"
)

// host is one machine's database and key: what a restore replaces.
type host struct {
	dir     string
	db, key string
	secret  []byte
	st      *store.Store
}

// newHost makes a host with an initialized database sealed under its own key, as the package
// leaves a fresh install.
func newHost(t *testing.T) *host {
	t.Helper()
	h := &host{dir: t.TempDir(), secret: randomBytes(t, 32)}
	h.db = filepath.Join(h.dir, "drawbridge.db")
	h.key = filepath.Join(h.dir, "secret.key")
	if err := os.WriteFile(h.key, h.secret, 0o640); err != nil {
		t.Fatal(err)
	}
	sealer, err := keys.NewSealer(h.secret)
	if err != nil {
		t.Fatal(err)
	}
	h.st, err = store.Open(context.Background(), h.db, sealer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return h
}

// backupOf snapshots the host's database and writes a backup of it to a file.
func backupOf(t *testing.T, h *host, schema int) string {
	t.Helper()
	cheapKDF(t)
	ctx := context.Background()
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := h.st.Snapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	return writeBackupFile(t, snap, h.secret, schema)
}

func writeBackupFile(t *testing.T, dbFile string, key []byte, schema int) string {
	t.Helper()
	cheapKDF(t)
	path := filepath.Join(t.TempDir(), "x.backup")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := Manifest{CreatedAt: time.Date(2026, 10, 2, 3, 4, 5, 0, time.UTC), Version: "0.9.0", Schema: schema}
	if err := Write(f, passphrase, m, key, dbFile); err != nil {
		t.Fatal(err)
	}
	return path
}

func (h *host) restore(file, pass string) (Restored, error) {
	return Restore(context.Background(), RestoreOptions{File: file, Passphrase: pass, DBPath: h.db, KeyPath: h.key,
		Actor: "root", Now: func() time.Time { return time.Date(2026, 10, 3, 22, 36, 0, 0, time.UTC) }})
}

// snapshotOfFiles is every file in the host's directory, by name, so a test can show that a
// failed restore changed nothing.
func (h *host) files(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(h.dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func openHost(t *testing.T, db string, secret []byte) *store.Store {
	t.Helper()
	sealer, err := keys.NewSealer(secret)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), db, sealer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// The exit criterion: a backup from one host restores onto a fresh one that has a different key
// and its own database, and what was sealed under the old key reads back.
func TestRestoreOntoAFreshHost(t *testing.T) {
	ctx := context.Background()
	old := newHost(t)
	phone, err := old.st.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.st.SaveDNSIntegration(ctx, store.DNSIntegration{Kind: store.KindAdGuard, BaseURL: "http://127.0.0.1:3000/control",
		Username: "drawbridge", Password: "adguard-password", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	user, err := old.st.CreateUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := old.st.CreateSession(ctx, store.Session{ID: "s1", TokenHash: []byte("t"), UserID: user.ID,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	oldSettings, err := old.st.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file := backupOf(t, old, store.LatestSchema())

	// The new host: a fresh install, with a key and a database of its own.
	fresh := newHost(t)
	freshSettings, _ := fresh.st.Settings(ctx)
	if freshSettings.PublicKey() == oldSettings.PublicKey() {
		t.Fatal("two hosts have the same server key")
	}
	_ = fresh.st.Close() // the daemon is stopped
	freshDB, _ := os.ReadFile(fresh.db)
	// A daemon that was killed leaves its WAL and shared-memory files. Beside the new database
	// they'd be applied to it, so they have to go with the old one.
	staleWAL, staleSHM := []byte("a WAL the old daemon left"), []byte("shared memory the old daemon left")
	if err := os.WriteFile(fresh.db+"-wal", staleWAL, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh.db+"-shm", staleSHM, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := fresh.restore(file, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionsEnded != 1 || res.Migrated || res.Manifest.Version != "0.9.0" {
		t.Errorf("result %+v", res)
	}

	// Nothing is left beside the database: no temporary files, no stray WAL.
	for name := range fresh.files(t) {
		if strings.HasPrefix(name, ".restore-") || strings.HasSuffix(name, ".restore") || name == "drawbridge.db-wal" || name == "drawbridge.db-shm" {
			t.Errorf("%s was left behind", name)
		}
	}

	// The key is the old host's, so the old host's secrets open.
	if got, _ := os.ReadFile(fresh.key); !bytes.Equal(got, old.secret) {
		t.Fatal("the secret key wasn't replaced")
	}
	if info, _ := os.Stat(fresh.key); info.Mode().Perm() != 0o640 {
		t.Errorf("the key is mode %v, want 0640", info.Mode().Perm())
	}
	if info, _ := os.Stat(fresh.db); info.Mode().Perm() != 0o600 {
		t.Errorf("the database is mode %v, want 0600", info.Mode().Perm())
	}
	st := openHost(t, fresh.db, old.secret)
	got, err := st.Client(ctx, store.ByName("phone"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != phone.ID || got.PrivateKey == nil || *got.PrivateKey != *phone.PrivateKey || got.PresharedKey != phone.PresharedKey || got.IPv4 != phone.IPv4 {
		t.Errorf("the restored client is %+v, want %+v", got, phone)
	}
	if s, _ := st.Settings(ctx); s.PublicKey() != oldSettings.PublicKey() || s.IPv4 != oldSettings.IPv4 || s.IPv6 != oldSettings.IPv6 {
		t.Errorf("the restored server is %+v, want the old host's %+v", s, oldSettings)
	}
	if in, ok, err := st.DNSIntegration(ctx); err != nil || !ok || in.Password != "adguard-password" {
		t.Errorf("the AdGuard Home password came back as %+v, %v, %v", in, ok, err)
	}
	// Nobody who was logged in to the old host is logged in to this one.
	if sessions, _ := st.Sessions(ctx, user.ID); len(sessions) != 0 {
		t.Errorf("%d sessions survived the restore", len(sessions))
	}
	events, _ := st.Events(ctx, store.EventFilter{Kind: "backup.restored"})
	if len(events) != 1 || events[0].Actor != "root" || events[0].Via != "cli" || events[0].Data["version"] != "0.9.0" ||
		events[0].Data["made"] != "2026-10-02T03:04:05Z" || events[0].Data["sessions_ended"] != "1" {
		t.Errorf("the restore's event is %+v", events)
	}

	// What it replaced is kept, whole, under names that say when.
	if !strings.HasSuffix(res.DBAside, ".before-restore-20261003-223600") || !strings.HasSuffix(res.KeyAside, ".before-restore-20261003-223600") {
		t.Fatalf("aside names %q and %q", res.DBAside, res.KeyAside)
	}
	if b, _ := os.ReadFile(res.DBAside); !bytes.Equal(b, freshDB) {
		t.Error("the replaced database wasn't kept byte for byte")
	}
	if b, _ := os.ReadFile(res.KeyAside); !bytes.Equal(b, fresh.secret) {
		t.Error("the replaced key wasn't kept")
	}
	if b, _ := os.ReadFile(res.DBAside + "-wal"); !bytes.Equal(b, staleWAL) {
		t.Error("the replaced database's WAL wasn't kept with it")
	}
	if b, _ := os.ReadFile(res.DBAside + "-shm"); !bytes.Equal(b, staleSHM) {
		t.Error("the replaced database's shared-memory file wasn't kept with it")
	}
}

// A restore that can't go ahead leaves the host exactly as it was.
func TestAFailedRestoreChangesNothing(t *testing.T) {
	ctx := context.Background()
	src := newHost(t)
	good := backupOf(t, src, store.LatestSchema())
	goodBytes, _ := os.ReadFile(good)

	// A database from a newer Drawbridge, with a manifest that says so, and one that doesn't.
	newer := func(manifestSays int) string {
		snap := filepath.Join(t.TempDir(), "newer.db")
		if err := src.st.Snapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", snap)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 'x')`, store.LatestSchema()+1); err != nil {
			t.Fatal(err)
		}
		return writeBackupFile(t, snap, src.secret, manifestSays)
	}
	// A database that isn't SQLite at all, and one sealed with another key than it came with.
	notSQLite := filepath.Join(t.TempDir(), "junk.db")
	_ = os.WriteFile(notSQLite, bytes.Repeat([]byte("not a database "), 1000), 0o600)
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := src.st.Snapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}

	damaged := damagedDatabaseBackup(t, src)

	cases := []struct {
		name string
		file string
		pass string
		want string // part of the error
		is   error
	}{
		{"a wrong passphrase", good, "not the passphrase", "", ErrPassphrase},
		// A database this small is one chunk, so damage to it can't be told from a wrong
		// passphrase, and the error says it could be either.
		{"a damaged file", func() string {
			p := filepath.Join(t.TempDir(), "d")
			b := bytes.Clone(goodBytes)
			b[len(b)/2] ^= 1
			_ = os.WriteFile(p, b, 0o600)
			return p
		}(), passphrase, "", ErrPassphrase},
		{"a cut-short file", func() string {
			p := filepath.Join(t.TempDir(), "c")
			_ = os.WriteFile(p, goodBytes[:len(goodBytes)-100], 0o600)
			return p
		}(), passphrase, "", ErrPassphrase},
		{"not a backup", notSQLite, passphrase, "", ErrNotABackup},
		{"a newer schema, said so", newer(store.LatestSchema() + 1), passphrase, "", ErrSchemaNewer},
		{"a newer schema, not said", newer(1), passphrase, "", ErrSchemaNewer},
		{"a manifest from a newer Drawbridge", writeBackupFile(t, snap, src.secret, store.LatestSchema()+1), passphrase, "", ErrSchemaNewer},
		{"a database SQLite finds damaged", damaged, passphrase, "integrity check", nil},
		{"a database that isn't SQLite", writeBackupFile(t, notSQLite, src.secret, store.LatestSchema()), passphrase, "opening the backup's database", nil},
		{"a key that isn't the database's", writeBackupFile(t, snap, randomBytes(t, 32), store.LatestSchema()), passphrase, "key doesn't open its database", nil},
		{"no such file", filepath.Join(t.TempDir(), "missing"), passphrase, "", os.ErrNotExist},
	}
	for _, c := range cases {
		h := newHost(t)
		_ = h.st.Close()
		before := h.files(t)
		_, err := h.restore(c.file, c.pass)
		if err == nil {
			t.Errorf("%s: the restore went ahead", c.name)
			continue
		}
		if c.is != nil && !errors.Is(err, c.is) {
			t.Errorf("%s: err %v, want %v", c.name, err, c.is)
		}
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %q doesn't say %q", c.name, err, c.want)
		}
		after := h.files(t)
		if len(after) != len(before) {
			t.Errorf("%s: the files went from %d to %d: %v", c.name, len(before), len(after), keysOf(after))
		}
		for name, content := range before {
			if after[name] != content {
				t.Errorf("%s: %s changed", c.name, name)
			}
		}
	}
}

func keysOf(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A backup from an older Drawbridge restores, and its database is brought up to date, so the
// daemon that starts on it finds the schema it expects.
func TestAnOlderBackupIsMigratedForward(t *testing.T) {
	ctx := context.Background()
	src := newHost(t)
	snap := filepath.Join(t.TempDir(), "old.db")
	if err := src.st.Snapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", snap)
	if err != nil {
		t.Fatal(err)
	}
	// Take the newest migration back, as an older Drawbridge's database would be without it.
	latest := store.LatestSchema()
	for _, q := range []string{`DROP TABLE api_tokens`, `DELETE FROM schema_migrations WHERE version = ` + strconv.Itoa(latest)} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()
	if latest != 8 {
		t.Skipf("this test takes back migration 8, and the newest is %d; update it", latest)
	}
	file := writeBackupFile(t, snap, src.secret, latest-1)

	dest := newHost(t)
	_ = dest.st.Close()
	res, err := dest.restore(file, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Migrated {
		t.Error("the result doesn't say the database was migrated")
	}
	st := openHost(t, dest.db, src.secret)
	if v, _ := st.SchemaVersion(ctx); v != latest {
		t.Errorf("the restored database is at schema %d, want %d", v, latest)
	}
	if tokens, err := st.APITokens(ctx, "nobody"); err != nil || len(tokens) != 0 {
		t.Errorf("the table the migration adds: %v, %v", tokens, err)
	}
}

// A host with nothing to replace (the daemon never started) restores too.
func TestRestoreOntoNothing(t *testing.T) {
	src := newHost(t)
	file := backupOf(t, src, store.LatestSchema())
	h := &host{dir: t.TempDir(), secret: nil}
	h.db, h.key = filepath.Join(h.dir, "drawbridge.db"), filepath.Join(h.dir, "secret.key")
	res, err := h.restore(file, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if res.DBAside != "" || res.KeyAside != "" {
		t.Errorf("it says it moved %q and %q aside, and there was nothing", res.DBAside, res.KeyAside)
	}
	openHost(t, h.db, src.secret)
}

// The moves can be undone, in order, so a failure partway leaves the old files in place.
func TestSwapUndoesWhatItDid(t *testing.T) {
	dir := t.TempDir()
	p := func(n string) string { return filepath.Join(dir, n) }
	for n, c := range map[string]string{"db": "old db", "key": "old key", "new-db": "new db", "new-key": "new key"} {
		_ = os.WriteFile(p(n), []byte(c), 0o600)
	}
	s := &swap{}
	steps := []error{
		s.aside(p("db"), p("db.aside")),
		s.aside(p("db-wal"), p("db-wal.aside")), // not there: nothing to do
		s.aside(p("key"), p("key.aside")),
		s.move(p("new-key"), p("key")),
		s.move(p("new-db"), p("db")),
		s.move(p("missing"), p("elsewhere")), // fails
	}
	for i, err := range steps {
		if (i == 5) != (err != nil) {
			t.Fatalf("step %d: err %v", i, err)
		}
	}
	s.undo()
	for n, want := range map[string]string{"db": "old db", "key": "old key", "new-db": "new db", "new-key": "new key"} {
		if got, _ := os.ReadFile(p(n)); string(got) != want {
			t.Errorf("%s is %q after the undo, want %q", n, got, want)
		}
	}
	for _, n := range []string{"db.aside", "key.aside", "db-wal.aside"} {
		if _, err := os.Stat(p(n)); err == nil {
			t.Errorf("%s is still there", n)
		}
	}
}

// damagedDatabaseBackup makes a backup of a database that opens and reads, and that SQLite's
// integrity check still fails: a page of a table nothing reads at startup is overwritten.
func damagedDatabaseBackup(t *testing.T, h *host) string {
	t.Helper()
	snap := filepath.Join(t.TempDir(), "damaged.db")
	if err := h.st.Snapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE junk (a INTEGER PRIMARY KEY, b BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		if _, err := db.Exec(`INSERT INTO junk (b) VALUES (randomblob(500))`); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	info, err := os.Stat(snap)
	if err != nil {
		t.Fatal(err)
	}
	// The junk table's pages are the last ones. Overwrite one in the middle of them.
	f, err := os.OpenFile(snap, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const page = 4096
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xFF}, page), (info.Size()/page-20)*page); err != nil {
		t.Fatal(err)
	}
	return writeBackupFile(t, snap, h.secret, store.LatestSchema())
}

// snapshotOf writes a plain snapshot of the host's database, as the nightly job does, and returns
// its path.
func snapshotOf(t *testing.T, h *host) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nightly-20261004-030000.db")
	if err := h.st.Snapshot(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	return path
}

// A snapshot goes back as the database alone: no passphrase, and the host's own key stays, because
// that's the key the snapshot was sealed with.
func TestRestoreASnapshot(t *testing.T) {
	ctx := context.Background()
	h := newHost(t)
	if _, err := h.st.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	// Someone is logged in when the snapshot is made, so the snapshot has a login in it.
	user, _ := h.st.CreateUser(ctx, "admin", "hash")
	now := time.Now()
	if err := h.st.CreateSession(ctx, store.Session{ID: "s1", TokenHash: []byte("t"), UserID: user.ID,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	snap := snapshotOf(t, h)
	snapBytes, _ := os.ReadFile(snap)
	if ok, err := IsSnapshot(snap); err != nil || !ok {
		t.Fatalf("IsSnapshot(snapshot) = %v, %v", ok, err)
	}

	// The host moves on: another client.
	if _, err := h.st.AddClient(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}
	_ = h.st.Close()
	keyBefore, _ := os.ReadFile(h.key)

	res, err := h.restore(snap, "") // no passphrase
	if err != nil {
		t.Fatal(err)
	}
	if !res.Snapshot || res.Migrated || res.KeyAside != "" || res.DBAside == "" || res.SessionsEnded != 1 {
		t.Errorf("result %+v", res)
	}
	if got, _ := os.ReadFile(h.key); !bytes.Equal(got, keyBefore) {
		t.Error("the host's key changed")
	}
	st := openHost(t, h.db, h.secret)
	if _, err := st.Client(ctx, store.ByName("phone")); err != nil {
		t.Errorf("the client the snapshot has: %v", err)
	}
	if _, err := st.Client(ctx, store.ByName("laptop")); err == nil {
		t.Error("the client added after the snapshot survived")
	}
	if sessions, _ := st.Sessions(ctx, user.ID); len(sessions) != 0 {
		t.Errorf("%d logins survived the restore", len(sessions))
	}
	events, _ := st.Events(ctx, store.EventFilter{Kind: "backup.restored"})
	if len(events) != 1 || events[0].Data["source"] != "snapshot" || events[0].Data["sessions_ended"] != "1" || events[0].Data["version"] != "" {
		t.Errorf("the restore's event is %+v", events)
	}
	// The snapshot file is where it was, untouched, and the replaced database is kept.
	if got, _ := os.ReadFile(snap); !bytes.Equal(got, snapBytes) {
		t.Error("the snapshot file changed")
	}
	if _, err := os.Stat(res.DBAside); err != nil {
		t.Errorf("the replaced database wasn't kept: %v", err)
	}
	for name := range h.files(t) {
		if strings.HasPrefix(name, ".restore-") {
			t.Errorf("%s was left behind", name)
		}
	}
}

// What a snapshot can't be restored from leaves the host as it was.
func TestAFailedSnapshotRestoreChangesNothing(t *testing.T) {
	ctx := context.Background()
	src := newHost(t)
	good := snapshotOf(t, src)

	// A snapshot from a host with another key: the host can't open it.
	other := newHost(t)
	foreign := snapshotOf(t, other)

	// A snapshot of a newer schema.
	newer := snapshotOf(t, src)
	db, err := sql.Open("sqlite", newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 'x')`, store.LatestSchema()+1); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// A file that starts like SQLite and isn't one.
	junk := filepath.Join(t.TempDir(), "junk.db")
	_ = os.WriteFile(junk, append(append([]byte{}, sqliteMagic...), bytes.Repeat([]byte("junk"), 2000)...), 0o600)

	for name, c := range map[string]struct {
		file string
		want error
		text string
	}{
		"another host's snapshot": {foreign, nil, "this host's key doesn't open its database"},
		"a newer schema":          {newer, ErrSchemaNewer, ""},
		"a file that isn't one":   {junk, nil, ""},
	} {
		h := newHost(t) // a different key from both
		_ = h.st.Close()
		if name == "a newer schema" {
			// The key has to be the snapshot's for the schema check to be what stops it.
			if err := os.WriteFile(h.key, src.secret, 0o640); err != nil {
				t.Fatal(err)
			}
		}
		before := h.files(t)
		_, err := h.restore(c.file, "")
		if err == nil {
			t.Errorf("%s: the restore went ahead", name)
			continue
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: err %v, want %v", name, err, c.want)
		}
		if c.text != "" && !strings.Contains(err.Error(), c.text) {
			t.Errorf("%s: err %q doesn't say %q", name, err, c.text)
		}
		after := h.files(t)
		if len(after) != len(before) {
			t.Errorf("%s: the files went from %d to %d", name, len(before), len(after))
		}
		for n, content := range before {
			if after[n] != content {
				t.Errorf("%s: %s changed", name, n)
			}
		}
	}
	_ = good
	_ = ctx
}

// A snapshot from before an upgrade (which is what the pre-migration one is) restores, and is
// brought up to date when it goes in, so the daemon that starts on it finds the schema it expects.
func TestAnOlderSnapshotIsMigratedForward(t *testing.T) {
	ctx := context.Background()
	src := newHost(t)
	snap := snapshotOf(t, src)
	db, err := sql.Open("sqlite", snap)
	if err != nil {
		t.Fatal(err)
	}
	latest := store.LatestSchema()
	if latest != 8 {
		t.Skipf("this test takes back migration 8, and the newest is %d; update it", latest)
	}
	for _, q := range []string{`DROP TABLE api_tokens`, `DELETE FROM schema_migrations WHERE version = 8`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()

	h := &host{dir: t.TempDir(), secret: src.secret}
	h.db, h.key = filepath.Join(h.dir, "drawbridge.db"), filepath.Join(h.dir, "secret.key")
	if err := os.WriteFile(h.key, src.secret, 0o640); err != nil {
		t.Fatal(err)
	}
	res, err := h.restore(snap, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Migrated {
		t.Error("the result doesn't say it was migrated")
	}
	st := openHost(t, h.db, src.secret)
	if v, _ := st.SchemaVersion(ctx); v != latest {
		t.Errorf("the restored database is at schema %d, want %d", v, latest)
	}
}

func TestIsSnapshot(t *testing.T) {
	h := newHost(t)
	snap := snapshotOf(t, h)
	file := backupOf(t, h, store.LatestSchema())
	if ok, err := IsSnapshot(file); err != nil || ok {
		t.Errorf("an encrypted backup: %v, %v", ok, err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	_ = os.WriteFile(empty, nil, 0o600)
	if ok, err := IsSnapshot(empty); err != nil || ok {
		t.Errorf("an empty file: %v, %v", ok, err)
	}
	if _, err := IsSnapshot(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing file")
	}
	if ok, _ := IsSnapshot(snap); !ok {
		t.Error("a snapshot isn't one")
	}
}
