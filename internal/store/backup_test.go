package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/snapshot"
)

func TestLatestSchemaIsTheLastMigration(t *testing.T) {
	s := initialized(t)
	v, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != LatestSchema() || v < 8 {
		t.Fatalf("a new database is at schema %d, and the latest is %d; want them equal and at least 8", v, LatestSchema())
	}
}

// A snapshot is a whole database of its own: it opens with the same key, has everything the
// original had, and doesn't change when the original does.
func TestSnapshotIsACompleteIndependentCopy(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	c, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snap.db")
	if err := s.Snapshot(ctx, path); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the snapshot is %v, %v; want mode 0600", info, err)
	}
	if _, err := os.Stat(path + "-wal"); err == nil {
		t.Error("the snapshot has a WAL file of its own")
	}

	// It's the same database, and it opens with the same key, so a client's private key and
	// preshared key come out of it as they went in.
	snap, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if err := snap.CheckIntegrity(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := snap.Client(ctx, ByName("phone"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != c.ID || got.PrivateKey == nil || *got.PrivateKey != *c.PrivateKey || got.PresharedKey != c.PresharedKey {
		t.Fatalf("the snapshot's client is %+v, want %+v", got, c)
	}

	// And it's a copy: a client added afterward isn't in it.
	if _, err := s.AddClient(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}
	if clients, _ := snap.Clients(ctx); len(clients) != 1 {
		t.Errorf("the snapshot has %d clients after one was added to the original, want 1", len(clients))
	}
}

func TestSnapshotRefusesToOverwrite(t *testing.T) {
	s := initialized(t)
	path := filepath.Join(t.TempDir(), "taken.db")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Snapshot(context.Background(), path); err == nil {
		t.Fatal("a snapshot over an existing file")
	}
	if b, _ := os.ReadFile(path); string(b) != "keep me" {
		t.Errorf("the existing file became %q", b)
	}
}

func TestDeleteAllSessions(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	u, err := s.CreateUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, id := range []string{"s1", "s2"} {
		if err := s.CreateSession(ctx, Session{ID: id, TokenHash: []byte(id), UserID: u.ID,
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.DeleteAllSessions(ctx)
	if err != nil || n != 2 {
		t.Fatalf("deleted %d, %v; want 2", n, err)
	}
	if sessions, _ := s.Sessions(ctx, u.ID); len(sessions) != 0 {
		t.Errorf("%d sessions are left", len(sessions))
	}
}

// openAt opens the database at path, as an upgraded daemon would, with snapshots before a
// migration into dir.
func openAt(t *testing.T, path, dir string, keep int) (*Store, error) {
	t.Helper()
	return Open(context.Background(), path, testSealer(t, 1), WithMigrationSnapshots(dir, keep))
}

// An upgrade snapshots the database as it was, before the first migration touches it, so a
// migration that goes wrong has something to go back to.
func TestMigrationSnapshotIsTakenFirstAndHoldsTheOldSchema(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	old := LatestSchema() - 2
	rollBackTo(t, s, old)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "backups")
	up, err := openAt(t, path, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	m := up.Migration()
	if m == nil || m.From != old || m.To != LatestSchema() || m.Snapshot == "" {
		t.Fatalf("migration %+v, want from %d to %d with a snapshot", m, old, LatestSchema())
	}
	if v, _ := up.SchemaVersion(ctx); v != LatestSchema() {
		t.Errorf("the database is at schema %d, want %d", v, LatestSchema())
	}

	// The snapshot is the database as it was: the old schema, with the client in it.
	if !strings.HasPrefix(filepath.Base(m.Snapshot), "pre-migration-v"+strconv.Itoa(old)+"-") {
		t.Errorf("the snapshot is called %s", filepath.Base(m.Snapshot))
	}
	raw, err := sql.Open("sqlite", m.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var atSchema int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&atSchema); err != nil || atSchema != old {
		t.Errorf("the snapshot is at schema %d (%v), want the old %d", atSchema, err, old)
	}
	_ = raw.Close()
	// Opening it migrates it (it's a database like any other), so that came first.
	snap, err := Open(ctx, m.Snapshot, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := snap.Client(ctx, ByName("phone")); err != nil || got.ID != c.ID {
		t.Errorf("the snapshot's client: %+v, %v", got, err)
	}
	_ = snap.Close()
	info, err := os.Stat(m.Snapshot)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the snapshot is %v, %v; want mode 0600", info, err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the directory is %v, %v; want mode 0700", info, err)
	}
}

// Nothing is snapshotted when there's nothing to migrate, or nothing to lose.
func TestNoMigrationSnapshotWhenCurrentOrNew(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "backups")
	path := filepath.Join(t.TempDir(), "drawbridge.db")

	// A new database is created at the latest schema, and has no data to save.
	s, err := openAt(t, path, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if m := s.Migration(); m == nil || m.From != 0 || m.To != LatestSchema() || m.Snapshot != "" {
		t.Errorf("a new database: %+v", m)
	}
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := os.Stat(dir); err == nil {
		t.Error("a new database made a snapshot directory")
	}

	// And one that's current changes nothing.
	s, err = openAt(t, path, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Migration() != nil {
		t.Errorf("a current database reports a migration: %+v", s.Migration())
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a current database made a snapshot directory")
	}
}

// A snapshot that can't be made stops the migration, with the database as it was.
func TestMigrationIsRefusedWhenItCannotBeSnapshotted(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	old := LatestSchema() - 1
	rollBackTo(t, s, old)
	_ = s.Close()

	// A file where the directory should be.
	blocker := filepath.Join(t.TempDir(), "backups")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openAt(t, path, blocker, 0); err == nil || !strings.Contains(err.Error(), "snapshotting the database before migrating") {
		t.Fatalf("err = %v, want a refusal that says the snapshot failed", err)
	}
	// Opened without snapshots, it's still at the old schema: the failed attempt changed nothing.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var v int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil || v != old {
		t.Errorf("the database is at schema %d (%v), want %d", v, err, old)
	}
}

// Only the newest few pre-migration snapshots stay, and the nightly ones aren't touched.
func TestMigrationSnapshotsAreRotated(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	nightly := filepath.Join(dir, "nightly-20200101-030000.db")
	if err := os.WriteFile(nightly, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		// Take the newest migration back, and open again, as four upgrades in a row would.
		raw, err := Open(ctx, path, testSealer(t, 1))
		if err != nil {
			t.Fatal(err)
		}
		rollBackTo(t, raw, LatestSchema()-1)
		_ = raw.Close()
		clock := time.Date(2026, 10, 4, 12, i, 0, 0, time.UTC)
		up, err := Open(ctx, path, testSealer(t, 1), WithMigrationSnapshots(dir, 2), func(s *Store) { s.now = func() time.Time { return clock } })
		if err != nil {
			t.Fatal(err)
		}
		_ = up.Close()
	}
	all, _ := snapshot.List(dir)
	var pre, night int
	for _, s := range all {
		switch s.Kind {
		case snapshot.PreMigration:
			pre++
		case snapshot.Nightly:
			night++
		}
	}
	if pre != 2 || night != 1 {
		t.Errorf("%d pre-migration and %d nightly snapshots are left, want 2 and 1: %+v", pre, night, all)
	}
	// The ones kept are the newest.
	if all[0].MadeAt.Minute() != 3 {
		t.Errorf("the newest is %v, want the last upgrade's", all[0].MadeAt)
	}
}
