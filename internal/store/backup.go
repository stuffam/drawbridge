package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/stuffam/drawbridge/internal/snapshot"
)

// LatestSchema is the newest schema version this build knows: the number of its last migration.
func LatestSchema() int { return latestSchemaIn(migrationFiles) }

func latestSchemaIn(fsys fs.FS) int {
	names, _ := fs.Glob(fsys, "migrations/*.sql")
	latest := 0
	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		if v, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0]); err == nil && v > latest {
			latest = v
		}
	}
	return latest
}

// SchemaVersion is the schema version the database is at.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// Snapshot writes a consistent copy of the database to path, which mustn't exist (docs/PLAN.md
// §6.6). It's SQLite's VACUUM INTO, so it's safe while the daemon is writing, and the copy has
// no WAL file of its own. The file is made readable by its owner alone.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	// VACUUM INTO accepts an empty file, so the mode is set before anything is written to it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the caller's path, in a directory it made.
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("snapshotting the database: %w", err)
	}
	return nil
}

// errMovedOn means another process upgraded the database while this one was copying it, so the
// copy isn't the database as it was before the migration.
var errMovedOn = errors.New("the database was upgraded by another process while it was being snapshotted")

// snapshotBeforeMigrating saves the database as it is, at schema version from, in the snapshot
// directory, and drops the oldest such snapshots past the number to keep. It returns the path.
// A snapshot that turns out to hold a later schema than its name says, because another process
// migrated the database first, is removed and errMovedOn returned.
func (s *Store) snapshotBeforeMigrating(ctx context.Context, from int) (string, error) {
	if err := os.MkdirAll(s.snapshotDir, 0o700); err != nil {
		return "", err
	}
	var (
		path string
		err  error
	)
	for range 10 {
		path = snapshot.NewPath(s.snapshotDir, snapshot.PreMigration, from, s.now())
		// A name another process took between NewPath's look and our create is tried again.
		if err = s.Snapshot(ctx, path); !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		return "", err
	}
	held, err := snapshotSchema(path)
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if held != from {
		_ = os.Remove(path)
		if held > from {
			return "", errMovedOn
		}
		return "", fmt.Errorf("the snapshot holds schema %d, not %d", held, from)
	}
	keep := s.snapshotKeep
	if keep <= 0 {
		keep = snapshot.DefaultKeepPreMigration
	}
	// A snapshot that can't be pruned is a few megabytes too many, not a reason to stop.
	_, _ = snapshot.Prune(s.snapshotDir, snapshot.PreMigration, keep)
	return path, nil
}

// snapshotSchema reads the schema version a snapshot file holds.
func snapshotSchema(path string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("reading the snapshot: %w", err)
	}
	return v, nil
}

// CheckIntegrity runs SQLite's integrity check, and returns what it found if it isn't "ok".
func (s *Store) CheckIntegrity(ctx context.Context) error {
	// SQLite reports a badly damaged file as an error, and a mildly damaged one as rows.
	rows, err := s.db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("the database failed SQLite's integrity check: %w", err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("the database failed SQLite's integrity check: %w", err)
	}
	if len(problems) > 0 {
		if len(problems) > 3 {
			problems = append(problems[:3], "…")
		}
		return errors.New("the database failed SQLite's integrity check: " + strings.Join(problems, "; "))
	}
	return nil
}

// DeleteAllSessions ends every login, and returns how many there were.
func (s *Store) DeleteAllSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
