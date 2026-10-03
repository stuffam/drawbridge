package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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
