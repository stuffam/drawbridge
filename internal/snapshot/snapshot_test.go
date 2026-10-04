package snapshot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func at(h, m, s int) time.Time { return time.Date(2026, 10, 4, h, m, s, 0, time.UTC) }

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	all, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range all {
		out = append(out, s.Name)
	}
	return out
}

func TestNamesRoundTrip(t *testing.T) {
	for _, c := range []struct {
		kind   string
		schema int
		want   string
	}{
		{Nightly, 0, "nightly-20261004-030507.db"},
		{Nightly, 9, "nightly-20261004-030507.db"}, // a nightly name has no schema
		{PreMigration, 7, "pre-migration-v7-20261004-030507.db"},
	} {
		got := Name(c.kind, c.schema, at(3, 5, 7).In(time.FixedZone("x", 3600)))
		if got != c.want {
			t.Errorf("Name(%s, %d) = %q, want %q", c.kind, c.schema, got, c.want)
		}
		info, ok := parse(got)
		if !ok || info.Kind != c.kind || !info.MadeAt.Equal(at(3, 5, 7)) {
			t.Errorf("parse(%q) = %+v, %v", got, info, ok)
		}
	}
	if info, _ := parse("pre-migration-v12-20261004-030507.db"); info.Schema != 12 {
		t.Errorf("schema %d, want 12", info.Schema)
	}
}

// Only what the package made is a snapshot: a file an admin dropped in the directory, or one
// that only looks a little like one, is left alone, listed or pruned.
func TestOnlyItsOwnFilesAreSnapshots(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"nightly-20261004-030000.db", "pre-migration-v7-20261004-030000.db",
		"notes.txt", "nightly-20261004.db", "nightly-20261004-030000.db.bak", "my-nightly-20261004-030000.db",
		"pre-migration-20261004-030000.db", "nightly-v3-20261004-030000.db", "nightly-20261340-990000.db",
	} {
		touch(t, dir, name)
	}
	if err := os.Mkdir(filepath.Join(dir, "nightly-20251004-030000.db"), 0o700); err != nil { // a directory
		t.Fatal(err)
	}
	want := []string{"nightly-20261004-030000.db", "pre-migration-v7-20261004-030000.db"}
	got := names(t, dir)
	if len(got) != 2 {
		t.Fatalf("listed %v, want %v", got, want)
	}
	// Pruning to one of each deletes nothing here, and never the foreign files.
	for _, k := range []string{Nightly, PreMigration} {
		if removed, err := Prune(dir, k, 1); err != nil || len(removed) != 0 {
			t.Errorf("Prune(%s) removed %v, %v", k, removed, err)
		}
	}
	for _, name := range []string{"notes.txt", "nightly-20261004.db", "nightly-20261004-030000.db.bak", "my-nightly-20261004-030000.db"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s is gone", name)
		}
	}
}

func TestListIsNewestFirstAndHasSizes(t *testing.T) {
	dir := t.TempDir()
	if got, err := List(filepath.Join(dir, "none")); err != nil || got != nil {
		t.Fatalf("a missing directory: %v, %v", got, err)
	}
	touch(t, dir, Name(Nightly, 0, at(3, 0, 0)))
	touch(t, dir, Name(PreMigration, 7, at(13, 0, 0)))
	touch(t, dir, Name(Nightly, 0, at(3, 0, 0).AddDate(0, 0, 1)))
	all, _ := List(dir)
	if len(all) != 3 || all[0].Kind != Nightly || all[1].Kind != PreMigration || all[2].Kind != Nightly ||
		!all[0].MadeAt.After(all[1].MadeAt) || !all[1].MadeAt.After(all[2].MadeAt) {
		t.Fatalf("order %+v", all)
	}
	if all[0].Size != int64(len(all[0].Name)) || all[1].Schema != 7 {
		t.Errorf("size or schema: %+v", all)
	}
	if l, ok, err := Latest(dir, Nightly); err != nil || !ok || l.Name != all[0].Name {
		t.Errorf("latest nightly %+v, %v, %v", l, ok, err)
	}
	if _, ok, _ := Latest(dir, "other"); ok {
		t.Error("a latest of a kind there isn't")
	}
}

func TestPruneKeepsTheNewestOfOneKind(t *testing.T) {
	dir := t.TempDir()
	for d := 1; d <= 5; d++ {
		touch(t, dir, Name(Nightly, 0, at(3, 0, 0).AddDate(0, 0, d)))
	}
	touch(t, dir, Name(PreMigration, 7, at(1, 0, 0)))
	touch(t, dir, Name(PreMigration, 8, at(2, 0, 0)))

	removed, err := Prune(dir, Nightly, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"nightly-20261007-030000.db", "nightly-20261006-030000.db", "nightly-20261005-030000.db"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %v, want %v", removed, want)
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"nightly-20261009-030000.db", "nightly-20261008-030000.db",
		"pre-migration-v8-20261004-020000.db", "pre-migration-v7-20261004-010000.db"}) {
		t.Errorf("left %v", got)
	}
	// No limit keeps everything, rather than deleting it all.
	for _, keep := range []int{0, -1} {
		if removed, err := Prune(dir, PreMigration, keep); err != nil || len(removed) != 0 {
			t.Errorf("keep %d removed %v, %v", keep, removed, err)
		}
	}
}

func TestNewPathNeverReusesAName(t *testing.T) {
	dir := t.TempDir()
	first := NewPath(dir, PreMigration, 7, at(3, 0, 0))
	if filepath.Base(first) != "pre-migration-v7-20261004-030000.db" {
		t.Fatalf("first %s", first)
	}
	touch(t, dir, filepath.Base(first))
	second := NewPath(dir, PreMigration, 7, at(3, 0, 0))
	touch(t, dir, filepath.Base(second))
	third := NewPath(dir, PreMigration, 7, at(3, 0, 0))
	if second == first || third == second || third == first {
		t.Fatalf("%s, %s, %s", first, second, third)
	}
	// All three are snapshots, in order, with the same time.
	all, _ := List(dir)
	if len(all) != 2 || !all[0].MadeAt.Equal(all[1].MadeAt) {
		t.Errorf("listed %+v", all)
	}
}
