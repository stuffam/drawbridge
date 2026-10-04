package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/stuffam/drawbridge/internal/backup"
	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/version"
)

// ErrNoBackup means the daemon doesn't know where its secret key is, so it can't make a backup.
var ErrNoBackup = errors.New("this daemon can't make backups")

// BackupFile is a backup that's been made and is waiting to be sent. Closing it deletes it.
type BackupFile struct {
	io.ReadCloser
	// Name is a file name for it, with the time it was made.
	Name string
	Size int64
}

// CreateBackup makes an encrypted backup of the database and the secret key (docs/PLAN.md
// §6.6). The whole file is made, and checked to be written, before any of it is returned, so a
// failure is an error and not a download that stops partway. The passphrase is the only thing
// that protects it, so it must be at least backup.MinPassphrase characters.
//
// It's an event, because the file holds every secret the server has.
func (s *Service) CreateBackup(ctx context.Context, passphrase string) (*BackupFile, error) {
	if s.SecretKeyPath == "" {
		return nil, ErrNoBackup
	}
	// Checked here, before the snapshot, so a short passphrase costs nothing.
	if _, err := backup.Check(passphrase); err != nil {
		return nil, &model.InvalidError{Err: err}
	}
	key, err := os.ReadFile(s.SecretKeyPath)
	if err != nil {
		// The path is the admin's own, and the error names it, but it never carries the key.
		return nil, fmt.Errorf("reading the secret key for the backup: %w", err)
	}
	schema, err := s.Store.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}

	// A private directory, so the snapshot (which holds every secret, still encrypted with
	// the key above) and the backup never sit in a place others can read.
	dir, err := os.MkdirTemp("", "drawbridge-backup-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	snap := filepath.Join(dir, "drawbridge.db")
	if err := s.Store.Snapshot(ctx, snap); err != nil {
		return nil, err
	}
	now := s.now()
	out, err := os.OpenFile(filepath.Join(dir, "backup"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: a name inside the private directory just made.
	if err != nil {
		return nil, err
	}
	m := backup.Manifest{CreatedAt: now.UTC(), Version: version.Version, Schema: schema}
	if err := backup.Write(out, passphrase, m, key, snap); err != nil {
		_ = out.Close()
		return nil, err
	}
	// The snapshot has done its job, and doesn't need to wait for the download.
	_ = os.Remove(snap)
	info, err := out.Stat()
	if err != nil {
		_ = out.Close()
		return nil, err
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		_ = out.Close()
		return nil, err
	}

	s.record(ctx, Event{Kind: "backup.created", Data: map[string]string{"size": strconv.FormatInt(info.Size(), 10)}})
	keep = true
	return &BackupFile{
		ReadCloser: &removeOnClose{File: out, dir: dir},
		Name:       "drawbridge-" + now.UTC().Format("20060102-150405") + ".backup",
		Size:       info.Size(),
	}, nil
}

// CreateBackupFor is CreateBackup for a logged-in account, the web UI's download. It takes the
// account's password again, because the file holds every secret the server has, and a session
// alone (a hijacked one, a browser left open) mustn't be able to carry them off. A wrong password
// counts against the same limits as a login. The passphrase is checked first, so a short one
// costs the account nothing.
func (s *Service) CreateBackupFor(ctx context.Context, u store.User, password, passphrase string) (*BackupFile, error) {
	if s.SecretKeyPath == "" {
		return nil, ErrNoBackup
	}
	if _, err := backup.Check(passphrase); err != nil {
		return nil, &model.InvalidError{Err: err}
	}
	if err := s.confirmPassword(ctx, u, password, "auth.backup_failed"); err != nil {
		return nil, err
	}
	return s.CreateBackup(ctx, passphrase)
}

// removeOnClose deletes the directory holding a file when the file's closed.
type removeOnClose struct {
	*os.File
	dir string
}

func (r *removeOnClose) Close() error {
	err := r.File.Close()
	if rerr := os.RemoveAll(r.dir); err == nil {
		err = rerr
	}
	return err
}
