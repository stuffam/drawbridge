package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stuffam/drawbridge/internal/snapshot"
)

// released pins what each migration says, as the SHA-256 of its statements (comments and
// whitespace left out). A database that was at schema N before a build ran has the first N
// migrations as they were then, so a change to one of them gives it a schema a new install never
// has, and a new install a schema an upgrade never reaches. A change goes in a new migration.
//
// An entry is added with the migration and never edited. To correct a mistake in a migration that
// hasn't shipped, change both; once it has, add a migration that fixes it.
var released = map[int]string{
	1:  "a34a7da6c9ec201fe52be97d75bfa1b6499a49f78ad4aad36968938b319c1428",
	2:  "145f08f2de7e0b3ef11de1ed8d9b5b606d3ab9ef2bf6b8d8712e4ba139c55c14",
	3:  "be0d892b05d614c7fba335b9641ccfcbfe8fc4fab897c93db374827dee10b231",
	4:  "390c31c5d8e6bb9d764c9771bcdbe6839981d9d3e92a276a3f5b9d683ccc24d3",
	5:  "61838ebbcc32e6c39d06a641bee2f18113bc89bbd1d08a66e1ca27ea0ad8384d",
	6:  "e57b8aaed4daa2fb29c31fa8f5b8780a68dfcd58e842732b670a6effb13fa545",
	7:  "55ca51259552f6ff84f17d014ac9226b459c5063262199d8e6ce814e6a4baaf8",
	8:  "f291667c8929b2c81cc777424407062e6f0862b8d66db7f446fbc986359384a2",
	9:  "d9bb7110e9098680c31ee915122718255b0f0d18c7122626eb749bcd1109b184",
	10: "874a21d4b7211ccf4a12a16f0927a7be87560404efac09cec5e6a1605021b8c5",
	11: "972b574821ba9506670e541a06d8c9e0f07fd0d3e9b874553aa144dbe22b3843",
}

var lineComment = regexp.MustCompile(`--[^\n]*`)

// statements is a migration's text without its comments and with whitespace collapsed, so a
// reworded comment doesn't count as a change.
func statements(text string) string {
	return strings.Join(strings.Fields(lineComment.ReplaceAllString(text, "")), " ")
}

func migrationVersions(t *testing.T) map[int]string {
	t.Helper()
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	name := regexp.MustCompile(`^migrations/([0-9]{4})_[a-z0-9_]+\.sql$`)
	out := map[int]string{}
	for _, f := range files {
		m := name.FindStringSubmatch(f)
		if m == nil {
			t.Errorf("%s isn't named NNNN_words_with_underscores.sql", f)
			continue
		}
		v, _ := strconv.Atoi(m[1])
		if other, dup := out[v]; dup {
			t.Errorf("migrations %s and %s are both version %d (two branches added one each? renumber the later one)", other, f, v)
		}
		out[v] = f
	}
	return out
}

func TestMigrationsAreNumberedWithoutGaps(t *testing.T) {
	files := migrationVersions(t)
	for v := 1; v <= len(files); v++ {
		if _, ok := files[v]; !ok {
			t.Errorf("no migration %04d: the versions have a gap, and a database records only the highest one it applied, so the numbers must run 1..N", v)
		}
	}
}

func TestReleasedMigrationsAreNeverChanged(t *testing.T) {
	files := migrationVersions(t)
	for v := 1; v <= len(files); v++ {
		text, err := fs.ReadFile(migrationFiles, files[v])
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(statements(string(text))))
		got := hex.EncodeToString(sum[:])
		want, pinned := released[v]
		switch {
		case !pinned:
			t.Errorf("migration %s isn't pinned: add %d: %q to released", files[v], v, got)
		case want != got:
			t.Errorf("migration %s changed (its statements hash to %s, pinned %s): a database that already applied it keeps the old one, so put the change in a new migration", files[v], got, want)
		}
	}
	for v := range released {
		if _, ok := files[v]; !ok {
			t.Errorf("released lists migration %d, which doesn't exist: a migration is never removed", v)
		}
	}
}

// withMigrations makes a test supply the migrations.
func withMigrations(fsys fs.FS) Option { return func(s *Store) { s.migrations = fsys } }

// embedded returns the real migrations as a map a test can add to.
func embedded(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := fs.ReadFile(migrationFiles, f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = &fstest.MapFile{Data: b}
	}
	return out
}

// A migration that fails halfway leaves nothing of itself behind, and the database keeps the
// snapshot of what it was; once the migration is fixed, the upgrade goes through.
func TestAMigrationThatFailsHalfwayChangesNothing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "drawbridge.db")
	backups := filepath.Join(dir, "backups")
	old := LatestSchema()

	s, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// Two statements work and the third doesn't.
	next := fmt.Sprintf("migrations/%04d_next.sql", old+1)
	broken := embedded(t)
	broken[next] = &fstest.MapFile{Data: []byte(`
		ALTER TABLE clients ADD COLUMN nickname TEXT NOT NULL DEFAULT '';
		CREATE TABLE half_done (id INTEGER PRIMARY KEY);
		INSERT INTO no_such_table VALUES (1);`)}
	if _, err := Open(ctx, path, testSealer(t, 1), WithMigrationSnapshots(backups, 3), withMigrations(broken)); err == nil ||
		!strings.Contains(err.Error(), "next.sql") || !strings.Contains(err.Error(), "no_such_table") {
		t.Fatalf("the failing migration opened: %v", err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version, columns, tables int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != old {
		t.Errorf("the database is at schema %d (%v), want the old %d", version, err, old)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('clients') WHERE name = 'nickname'`).Scan(&columns); err != nil || columns != 0 {
		t.Errorf("the failed migration's first statement stayed: %d nickname columns (%v)", columns, err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'half_done'`).Scan(&tables); err != nil || tables != 0 {
		t.Errorf("the failed migration's second statement stayed: %d half_done tables (%v)", tables, err)
	}
	snaps, _ := snapshot.List(backups)
	if len(snaps) != 1 || snaps[0].Schema != old {
		t.Errorf("snapshots %+v, want the one from before, at schema %d", snaps, old)
	}

	// The fix: the migration without its last statement.
	fixed := embedded(t)
	fixed[next] = &fstest.MapFile{Data: []byte(`
		ALTER TABLE clients ADD COLUMN nickname TEXT NOT NULL DEFAULT '';
		CREATE TABLE half_done (id INTEGER PRIMARY KEY);`)}
	again, err := Open(ctx, path, testSealer(t, 1), WithMigrationSnapshots(backups, 3), withMigrations(fixed))
	if err != nil {
		t.Fatalf("opening after the fix: %v", err)
	}
	defer again.Close()
	if v, _ := again.SchemaVersion(ctx); v != old+1 {
		t.Errorf("after the fix the database is at schema %d, want %d", v, old+1)
	}
	if m := again.Migration(); m == nil || m.From != old || m.To != old+1 {
		t.Errorf("the upgrade is reported as %+v", m)
	}
	if c, err := again.Client(ctx, ByName("phone")); err != nil || c.PrivateKey == nil {
		t.Errorf("the client after the upgrade: %+v, %v", c, err)
	}
}

// A database from a newer build is left exactly as it is: no migration, no snapshot, no write,
// and the store says so, so that the daemon can refuse and the tunnel unit can go on.
func TestADatabaseFromANewerBuildIsLeftAlone(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "drawbridge.db")
	backups := filepath.Join(dir, "backups")
	known := LatestSchema()

	// A newer build: one more migration than this one has.
	newer := embedded(t)
	newer[fmt.Sprintf("migrations/%04d_next.sql", known+1)] = &fstest.MapFile{Data: []byte(`ALTER TABLE clients ADD COLUMN nickname TEXT NOT NULL DEFAULT '';`)}
	s, err := Open(ctx, path, testSealer(t, 1), withMigrations(newer))
	if err != nil {
		t.Fatal(err)
	}
	if s.NewerSchema() != 0 {
		t.Fatalf("the newer build sees its own schema as newer: %d", s.NewerSchema())
	}
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	before := rawRows(t, path)

	// This build opens it.
	old, err := Open(ctx, path, testSealer(t, 1), WithMigrationSnapshots(backups, 3))
	if err != nil {
		t.Fatalf("opening a newer database: %v", err)
	}
	defer old.Close()
	if got := old.NewerSchema(); got != known+1 {
		t.Errorf("NewerSchema is %d, want %d", got, known+1)
	}
	if old.Migration() != nil {
		t.Errorf("a newer database was migrated: %+v", old.Migration())
	}
	if snaps, _ := snapshot.List(backups); len(snaps) != 0 {
		t.Errorf("a newer database was snapshotted: %v", snaps)
	}
	if v, _ := old.SchemaVersion(ctx); v != known+1 {
		t.Errorf("the schema went from %d to %d", known+1, v)
	}
	// It still reads, which is all the tunnel does.
	if c, err := old.Client(ctx, ByName("phone")); err != nil || c.PrivateKey == nil {
		t.Errorf("reading the client: %+v, %v", c, err)
	}
	if after := rawRows(t, path); after != before {
		t.Errorf("opening a newer database changed it:\nbefore: %s\nafter:  %s", before, after)
	}

	// A current database says nothing is newer.
	cur, _ := openTest(t)
	if cur.NewerSchema() != 0 {
		t.Errorf("a current database reports a newer schema: %d", cur.NewerSchema())
	}
}

// rawRows is the schema versions and every client row, as text, to show a database wasn't
// written to.
func rawRows(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var out []string
	for _, q := range []string{`SELECT version, applied_at FROM schema_migrations ORDER BY version`,
		`SELECT id, name, updated_at, nickname FROM clients ORDER BY id`} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(vals...))
		}
		rows.Close()
	}
	return strings.Join(out, "\n")
}
