package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/snapshot"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/store/storetest"
)

func sealer(t *testing.T) *keys.Sealer {
	t.Helper()
	s, err := keys.NewSealer(bytes.Repeat([]byte{7}, keys.SecretSize))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestEveryMigrationHasAFixture fails until a new migration has a feature in storetest: rows
// for what it adds, in the shape the release stored them, and the check that reads them back.
// Without that the upgrade matrix would pass over the new migration without exercising it.
func TestEveryMigrationHasAFixture(t *testing.T) {
	var want []int
	for v := 1; v <= store.LatestSchema(); v++ {
		want = append(want, v)
	}
	if got := storetest.Versions(); !slices.Equal(got, want) {
		t.Errorf("the fixtures cover schema versions %v, and the migrations are %v: add a feature to internal/store/storetest for each migration that has none", got, want)
	}
}

// TestUpgradeFromEverySchema is the upgrade matrix: a database as each earlier release left it,
// for a host that had been used and one that had never been set up, opened by this build. An
// upgrade has to keep every row and read every one back, leave the schema the way a new
// install has it, and leave the database as usable as before.
func TestUpgradeFromEverySchema(t *testing.T) {
	for _, p := range storetest.Profiles {
		for v := 1; v <= store.LatestSchema(); v++ {
			t.Run(fmt.Sprintf("%s host from schema %d", p, v), func(t *testing.T) { upgradeFrom(t, v, p) })
		}
	}
}

func upgradeFrom(t *testing.T, v int, p storetest.Profile) {
	ctx := context.Background()
	latest := store.LatestSchema()
	dir := t.TempDir()
	path := filepath.Join(dir, "drawbridge.db")
	backups := filepath.Join(dir, "backups")
	storetest.Build(t, path, sealer(t), v, p)
	before := storetest.Rows(t, path)
	if len(before) == 0 {
		t.Fatal("the old database has no tables")
	}

	st, err := store.Open(ctx, path, sealer(t), store.WithMigrationSnapshots(backups, 3))
	if err != nil {
		t.Fatalf("opening the schema %d database: %v", v, err)
	}
	defer st.Close()

	// What Open says it did, and the snapshot it took first.
	m := st.Migration()
	snaps, _ := snapshot.List(backups)
	if v == latest {
		if m != nil || len(snaps) != 0 {
			t.Errorf("a current database was migrated (%+v) or snapshotted (%v)", m, snaps)
		}
	} else {
		if m == nil || m.From != v || m.To != latest || m.Snapshot == "" {
			t.Fatalf("the upgrade is reported as %+v, want from %d to %d with a snapshot", m, v, latest)
		}
		if len(snaps) != 1 || snaps[0].Kind != snapshot.PreMigration || snaps[0].Schema != v {
			t.Errorf("the snapshots are %+v, want one before the migration from schema %d", snaps, v)
		}
		// The snapshot is the database as it was: the old schema, and every row.
		if got := storetest.Version(t, m.Snapshot); got != v {
			t.Errorf("the snapshot is at schema %d, want %d", got, v)
		}
		if got := storetest.Rows(t, m.Snapshot); !reflect.DeepEqual(got, before) {
			t.Errorf("the snapshot isn't the old database: %v", diffTables(before, got))
		}
	}

	// What the upgrade did.
	if got, err := st.SchemaVersion(ctx); err != nil || got != latest {
		t.Fatalf("the database is at schema %d (%v), want %d", got, err, latest)
	}
	storetest.Verify(t, st, v, p)
	storetest.Kept(t, before, path)
	if err := st.CheckIntegrity(ctx); err != nil {
		t.Error(err)
	}
	if bad := storetest.ForeignKeyViolations(t, path); len(bad) > 0 {
		t.Errorf("the upgrade left rows that point at nothing: %v", bad)
	}
	fresh := filepath.Join(dir, "fresh.db")
	f, err := store.Open(ctx, fresh, sealer(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got, want := storetest.Schema(t, path), storetest.Schema(t, fresh); !slices.Equal(got, want) {
		t.Errorf("the upgraded schema differs from a new install's:\n%s", diffLines(want, got))
	}

	usable(t, st, v, p)

	// Opening it again changes nothing: no migration, and no second snapshot.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(ctx, path, sealer(t), store.WithMigrationSnapshots(backups, 3))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Migration() != nil {
		t.Errorf("a second open migrated again: %+v", again.Migration())
	}
	if after, _ := snapshot.List(backups); len(after) != len(snaps) {
		t.Errorf("a second open made a snapshot: %v", after)
	}
}

// usable does what the admin does next: a host that was set up adds a client and changes a
// setting, and one that wasn't finishes setting up.
func usable(t *testing.T, st *store.Store, v int, p storetest.Profile) {
	t.Helper()
	ctx := context.Background()
	if p == storetest.Unused {
		// The daemon asks for the token when it starts. A host from before schema 2 never had
		// one, so it gets a new one; every later host's is the one it was waiting with.
		token, err := st.EnsureSetupToken(ctx, "a-new-token")
		if err != nil {
			t.Fatalf("asking for the setup token: %v", err)
		}
		if want := map[bool]string{true: "the-first-run-token", false: "a-new-token"}[v >= 2]; token != want {
			t.Errorf("the setup token is %q, want %q", token, want)
		}
		hash := "$argon2id$v=19$m=1024,t=1,p=1$ZHJhd2JyaWRnZS1maXhlZA$RP0K5sTWytWiUHt0eD4sxL0tM3s22nfV9CE2nOnkzDY"
		if _, err := st.CompleteSetup(ctx, token, "admin", hash); err != nil {
			t.Errorf("finishing the setup that was waiting: %v", err)
		}
		return
	}

	before, err := st.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	added, err := st.AddClient(ctx, "tablet")
	if err != nil {
		t.Fatalf("adding a client: %v", err)
	}
	for _, c := range before {
		if c.IPv4 == added.IPv4 || c.IPv6 == added.IPv6 && added.IPv6.IsValid() {
			t.Errorf("the new client got %v and %v, which %s already has", added.IPv4, added.IPv6, c.Name)
		}
	}
	if _, err := st.AddClient(ctx, "PHONE"); !errors.Is(err, store.ErrNameTaken) {
		t.Errorf("a name that exists in another case: %v, want ErrNameTaken", err)
	}

	// A change that was waiting when the old release stopped still has to be kept or undone.
	if v >= 10 {
		if _, err := st.UpdateSettings(ctx, func(s *model.Settings) error { s.MTU = 1380; return nil }); !errors.Is(err, store.ErrChangePending) {
			t.Errorf("a settings change with another waiting: %v, want ErrChangePending", err)
		}
		undone, err := st.ResolvePending(ctx, false)
		if err != nil {
			t.Fatalf("undoing the change that was waiting: %v", err)
		}
		if undone.Previous.ListenPort != 51820 {
			t.Errorf("undoing it restored port %d, want 51820", undone.Previous.ListenPort)
		}
		if s, err := st.Settings(ctx); err != nil || s.ListenPort != 51820 {
			t.Errorf("after the undo the port is %d (%v), want 51820", s.ListenPort, err)
		}
	}
	got, err := st.UpdateSettings(ctx, func(s *model.Settings) error { s.MTU = 1380; return nil })
	if err != nil || got.MTU != 1380 {
		t.Errorf("changing a setting: MTU %d, %v", got.MTU, err)
	}
}

// TestTwoProcessesUpgradingTheSameDatabaseAtOnce is the tunnel unit and the daemon starting
// together on a database that's behind: systemd orders them, but nothing else does, and the one
// that loses must not fail on what the other already did.
func TestTwoProcessesUpgradingTheSameDatabaseAtOnce(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "drawbridge.db")
	backups := filepath.Join(dir, "backups")
	storetest.Build(t, path, sealer(t), 1, storetest.Used)
	before := storetest.Rows(t, path)

	const openers = 4
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		stores  = make([]*store.Store, openers)
		failure = make([]error, openers)
	)
	for i := range openers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			stores[i], failure[i] = store.Open(ctx, path, sealer(t), store.WithMigrationSnapshots(backups, 10))
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range failure {
		if err != nil {
			t.Errorf("opener %d: %v", i, err)
		}
	}
	for _, st := range stores {
		if st != nil {
			defer st.Close()
		}
	}
	if t.Failed() {
		return
	}
	storetest.Verify(t, stores[0], 1, storetest.Used)
	storetest.Kept(t, before, path)
	if got := storetest.Version(t, path); got != store.LatestSchema() {
		t.Errorf("the database is at schema %d, want %d", got, store.LatestSchema())
	}
	if n := storetest.Migrations(t, path); n != store.LatestSchema() {
		t.Errorf("%d migrations are recorded, want one each for %d", n, store.LatestSchema())
	}
	// Whoever snapshotted it, a snapshot is what the database was before, never after.
	snaps, _ := snapshot.List(backups)
	if len(snaps) == 0 {
		t.Error("nobody took a snapshot")
	}
	for _, s := range snaps {
		if got := storetest.Version(t, filepath.Join(backups, s.Name)); got != 1 {
			t.Errorf("%s is at schema %d, but it's labeled schema %d, the one before the migration", s.Name, got, s.Schema)
		}
	}
	if entries, _ := os.ReadDir(backups); len(entries) != len(snaps) {
		t.Errorf("%d files in the snapshot directory, %d of them snapshots", len(entries), len(snaps))
	}
}

// diffTables says which tables differ, for a failure message.
func diffTables(want, got storetest.Tables) []string {
	var out []string
	for name, w := range want {
		if g, ok := got[name]; !ok || !reflect.DeepEqual(w, g) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// diffLines lists the lines in only one of the two, for a failure message.
func diffLines(want, got []string) string {
	var out string
	for _, l := range want {
		if !slices.Contains(got, l) {
			out += "- " + l + "\n"
		}
	}
	for _, l := range got {
		if !slices.Contains(want, l) {
			out += "+ " + l + "\n"
		}
	}
	return out
}
