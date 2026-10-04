package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/snapshot"
	"github.com/stuffam/drawbridge/internal/store"
)

func withSnapshots(t *testing.T, s *Service) string {
	t.Helper()
	s.SnapshotDir = filepath.Join(t.TempDir(), "backups")
	return s.SnapshotDir
}

func nightly(t *testing.T, dir string) []snapshot.Info {
	t.Helper()
	all, err := snapshot.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []snapshot.Info
	for _, s := range all {
		if s.Kind == snapshot.Nightly {
			out = append(out, s)
		}
	}
	return out
}

// A nightly snapshot is a database of its own: it opens with the host's key and has the clients
// the live one had when it was made.
func TestSnapshotDatabaseIsAWholeDatabase(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestService(t)
	dir := withSnapshots(t, s)
	if _, _, err := s.AddClient(web(ctx, "admin"), "a phone"); err != nil {
		t.Fatal(err)
	}
	info, err := s.SnapshotDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "nightly-20260926-120000.db" || info.Kind != snapshot.Nightly || info.Size == 0 {
		t.Fatalf("info %+v", info)
	}
	if fi, err := os.Stat(filepath.Join(dir, info.Name)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file is %v, %v; want mode 0600", fi, err)
	}
	sealer, _ := keys.NewSealer(bytes.Repeat([]byte{7}, keys.SecretSize))
	st, err := store.Open(ctx, filepath.Join(dir, info.Name), sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Client(ctx, store.ByName("a phone")); err != nil {
		t.Errorf("the snapshot lacks the client: %v", err)
	}
	// It's from a moment: a client added later isn't in it.
	clk.advance(time.Hour)
	if _, _, err := s.AddClient(web(ctx, "admin"), "a laptop"); err != nil {
		t.Fatal(err)
	}
	if clients, _ := st.Clients(ctx); len(clients) != 1 {
		t.Errorf("the snapshot has %d clients, want the 1 from when it was made", len(clients))
	}
}

func TestSnapshotsAreKeptToALimit(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestService(t)
	dir := withSnapshots(t, s)
	s.SnapshotKeep = 3
	// A pre-migration snapshot and a file of the admin's, which pruning leaves alone.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pre-migration-v7-20260101-000000.db", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		clk.advance(24 * time.Hour)
		if _, err := s.SnapshotDatabase(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got := nightly(t, dir)
	if len(got) != 3 || got[0].Name != "nightly-20261001-120000.db" || got[2].Name != "nightly-20260929-120000.db" {
		t.Fatalf("nightly snapshots %+v, want the newest three", got)
	}
	for _, name := range []string{"pre-migration-v7-20260101-000000.db", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was deleted", name)
		}
	}
}

// The daemon makes one when the newest is a day old, and not before, and a restart in between
// doesn't make another: it goes by the files, not by a timer.
func TestSnapshotIfDue(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestService(t)
	dir := withSnapshots(t, s)

	if due, _ := s.SnapshotDue(); !due {
		t.Fatal("not due with no snapshot")
	}
	if made, err := s.SnapshotIfDue(ctx); !made || err != nil {
		t.Fatalf("the first: %v, %v", made, err)
	}
	for _, after := range []time.Duration{time.Minute, 12 * time.Hour, 23*time.Hour + 59*time.Minute} {
		clk.t = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC).Add(after)
		if made, err := s.SnapshotIfDue(ctx); made || err != nil {
			t.Fatalf("%v later: made %v, %v; want nothing yet", after, made, err)
		}
	}
	clk.t = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if made, err := s.SnapshotIfDue(ctx); !made || err != nil {
		t.Fatalf("a day later: %v, %v", made, err)
	}
	if got := nightly(t, dir); len(got) != 2 {
		t.Errorf("%d snapshots, want 2", len(got))
	}

	// A different interval changes when it's due.
	s.SnapshotInterval = time.Hour
	clk.advance(61 * time.Minute)
	if due, _ := s.SnapshotDue(); !due {
		t.Error("an hour interval isn't due after 61 minutes")
	}
}

func TestSnapshotsCanBeTurnedOff(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	if s.SnapshotsOn() {
		t.Error("on with no directory")
	}
	dir := withSnapshots(t, s)
	s.SnapshotInterval = -1
	if s.SnapshotsOn() {
		t.Error("on with a negative interval")
	}
	if made, err := s.SnapshotIfDue(ctx); made || err != nil {
		t.Errorf("an off service made one: %v, %v", made, err)
	}
	// And the loop returns at once, instead of waiting for nothing.
	done := make(chan struct{})
	go func() { s.RunSnapshots(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunSnapshots didn't return for an off service")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("an off service made the directory")
	}
}

func TestRunSnapshotsStopsWhenAsked(t *testing.T) {
	s, _ := newTestService(t)
	withSnapshots(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunSnapshots(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunSnapshots kept running after its context ended")
	}
}

// A snapshot that fails (a full disk, say) is an error and leaves no half-written file.
func TestFailedSnapshotLeavesNothing(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	blocker := filepath.Join(t.TempDir(), "backups")
	if err := os.WriteFile(blocker, []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.SnapshotDir = blocker
	if _, err := s.SnapshotDatabase(ctx); err == nil {
		t.Fatal("a snapshot into a file")
	}
	if made, err := s.SnapshotIfDue(ctx); made || err == nil {
		t.Errorf("SnapshotIfDue: made %v, err %v; want an error", made, err)
	}
}

// The page's list is what's in the directory, newest first, and it never has a file the daemon
// didn't make: an admin's own file in that directory isn't a snapshot.
func TestListSnapshots(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestService(t)

	// A daemon with no directory keeps none, and says so with an empty list, not an error.
	l, err := s.ListSnapshots()
	if err != nil || l.Dir != "" || l.Nightly || l.Items == nil || len(l.Items) != 0 {
		t.Fatalf("no directory: %+v, %v", l, err)
	}

	dir := withSnapshots(t, s)
	if l, err = s.ListSnapshots(); err != nil || l.Dir != dir || !l.Nightly || l.Items == nil || len(l.Items) != 0 {
		t.Fatalf("no snapshots yet: %+v, %v", l, err)
	}
	if _, err := s.SnapshotDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	clk.advance(24 * time.Hour)
	if _, err := s.SnapshotDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	pre := snapshot.NewPath(dir, snapshot.PreMigration, 3, clk.now().Add(-time.Hour))
	if err := os.WriteFile(pre, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "my-own-copy.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err = s.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range l.Items {
		names = append(names, i.Name)
	}
	want := []string{"nightly-20260927-120000.db", "pre-migration-v3-20260927-110000.db", "nightly-20260926-120000.db"}
	if !slices.Equal(names, want) {
		t.Errorf("snapshots %v, want %v", names, want)
	}
	if l.Items[1].Schema != 3 || l.Items[0].Size == 0 {
		t.Errorf("items %+v", l.Items)
	}

	// With the nightly job off, the ones that are there still show, and the page can say it's off.
	s.SnapshotInterval = -1
	if l, err = s.ListSnapshots(); err != nil || l.Nightly || len(l.Items) != 3 {
		t.Errorf("nightly off: %+v, %v", l, err)
	}
}
