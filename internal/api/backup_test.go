package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/backup"
	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/service"
	"github.com/stuffam/drawbridge/internal/snapshot"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/views"
)

const apiPassphrase = "a long enough passphrase"

// withBackups gives the service what it needs to make backups: the file the key is in, which is
// the key newService's store was sealed with.
func withBackups(t *testing.T, svc *service.Service) []byte {
	t.Helper()
	key := bytes.Repeat([]byte{9}, keys.SecretSize)
	svc.SecretKeyPath = filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(svc.SecretKeyPath, key, 0o640); err != nil {
		t.Fatal(err)
	}
	return key
}

// The download is the whole backup, opened by the passphrase: the key, and a database that key
// opens. The headers make a browser save it, and not keep it.
func TestBackupDownload(t *testing.T) {
	svc := newService(t)
	key := withBackups(t, svc)
	srv := newServer(t, svc)
	b, password := loggedIn(t, svc, srv)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	r := b.expect(http.StatusOK, "POST", "/api/system/backup",
		views.BackupDownloadRequest{Password: password, Passphrase: apiPassphrase})
	if got := r.header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type %q", got)
	}
	if got := r.header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q: a backup must not be kept by the browser or a proxy", got)
	}
	if got := r.header.Get("Content-Disposition"); !regexp.MustCompile(`^attachment; filename="drawbridge-\d{8}-\d{6}\.backup"$`).MatchString(got) {
		t.Errorf("Content-Disposition %q", got)
	}
	if got, _ := strconv.Atoi(r.header.Get("Content-Length")); got != len(r.body) || got == 0 {
		t.Errorf("Content-Length %q, body %d bytes", r.header.Get("Content-Length"), len(r.body))
	}
	if !bytes.HasPrefix(r.body, []byte("drawbridge-backup\n")) || bytes.Contains(r.body, key) {
		t.Error("the download isn't an encrypted backup")
	}

	var db bytes.Buffer
	m, gotKey, err := backup.Read(bytes.NewReader(r.body), apiPassphrase, &db)
	if err != nil {
		t.Fatalf("the download doesn't open with its passphrase: %v", err)
	}
	if !bytes.Equal(gotKey, key) || m.Schema != store.LatestSchema() {
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
	if _, err := st.Settings(context.Background()); err != nil {
		t.Errorf("the key in the backup doesn't open its database: %v", err)
	}

	// The event says who, and carries neither secret. Nothing of the file stays on the host.
	var events []views.EventView
	b.expect(http.StatusOK, "GET", "/api/events?kind=backup.created", nil).decode(t, &events)
	if len(events) != 1 || events[0].Actor != "admin" || events[0].Via != "web" {
		t.Fatalf("events %+v", events)
	}
	all := b.expect(http.StatusOK, "GET", "/api/events?limit=200", nil)
	if strings.Contains(string(all.body), apiPassphrase) || strings.Contains(string(all.body), password) {
		t.Error("the log carries the password or the passphrase")
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("the download left %d files in the temp directory", len(left))
	}
}

// A session isn't enough: the password has to come with it, and the passphrase has to be one worth
// the file's contents. Every refusal is an error response, never a file.
func TestBackupDownloadRefusals(t *testing.T) {
	svc := newService(t)
	withBackups(t, svc)
	srv := newServer(t, svc)
	b, password := loggedIn(t, svc, srv)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	good := views.BackupDownloadRequest{Password: password, Passphrase: apiPassphrase}

	newBrowser(t, srv).expect(http.StatusUnauthorized, "POST", "/api/system/backup", good)
	b.expect(http.StatusForbidden, "POST", "/api/system/backup", good, csrfHeader, "")

	for name, c := range map[string]struct {
		req  views.BackupDownloadRequest
		want string
	}{
		"a wrong password":   {views.BackupDownloadRequest{Password: "not it", Passphrase: apiPassphrase}, "password is wrong"},
		"no password":        {views.BackupDownloadRequest{Passphrase: apiPassphrase}, "password is wrong"},
		"a short passphrase": {views.BackupDownloadRequest{Password: password, Passphrase: "too short"}, "at least 12"},
		"no passphrase":      {views.BackupDownloadRequest{Password: password}, "at least 12"},
	} {
		r := b.expect(http.StatusBadRequest, "POST", "/api/system/backup", c.req)
		if !strings.Contains(r.errorText(), c.want) {
			t.Errorf("%s: error %q, want it to say %q", name, r.errorText(), c.want)
		}
		if r.header.Get("Content-Disposition") != "" {
			t.Errorf("%s: the response is an attachment", name)
		}
	}
	// Unknown fields and a body that isn't JSON are the usual 400.
	b.expect(http.StatusBadRequest, "POST", "/api/system/backup", map[string]string{"password": password, "extra": "x"})

	// Wrong passwords are limited like logins: the sixth failure locks even the right one out.
	for range 6 {
		b.do("POST", "/api/system/backup", views.BackupDownloadRequest{Password: "wrong", Passphrase: apiPassphrase})
	}
	r := b.expect(http.StatusTooManyRequests, "POST", "/api/system/backup", good)
	if secs, _ := strconv.Atoi(r.header.Get("Retry-After")); secs < 1 {
		t.Errorf("Retry-After %q", r.header.Get("Retry-After"))
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("a refusal left %d files in the temp directory", len(left))
	}
	var events []views.EventView
	b.expect(http.StatusOK, "GET", "/api/events?kind=backup.created", nil).decode(t, &events)
	if len(events) != 0 {
		t.Errorf("a refused download is recorded as a backup: %+v", events)
	}
}

// A daemon that doesn't know where its key is can't make a backup, and says so plainly instead
// of failing in the journal.
func TestBackupDownloadWithoutAKey(t *testing.T) {
	svc := newService(t)
	srv := newServer(t, svc)
	b, password := loggedIn(t, svc, srv)
	r := b.expect(http.StatusNotImplemented, "POST", "/api/system/backup",
		views.BackupDownloadRequest{Password: password, Passphrase: apiPassphrase})
	if !strings.Contains(r.errorText(), "backups") {
		t.Errorf("error %q doesn't say what's missing", r.errorText())
	}
}

// A download that's stopped, by the daemon shutting down or by a reader that has gone, stops
// reading the file. The length was announced, so the browser sees the file as cut short, and
// doesn't save a partial one.
func TestSendFileStops(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 200<<10)

	t.Run("on shutdown", func(t *testing.T) {
		shutdown := make(chan struct{})
		w := &afterFirstWrite{ResponseRecorder: httptest.NewRecorder(), then: func() { close(shutdown) }}
		err := sendFile(w, bytes.NewReader(data), shutdown)
		if !errors.Is(err, errShuttingDown) {
			t.Errorf("err %v, want errShuttingDown", err)
		}
		if n := w.Body.Len(); n == 0 || n >= len(data) {
			t.Errorf("sent %d of %d bytes: it should send what was in flight and then stop", n, len(data))
		}
	})
	t.Run("when the reader has gone", func(t *testing.T) {
		gone := errors.New("broken pipe")
		w := &afterFirstWrite{ResponseRecorder: httptest.NewRecorder(), fail: gone}
		r := &countingReader{Reader: bytes.NewReader(data)}
		if err := sendFile(w, r, nil); !errors.Is(err, gone) {
			t.Errorf("err %v, want the write's", err)
		}
		if r.n >= len(data) {
			t.Errorf("read all %d bytes after the write failed", r.n)
		}
	})
	t.Run("a whole file", func(t *testing.T) {
		w := httptest.NewRecorder()
		if err := sendFile(w, bytes.NewReader(data), nil); err != nil || !bytes.Equal(w.Body.Bytes(), data) {
			t.Errorf("err %v, sent %d of %d bytes", err, w.Body.Len(), len(data))
		}
	})
}

type afterFirstWrite struct {
	*httptest.ResponseRecorder
	then func()
	fail error
	n    int
}

func (w *afterFirstWrite) Write(p []byte) (int, error) {
	if w.n++; w.n > 1 && w.fail != nil {
		return 0, w.fail
	}
	k, err := w.ResponseRecorder.Write(p)
	if w.n == 1 && w.then != nil {
		w.then()
	}
	return k, err
}

type countingReader struct {
	io.Reader
	n int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += n
	return n, err
}

// The snapshots the page lists are the host's, newest first. They're a list and nothing more: no
// route hands one out, because a snapshot has no passphrase.
func TestSnapshotList(t *testing.T) {
	svc := newService(t)
	srv := newServer(t, svc)
	b, _ := loggedIn(t, svc, srv)
	newBrowser(t, srv).expect(http.StatusUnauthorized, "GET", "/api/system/snapshots", nil)

	// A daemon with no snapshot directory has none, and the list is empty, not null.
	r := b.expect(http.StatusOK, "GET", "/api/system/snapshots", nil)
	if !strings.Contains(string(r.body), `"snapshots":[]`) {
		t.Errorf("body %s: want an empty list", r.body)
	}
	var l views.SnapshotListView
	r.decode(t, &l)
	if l.Dir != "" || l.Nightly {
		t.Errorf("no directory: %+v", l)
	}

	svc.SnapshotDir = filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(svc.SnapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	for _, p := range []string{
		snapshot.NewPath(svc.SnapshotDir, snapshot.Nightly, 0, old),
		snapshot.NewPath(svc.SnapshotDir, snapshot.PreMigration, 4, old.Add(24*time.Hour)),
		filepath.Join(svc.SnapshotDir, "mine.db"),
	} {
		if err := os.WriteFile(p, []byte("1234"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l = views.SnapshotListView{}
	b.expect(http.StatusOK, "GET", "/api/system/snapshots", nil).decode(t, &l)
	if l.Dir != svc.SnapshotDir || !l.Nightly || len(l.Snapshots) != 2 {
		t.Fatalf("list %+v", l)
	}
	if s := l.Snapshots[0]; s.Name != "pre-migration-v4-20261002-030000.db" || s.Kind != "pre-migration" || s.Schema != 4 || s.Size != 4 || !s.MadeAt.Equal(old.Add(24*time.Hour)) {
		t.Errorf("newest %+v", s)
	}
	if s := l.Snapshots[1]; s.Name != "nightly-20261001-030000.db" || s.Kind != "nightly" || s.Schema != 0 {
		t.Errorf("oldest %+v", s)
	}
	if strings.Contains(string(b.expect(http.StatusOK, "GET", "/api/system/snapshots", nil).body), `"schema":0`) {
		t.Error("a nightly snapshot has no schema to report")
	}
}
