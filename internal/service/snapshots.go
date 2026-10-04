package service

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/stuffam/drawbridge/internal/snapshot"
)

// How often the daemon looks at whether a nightly snapshot is due, and how long it waits to try
// again after one fails. Looking is a directory listing, so it's cheap, and it keeps a snapshot
// from drifting more than this far past its day.
const (
	snapshotCheckEvery = 10 * time.Minute
	snapshotRetryAfter = time.Hour
	// snapshotFirstCheck is how long after the daemon starts it looks the first time, so a
	// start (which has the tunnel and the reconcile to do) isn't also a snapshot.
	snapshotFirstCheck = time.Minute
)

func (s *Service) snapshotInterval() time.Duration {
	if s.SnapshotInterval == 0 {
		return snapshot.DefaultInterval
	}
	return s.SnapshotInterval
}

func (s *Service) snapshotKeep() int {
	if s.SnapshotKeep <= 0 {
		return snapshot.DefaultKeep
	}
	return s.SnapshotKeep
}

// SnapshotsOn is whether the nightly job runs: it needs a directory, and an interval that isn't
// negative.
func (s *Service) SnapshotsOn() bool { return s.SnapshotDir != "" && s.SnapshotInterval >= 0 }

// SnapshotDatabase saves a snapshot of the database in the snapshot directory, as a nightly one,
// and drops the oldest nightly snapshots past the number to keep (docs/PLAN.md §6.6). It's
// SQLite's VACUUM INTO, so it's safe while the daemon writes, and it writes a new file: nothing
// in the live database. It isn't an event, because it changes nothing the admin manages.
func (s *Service) SnapshotDatabase(ctx context.Context) (snapshot.Info, error) {
	if err := os.MkdirAll(s.SnapshotDir, 0o700); err != nil {
		return snapshot.Info{}, err
	}
	now := s.now()
	path := snapshot.NewPath(s.SnapshotDir, snapshot.Nightly, 0, now)
	if err := s.Store.Snapshot(ctx, path); err != nil {
		return snapshot.Info{}, err
	}
	// A snapshot that can't be pruned is a few megabytes too many, not a failure.
	if removed, err := snapshot.Prune(s.SnapshotDir, snapshot.Nightly, s.snapshotKeep()); err != nil {
		s.Log.Warn("can't delete an old snapshot", "err", err)
	} else if len(removed) > 0 {
		s.Log.Info("deleted old snapshots", "files", removed)
	}
	info := snapshot.Info{Name: filepath.Base(path), Kind: snapshot.Nightly, MadeAt: now.UTC().Truncate(time.Second)}
	if fi, err := os.Stat(path); err == nil {
		info.Size = fi.Size()
	}
	return info, nil
}

// SnapshotDue is whether the newest nightly snapshot is older than the interval, or there is
// none.
func (s *Service) SnapshotDue() (bool, error) {
	if !s.SnapshotsOn() {
		return false, nil
	}
	latest, ok, err := snapshot.Latest(s.SnapshotDir, snapshot.Nightly)
	if err != nil {
		return false, err
	}
	return !ok || s.now().Sub(latest.MadeAt) >= s.snapshotInterval(), nil
}

// SnapshotIfDue makes the nightly snapshot when one is due, and reports whether it did.
func (s *Service) SnapshotIfDue(ctx context.Context) (bool, error) {
	due, err := s.SnapshotDue()
	if err != nil || !due {
		return false, err
	}
	info, err := s.SnapshotDatabase(ctx)
	if err != nil {
		return false, err
	}
	s.Log.Info("snapshotted the database", "file", info.Name, "bytes", info.Size)
	return true, nil
}

// RunSnapshots takes the nightly snapshot until ctx ends: a minute after it starts, and then
// whenever the newest is a day old. Judging by the newest file, not by a timer, means a restart
// doesn't make an extra one, and a host that was off at night makes it when it's back. A failure
// is logged and tried again in an hour.
func (s *Service) RunSnapshots(ctx context.Context) {
	if !s.SnapshotsOn() {
		return
	}
	wait := snapshotFirstCheck
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		wait = snapshotCheckEvery
		if iv := s.snapshotInterval() / 2; iv < wait {
			wait = iv
		}
		if _, err := s.SnapshotIfDue(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("can't snapshot the database", "err", err, "trying again in", snapshotRetryAfter.String())
			wait = snapshotRetryAfter
		}
	}
}
