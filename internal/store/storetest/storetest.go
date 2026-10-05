// Package storetest builds the databases that older releases left behind, so the upgrade tests
// can open them with this build (docs/PLAN.md §12).
//
// A release can't be run again from here, so each schema version's data is written by hand, as
// raw SQL against exactly that version's tables, in the shape the release stored it: sealed
// secrets under their purposes, times in the format of the day, and rows in every table the
// version has. The same code reads the rows back through the current store (Verify), which is
// what an upgrade has to keep working.
//
// Everything is a feature: what one migration added (a table, or columns) with the rows to seed
// it and the check that reads them back. A new migration needs a feature, and
// TestEveryMigrationHasAFixture fails until it has one.
package storetest

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/store"

	_ "modernc.org/sqlite" // The driver the raw seeding and dumping use.
)

// Profile is what kind of host a database belongs to.
type Profile string

const (
	// Used is a host that was set up and has run for a while: an admin account, clients with
	// their keys, history, and every optional setting turned on.
	Used Profile = "used"
	// Unused is a host that was installed and never set up: the server's settings and the
	// setup token, with no account and no clients. It's the state most hosts are in for a
	// few minutes after the package installs, and an upgrade can land in it.
	Unused Profile = "unused"
)

// Profiles lists every profile.
var Profiles = []Profile{Used, Unused}

// Build writes a database at path as the release at schema version v left it, for a host of the
// given profile. The key that seals its secrets is sealer's. The database is closed, with no
// WAL beside it, when Build returns.
func Build(t testing.TB, path string, sealer *keys.Sealer, v int, p Profile) {
	t.Helper()
	ctx := context.Background()
	// Open creates the schema, and nothing else: it stops at v.
	st, err := store.Open(ctx, path, sealer, store.WithSchemaLimit(v))
	if err != nil {
		t.Fatalf("creating the schema at version %d: %v", v, err)
	}
	if got, err := st.SchemaVersion(ctx); err != nil || got != v {
		_ = st.Close()
		t.Fatalf("the database is at schema %d (%v), want %d", got, err, v)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Foreign keys on, as the store opens its own connection, so a seed that points at a row
	// that isn't there is caught here.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sd := &seeder{t: t, db: db, sealer: sealer}
	for _, f := range features {
		if f.since <= v && f.applies(p) {
			sd.feature = f.name
			f.seed(sd)
		}
	}
}

type options struct {
	endedLogins bool
	except      map[string]bool
}

// Option changes what Verify and Kept expect.
type Option func(*options)

// EndedLogins says every login was ended on purpose, as a restore does: Verify expects the
// session to be gone, and Kept doesn't count it as lost.
func EndedLogins() Option { return func(o *options) { o.endedLogins = true } }

// Except leaves the named tables out of Kept: the ones a running daemon keeps writing (the
// connection history and the traffic buckets), which a comparison made after it started would
// find changed.
func Except(tables ...string) Option {
	return func(o *options) {
		if o.except == nil {
			o.except = map[string]bool{}
		}
		for _, t := range tables {
			o.except[t] = true
		}
	}
}

func optionsOf(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Verify reads back, through the current store, everything Build(…, v, p) wrote, and fails the
// test on whatever is missing or changed. The store may be at any later schema.
func Verify(t testing.TB, s *store.Store, v int, p Profile, opts ...Option) {
	t.Helper()
	o := optionsOf(opts)
	for _, f := range features {
		if f.since <= v && f.applies(p) {
			f.check(&checker{t: t, ctx: context.Background(), s: s, feature: f.name, opts: o})
		}
	}
}

// Versions lists the schema versions that have a fixture of their own: the versions of the
// features, which is every migration's version when the fixtures are complete.
func Versions() []int {
	seen := map[int]bool{}
	for _, f := range features {
		seen[f.since] = true
	}
	var out []int
	for v := range seen {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// Tables is every row of every table, as text, so two databases can be compared.
type Tables map[string]Table

// Table is one table's columns, in order, and its rows, each as one line of text.
type Table struct {
	Columns []string
	Rows    []string
}

// Rows reads every table in the SQLite file at path (the migrations' own bookkeeping aside).
func Rows(t testing.TB, path string) Tables {
	t.Helper()
	db := openRaw(t, path)
	defer db.Close()
	out := Tables{}
	for _, name := range tableNames(t, db) {
		cols := columnsOf(t, db, name)
		rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM %q`, quoteList(cols), name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		tab := Table{Columns: cols}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			tab.Rows = append(tab.Rows, renderRow(vals))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		rows.Close()
		sort.Strings(tab.Rows)
		out[name] = tab
	}
	return out
}

// Kept fails the test for every row of before that isn't in the database at path: a table, or
// a row of it, that an upgrade lost or changed. The upgrade may have added tables and columns,
// so only the columns before had are compared, and the rows it added are ignored.
//
// A migration that changes old rows on purpose breaks this, and the way to say so is to change
// what the old fixture holds, not to loosen the check.
func Kept(t testing.TB, before Tables, path string, opts ...Option) {
	t.Helper()
	o := optionsOf(opts)
	db := openRaw(t, path)
	defer db.Close()
	for _, name := range sortedKeys(before) {
		if o.endedLogins && name == "auth_sessions" || o.except[name] {
			continue
		}
		tab := before[name]
		have := map[string]int{}
		rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM %q`, quoteList(tab.Columns), name))
		if err != nil {
			t.Errorf("table %s: %v (an upgrade must not drop a table or a column that has data; migrate it instead)", name, err)
			continue
		}
		for rows.Next() {
			vals := make([]any, len(tab.Columns))
			ptrs := make([]any, len(tab.Columns))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			have[renderRow(vals)]++
		}
		rows.Close()
		for _, row := range tab.Rows {
			if have[row] == 0 {
				t.Errorf("table %s lost or changed a row in the upgrade: %s", name, row)
				continue
			}
			have[row]--
		}
	}
}

// MakeNewer makes the database at path look as if a newer Drawbridge, whose schema is version v,
// had last used it: it records a migration this build doesn't have. Nothing else in it changes,
// which is how a database from a newer release looks to an older one, when the newer release's
// migrations only add.
func MakeNewer(t testing.TB, path string, v int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, '2026-10-05T00:00:00.000000Z')`, v); err != nil {
		t.Fatal(err)
	}
}

// Version is the schema version of the SQLite file at path, read without migrating it.
func Version(t testing.TB, path string) int {
	t.Helper()
	db := openRaw(t, path)
	defer db.Close()
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatalf("reading the schema version of %s: %v", path, err)
	}
	return v
}

// Migrations is how many migrations the file at path records as applied.
func Migrations(t testing.TB, path string) int {
	t.Helper()
	db := openRaw(t, path)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("counting the migrations of %s: %v", path, err)
	}
	return n
}

// Schema is the database's tables, indexes, and triggers as SQLite stores them, one per line,
// so two databases can be compared.
func Schema(t testing.TB, path string) []string {
	t.Helper()
	db := openRaw(t, path)
	defer db.Close()
	rows, err := db.Query(`SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ, name, tbl, def string
		if err := rows.Scan(&typ, &name, &tbl, &def); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join([]string{typ, name, tbl, strings.Join(strings.Fields(def), " ")}, " | "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// ForeignKeyViolations lists the rows whose foreign key points at nothing.
func ForeignKeyViolations(t testing.TB, path string) []string {
	t.Helper()
	db := openRaw(t, path)
	defer db.Close()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var table, parent string
		var rowid, fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s row %d points at a missing row of %s", table, rowid.Int64, parent))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func openRaw(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

// tableNames lists the tables worth comparing: not SQLite's own, and not the migrations'
// bookkeeping, which grows by design.
func tableNames(t testing.TB, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func columnsOf(t testing.TB, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, typ        string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func quoteList(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = fmt.Sprintf("%q", c)
	}
	return strings.Join(q, ", ")
}

// renderRow writes a row as one line, with blobs in hex so it reads and compares the same
// everywhere.
func renderRow(vals []any) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		switch v := v.(type) {
		case nil:
			parts[i] = "NULL"
		case []byte:
			parts[i] = fmt.Sprintf("x'%x'", v)
		case string:
			parts[i] = fmt.Sprintf("%q", v)
		default:
			parts[i] = fmt.Sprint(v)
		}
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m Tables) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
