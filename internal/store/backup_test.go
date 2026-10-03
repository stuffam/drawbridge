package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
