// Package snapshot names, lists, and rotates the database snapshots Drawbridge keeps on the host
// itself (docs/PLAN.md §6.6): one a night, and one before a migration. They hold the database
// only, sealed with the key that's already on the host, so they protect against a bad change or a
// bad migration, not against a lost SD card; that's what a backup is for (internal/backup).
//
// The package only handles files. Making one is the store's `Snapshot`, so this has no
// dependency on it, and the store can use it.
package snapshot

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// The two kinds of snapshot.
const (
	// Nightly is the daemon's daily snapshot.
	Nightly = "nightly"
	// PreMigration is the one made before the schema is upgraded; its name has the schema
	// version it holds, which is the one the upgrade started from.
	PreMigration = "pre-migration"
)

// Defaults for the nightly job.
const (
	DefaultInterval = 24 * time.Hour
	DefaultKeep     = 7
	// DefaultKeepPreMigration is how many pre-migration snapshots stay. They're rare, and the
	// one that matters is the newest, so a few is plenty.
	DefaultKeepPreMigration = 3
)

// DirName is the directory's name inside the state directory.
const DirName = "backups"

// Dir is where the snapshots of the database at dbPath are kept: beside it, in the state
// directory both units own.
func Dir(dbPath string) string { return filepath.Join(filepath.Dir(dbPath), DirName) }

// Info describes one snapshot file.
type Info struct {
	// Name is the file's name, without its directory.
	Name string
	Kind string
	// MadeAt is when it was made, as the name says.
	MadeAt time.Time
	// Schema is the schema version a pre-migration snapshot holds; zero for a nightly one.
	Schema int
	Size   int64
}

const timeLayout = "20060102-150405"

// The names are strict, so that listing and pruning touch only what this package made, and
// never a file an admin put in the directory.
var namePattern = regexp.MustCompile(`^(nightly|pre-migration)(?:-v([0-9]+))?-([0-9]{8}-[0-9]{6})(?:-([0-9]+))?\.db$`)

// Name is a snapshot's file name. schema is for PreMigration, and ignored for Nightly.
func Name(kind string, schema int, t time.Time) string {
	if kind == PreMigration {
		return fmt.Sprintf("%s-v%d-%s.db", kind, schema, t.UTC().Format(timeLayout))
	}
	return fmt.Sprintf("%s-%s.db", kind, t.UTC().Format(timeLayout))
}

// NewPath returns a path in dir for a snapshot made at t that doesn't exist yet. Two made in the
// same second (the tunnel unit and the daemon, upgrading together) get different names.
func NewPath(dir, kind string, schema int, t time.Time) string {
	name := Name(kind, schema, t)
	path := filepath.Join(dir, name)
	for n := 2; ; n++ {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return path
		}
		path = filepath.Join(dir, name[:len(name)-len(".db")]+"-"+strconv.Itoa(n)+".db")
	}
}

// parse reads a snapshot's name.
func parse(name string) (Info, bool) {
	m := namePattern.FindStringSubmatch(name)
	if m == nil {
		return Info{}, false
	}
	t, err := time.ParseInLocation(timeLayout, m[3], time.UTC)
	if err != nil {
		return Info{}, false
	}
	info := Info{Name: name, Kind: m[1], MadeAt: t}
	if m[1] == PreMigration {
		if m[2] == "" {
			return Info{}, false
		}
		info.Schema, _ = strconv.Atoi(m[2])
	} else if m[2] != "" {
		return Info{}, false
	}
	return info, true
}

// List returns the snapshots in dir, newest first. A directory that doesn't exist has none.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range entries {
		info, ok := parse(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		info.Size = fi.Size()
		out = append(out, info)
	}
	// Newest first; the name breaks a tie, so two made in one second keep their order.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].MadeAt.Equal(out[j].MadeAt) {
			return out[i].MadeAt.After(out[j].MadeAt)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// Latest returns the newest snapshot of a kind, and whether there is one.
func Latest(dir, kind string) (Info, bool, error) {
	all, err := List(dir)
	if err != nil {
		return Info{}, false, err
	}
	for _, s := range all {
		if s.Kind == kind {
			return s, true, nil
		}
	}
	return Info{}, false, nil
}

// Prune deletes the oldest snapshots of a kind until keep are left, and returns the names it
// deleted. It never touches another kind, or a file whose name isn't one of its own. A keep of
// zero or less keeps everything: pruning is for a limit, and no limit isn't "none".
func Prune(dir, kind string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	all, err := List(dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	n := 0
	for _, s := range all { // newest first
		if s.Kind != kind {
			continue
		}
		if n++; n <= keep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, s.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, err
		}
		removed = append(removed, s.Name)
	}
	return removed, nil
}
