package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stuffam/drawbridge/internal/backup"
	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/store"
)

const backupPassphrase = "a long enough passphrase"

// withKeyFile gives the service the secret key's file, which is what lets it make backups. The
// bytes are the ones newTestServiceDB's store was sealed with.
func withKeyFile(t *testing.T, s *Service) []byte {
	t.Helper()
	key := bytes.Repeat([]byte{7}, keys.SecretSize)
	s.SecretKeyPath = filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(s.SecretKeyPath, key, 0o640); err != nil {
		t.Fatal(err)
	}
	return key
}

// The file the daemon hands out opens with the passphrase, and holds the database as it is, the
// key it's sealed with, and the schema it's at: everything a restore needs, and no more.
func TestCreateBackupHoldsTheDatabaseAndTheKey(t *testing.T) {
	ctx := web(context.Background(), "admin")
	svc, clk := newTestService(t)
	key := withKeyFile(t, svc)
	phone, _, err := svc.AddClient(ctx, "a phone")
	if err != nil {
		t.Fatal(err)
	}

	b, err := svc.CreateBackup(ctx, backupPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	file := b.ReadCloser.(*removeOnClose).Name()
	data, err := io.ReadAll(b)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != b.Size {
		t.Errorf("the file is %d bytes and says %d", len(data), b.Size)
	}
	if want := "drawbridge-20260926-120000.backup"; b.Name != want {
		t.Errorf("name %q, want %q", b.Name, want)
	}
	if !bytes.HasPrefix(data, []byte("drawbridge-backup\n")) || bytes.Contains(data, []byte("a phone")) || bytes.Contains(data, key) {
		t.Error("the file isn't encrypted")
	}

	var db bytes.Buffer
	m, gotKey, err := backup.Read(bytes.NewReader(data), backupPassphrase, &db)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotKey, key) || m.Schema != store.LatestSchema() || !m.CreatedAt.Equal(clk.now()) {
		t.Errorf("manifest %+v, key matches %v", m, bytes.Equal(gotKey, key))
	}
	dbFile := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(dbFile, db.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	sealer, _ := keys.NewSealer(gotKey)
	st, err := store.Open(context.Background(), dbFile, sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.Client(context.Background(), store.ByName("a phone"))
	if err != nil || got.ID != phone.ID || *got.PrivateKey != *phone.PrivateKey {
		t.Fatalf("the backup's client: %+v, %v", got, err)
	}

	// Closing it cleans up: neither the backup nor the snapshot it was made from stays in /tmp.
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(file)); err == nil {
		t.Errorf("%s is still there after Close", filepath.Dir(file))
	}
}

// Making a backup is an event, because the file holds every secret. The event says that it
// happened, and who did it, and carries nothing of what's in it.
func TestCreateBackupIsAnEvent(t *testing.T) {
	ctx := web(context.Background(), "admin")
	svc, _ := newTestService(t)
	withKeyFile(t, svc)
	b, err := svc.CreateBackup(ctx, backupPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	events, _ := svc.Events(ctx, store.EventFilter{Kind: "backup.created"})
	if len(events) != 1 || events[0].Actor != "admin" || events[0].Via != "web" || events[0].Data["size"] == "" {
		t.Fatalf("events %+v", events)
	}
	for k, v := range events[0].Data {
		if strings.Contains(k+v, backupPassphrase) {
			t.Errorf("the event carries the passphrase: %s=%s", k, v)
		}
	}
}

func TestCreateBackupRefusals(t *testing.T) {
	ctx := web(context.Background(), "admin")
	svc, _ := newTestService(t)

	if _, err := svc.CreateBackup(ctx, backupPassphrase); !errors.Is(err, ErrNoBackup) {
		t.Errorf("with no key file configured: err %v, want ErrNoBackup", err)
	}

	withKeyFile(t, svc)
	before := kinds(t, svc)
	for _, weak := range []string{"", "short", "elevenchars"} {
		_, err := svc.CreateBackup(ctx, weak)
		if !model.IsInvalid(err) || !errors.Is(err, backup.ErrWeakPassphrase) {
			t.Errorf("%q: err %v, want an invalid-request error that names the minimum", weak, err)
		}
	}
	// A key file that can't be read is an error, and says which one.
	svc.SecretKeyPath = filepath.Join(t.TempDir(), "gone")
	if _, err := svc.CreateBackup(ctx, backupPassphrase); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("a missing key file: err %v", err)
	}
	if got := kinds(t, svc); len(got) != len(before) {
		t.Errorf("events went from %v to %v: a refused backup was recorded", before, got)
	}
}

// The web's download takes the account's password again, because a session alone (a hijacked one)
// mustn't be able to carry off every secret the server has. A wrong one makes no file, is an event
// of its own, and counts against the login limits, the same ones a token's creation counts against.
func TestCreateBackupForNeedsThePassword(t *testing.T) {
	s, _, ctx, login, password := tokenEnv(t)
	withKeyFile(t, s)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	for _, wrong := range []string{"not the password", ""} {
		b, err := s.CreateBackupFor(ctx, login.User, wrong, backupPassphrase)
		if !errors.Is(err, ErrWrongPassword) || !model.IsInvalid(err) || b != nil {
			t.Fatalf("password %q: err = %v, backup %v", wrong, err, b)
		}
	}
	got := kinds(t, s)
	if got[len(got)-1] != "auth.backup_failed" || slices.Contains(got, "backup.created") {
		t.Errorf("events %v: want the failure, and no backup", got)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("a refused backup left %d files in the temp directory", len(left))
	}

	b, err := s.CreateBackupFor(ctx, login.User, password, backupPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	var db bytes.Buffer
	data, _ := io.ReadAll(b)
	if _, _, err := backup.Read(bytes.NewReader(data), backupPassphrase, &db); err != nil || db.Len() == 0 {
		t.Errorf("the backup doesn't open with its passphrase: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(ctx, store.EventFilter{Kind: "backup.created"})
	if len(events) != 1 || events[0].Actor != "admin" || events[0].Via != "web" {
		t.Errorf("events %+v", events)
	}
}

// The wrong password counts against the same limits as a login: five failures are free, and after
// the sixth the right password has to wait too.
func TestCreateBackupForIsRateLimited(t *testing.T) {
	s, _, ctx, login, password := tokenEnv(t)
	withKeyFile(t, s)
	for range 6 {
		_, _ = s.CreateBackupFor(ctx, login.User, "wrong", backupPassphrase)
	}
	_, err := s.CreateBackupFor(ctx, login.User, password, backupPassphrase)
	var limited *RateLimitedError
	if !errors.As(err, &limited) || limited.Wait <= 0 {
		t.Fatalf("after 6 wrong passwords: err = %v, want a RateLimitedError, even for the right one", err)
	}
	// It's one limit for the account, whatever it's asked for: a token can't be made either.
	if _, _, err := s.CreateAPIToken(ctx, login.User, password, "Homepage"); !errors.As(err, &limited) {
		t.Errorf("making a token while rate limited: err = %v", err)
	}
}

// A weak passphrase is refused before the password is looked at, so it costs the account none of
// its attempts, and a daemon that can't make backups says so without touching either.
func TestCreateBackupForRefusals(t *testing.T) {
	s, _, ctx, login, password := tokenEnv(t)
	if _, err := s.CreateBackupFor(ctx, login.User, password, backupPassphrase); !errors.Is(err, ErrNoBackup) {
		t.Errorf("with no key file configured: err %v, want ErrNoBackup", err)
	}
	withKeyFile(t, s)
	for range 10 {
		_, err := s.CreateBackupFor(ctx, login.User, "wrong", "short")
		if !errors.Is(err, backup.ErrWeakPassphrase) || !model.IsInvalid(err) {
			t.Fatalf("a short passphrase: err %v", err)
		}
	}
	if b, err := s.CreateBackupFor(ctx, login.User, password, backupPassphrase); err != nil {
		t.Errorf("the account was limited by refused passphrases: %v", err)
	} else {
		_ = b.Close()
	}
	if got := kinds(t, s); slices.Contains(got, "auth.backup_failed") {
		t.Errorf("events %v: a weak passphrase was counted as a wrong password", got)
	}
}
