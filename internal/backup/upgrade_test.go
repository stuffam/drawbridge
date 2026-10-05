package backup

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/store/storetest"
)

// The restore half of the upgrade matrix: a backup, or a snapshot, that any earlier release made
// restores onto this one. The restore brings the database up to date, and everything it held
// reads back, except the logins, which a restore ends on purpose.
func TestRestoreFromEverySchema(t *testing.T) {
	for _, p := range storetest.Profiles {
		for v := 1; v <= store.LatestSchema(); v++ {
			for _, snapshot := range []bool{false, true} {
				kind := "backup"
				if snapshot {
					kind = "snapshot"
				}
				t.Run(fmt.Sprintf("%s host's %s from schema %d", p, kind, v), func(t *testing.T) {
					restoreFrom(t, v, p, snapshot)
				})
			}
		}
	}
}

func restoreFrom(t *testing.T, v int, p storetest.Profile, snapshot bool) {
	ctx := context.Background()
	latest := store.LatestSchema()

	// The host that takes the restore is a fresh install, with the daemon stopped.
	h := newHost(t)
	_ = h.st.Close()
	// A backup brings its own key. A snapshot is sealed with the host's, which stays.
	key := h.secret
	if !snapshot {
		key = randomBytes(t, keys.SecretSize)
	}
	sealer, err := keys.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}

	// The old release's database, copied out the way the daemon copies its own.
	old := filepath.Join(t.TempDir(), "old.db")
	storetest.Build(t, old, sealer, v, p)
	src, err := store.Open(ctx, old, sealer, store.WithSchemaLimit(v))
	if err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := src.Snapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	before := storetest.Rows(t, snap)

	file, pass := snap, ""
	if !snapshot {
		file, pass = writeBackupFile(t, snap, key, v), passphrase
	}
	res, err := h.restore(file, pass)
	if err != nil {
		t.Fatalf("restoring a schema %d database: %v", v, err)
	}
	if res.Snapshot != snapshot || res.Manifest.Schema != v || res.Migrated != (v < latest) {
		t.Errorf("the result is %+v, want a snapshot %v from schema %d, migrated %v", res, snapshot, v, v < latest)
	}
	wantEnded := int64(0)
	if p == storetest.Used && v >= 2 {
		wantEnded = 1
	}
	if res.SessionsEnded != wantEnded {
		t.Errorf("%d logins were ended, want %d", res.SessionsEnded, wantEnded)
	}

	st := openHost(t, h.db, key)
	if got, err := st.SchemaVersion(ctx); err != nil || got != latest {
		t.Fatalf("the restored database is at schema %d (%v), want %d", got, err, latest)
	}
	storetest.Verify(t, st, v, p, storetest.EndedLogins())
	storetest.Kept(t, before, h.db, storetest.EndedLogins())
	if err := st.CheckIntegrity(ctx); err != nil {
		t.Error(err)
	}
	if bad := storetest.ForeignKeyViolations(t, h.db); len(bad) > 0 {
		t.Errorf("the restore left rows that point at nothing: %v", bad)
	}
}
