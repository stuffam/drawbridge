package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stuffam/drawbridge/internal/auth"
	"github.com/stuffam/drawbridge/internal/control"
	"github.com/stuffam/drawbridge/internal/diag"
	"github.com/stuffam/drawbridge/internal/firewall"
	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/lan"
	"github.com/stuffam/drawbridge/internal/reconcile"
	"github.com/stuffam/drawbridge/internal/service"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/tlscert"
	"github.com/stuffam/drawbridge/internal/tlscert/tlscerttest"
	"github.com/stuffam/drawbridge/internal/version"
	"github.com/stuffam/drawbridge/internal/views"
	"github.com/stuffam/drawbridge/internal/wg"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type result struct {
	code           int
	stdout, stderr string
}

func runCLI(stdin string, args ...string) result {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return result{code, stdout.String(), stderr.String()}
}

func TestRunVersion(t *testing.T) {
	r := runCLI("", "version")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if want := "drawbridge " + version.String() + "\n"; r.stdout != want {
		t.Fatalf("stdout %q, want %q", r.stdout, want)
	}
	if !strings.HasPrefix(version.String(), "v") {
		t.Fatalf("version %q, want a lowercase v prefix", version.String())
	}
}

func TestRunHelpAndErrors(t *testing.T) {
	cases := []struct {
		args       []string
		code       int
		wantStdout string
		wantStderr string
	}{
		{args: nil, code: 2, wantStderr: "Usage: drawbridge"},
		{args: []string{"help"}, code: 0, wantStdout: "client qr NAME"},
		{args: []string{"frobnicate"}, code: 2, wantStderr: `unknown command "frobnicate"`},
		{args: []string{"serve", "extra"}, code: 2, wantStderr: `unexpected argument "extra"`},
		{args: []string{"serve", "-bogus"}, code: 2, wantStderr: "flag provided but not defined"},
		{args: []string{"serve", "-listen", "not-an-address"}, code: 1, wantStderr: "can't listen"},
		{args: []string{"tunnel"}, code: 2, wantStderr: "Usage: drawbridge tunnel"},
		{args: []string{"tunnel", "sideways"}, code: 2, wantStderr: "Usage: drawbridge tunnel"},
		{args: []string{"server"}, code: 2, wantStderr: "Usage: drawbridge server"},
		{args: []string{"client"}, code: 2, wantStderr: "Usage: drawbridge client"},
		{args: []string{"client", "fly"}, code: 2, wantStderr: `unknown command "fly"`},
		{args: []string{"client", "add"}, code: 2, wantStderr: "needs exactly one NAME"},
		{args: []string{"client", "add", "a", "b"}, code: 2, wantStderr: "needs exactly one NAME"},
		{args: []string{"client", "list", "extra"}, code: 2, wantStderr: `unexpected argument "extra"`},
		{args: []string{"client", "rename", "a"}, code: 2, wantStderr: "needs NAME and NEW-NAME"},
		{args: []string{"admin"}, code: 2, wantStderr: "Usage: drawbridge admin"},
		{args: []string{"admin", "create"}, code: 2, wantStderr: "Usage: drawbridge admin"},
		{args: []string{"admin", "reset-password", "a", "b"}, code: 2, wantStderr: "Usage: drawbridge admin"},
		{args: []string{"admin", "promote"}, code: 2, wantStderr: `unknown command "promote"`},
		{args: []string{"events", "extra"}, code: 2, wantStderr: `unexpected argument "extra"`},
		{args: []string{"serve", "--backend", "userspace"}, code: 2, wantStderr: "--backend must be kernel or fake"},
	}
	for _, c := range cases {
		r := runCLI("", c.args...)
		if r.code != c.code {
			t.Errorf("%v: exit %d, want %d (stderr %q)", c.args, r.code, c.code, r.stderr)
		}
		if !strings.Contains(r.stdout, c.wantStdout) {
			t.Errorf("%v: stdout %q, want it to contain %q", c.args, r.stdout, c.wantStdout)
		}
		if !strings.Contains(r.stderr, c.wantStderr) {
			t.Errorf("%v: stderr %q, want it to contain %q", c.args, r.stderr, c.wantStderr)
		}
	}
}

// memFirewall remembers the applied revision, like the kernel's table does.
type memFirewall struct{ rev string }

func (f *memFirewall) Apply(_ context.Context, rs firewall.Ruleset) error {
	f.rev = rs.Revision
	return nil
}
func (f *memFirewall) Remove(context.Context) error                   { f.rev = ""; return nil }
func (f *memFirewall) Revision(context.Context) (string, bool, error) { return f.rev, f.rev != "", nil }

type fakeEnv struct {
	store  *store.Store
	wg     *wg.Fake
	rec    *reconcile.Reconciler
	svc    *service.Service
	socket string
}

// newFakeEnv builds the service over a fake tunnel that's already up.
func newFakeEnv(t *testing.T) *fakeEnv {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	sealer, err := keys.NewSealer(bytes.Repeat([]byte{5}, keys.SecretSize))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(dir, "db"), sealer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	backend := wg.NewFake()
	rec := &reconcile.Reconciler{State: st, WG: backend, Firewall: &memFirewall{},
		Lock: reconcile.FileLock{Path: filepath.Join(dir, "reconcile.lock")}}
	if _, err := rec.Up(ctx); err != nil {
		t.Fatal(err)
	}
	svc := &service.Service{Store: st, Rec: rec, WG: backend, Log: discard,
		Hasher: auth.NewHasher(auth.Params{Memory: 64, Time: 1, Threads: 1}), Limiter: auth.NewLimiter()}
	return &fakeEnv{store: st, wg: backend, rec: rec, svc: svc, socket: filepath.Join(dir, "control.sock")}
}

// startDaemon runs the daemon with the fake service and returns once it's ready.
func startDaemon(t *testing.T, env *fakeEnv) string {
	t.Helper()
	web, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctl, err := control.Listen(env.socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- daemon{web: web, control: ctl, svc: env.svc, drift: time.Hour, log: discard}.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})
	waitFor(t, func() bool {
		resp, err := http.Get("http://" + web.Addr().String() + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	return web.Addr().String()
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDaemonReportsReadinessAndShutsDown(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "notify.sock")
	notify, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sockPath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer notify.Close()
	t.Setenv("NOTIFY_SOCKET", sockPath)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon{web: ln, log: discard}.run(ctx) }()

	if got := readNotification(t, notify); got != "READY=1" {
		t.Fatalf("first notification %q, want READY=1", got)
	}
	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz: status %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't stop after cancel")
	}
	if got := readNotification(t, notify); got != "STOPPING=1" {
		t.Fatalf("second notification %q, want STOPPING=1", got)
	}
}

func readNotification(t *testing.T, conn *net.UnixConn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("reading notification: %v", err)
	}
	return string(buf[:n])
}

func TestDaemonReconcilesAtStartup(t *testing.T) {
	env := newFakeEnv(t)
	// Drift before the daemon starts: a peer the database doesn't know about.
	if _, err := env.store.AddClient(context.Background(), "phone"); err != nil {
		t.Fatal(err)
	}
	startDaemon(t, env)
	// /healthz answers before the startup reconcile finishes, so wait for its effect.
	waitFor(t, func() bool {
		d, err := env.wg.Device("wg0")
		return err == nil && len(d.Peers) == 1
	})
}

func TestClientCommands(t *testing.T) {
	env := newFakeEnv(t)
	startDaemon(t, env)
	sock := "--control=" + env.socket

	r := runCLI("", "client", "list", sock)
	if r.code != 0 || !strings.Contains(r.stdout, "No clients yet") {
		t.Fatalf("empty list: %+v", r)
	}

	r = runCLI("", "client", "add", "Alex's iPhone", sock)
	if r.code != 0 {
		t.Fatalf("add: %+v", r)
	}
	for _, want := range []string{`Added client "Alex's iPhone": 10.8.0.2, fd`, `drawbridge client qr 'Alex'\''s iPhone'`, "> Alex-s-iPhone.conf"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("add output lacks %q:\n%s", want, r.stdout)
		}
	}
	if r := runCLI("", "client", "add", "Alex's iPhone", sock); r.code != 1 || !strings.Contains(r.stderr, "already exists") {
		t.Errorf("duplicate add: %+v", r)
	}

	r = runCLI("", "client", "list", sock)
	if r.code != 0 || !strings.Contains(r.stdout, "Alex's iPhone") || !strings.Contains(r.stdout, "active") ||
		!strings.Contains(r.stdout, "never") {
		t.Fatalf("list:\n%s", r.stdout)
	}

	if r := runCLI("", "client", "pause", "Alex's iPhone", sock); r.code != 0 || !strings.Contains(r.stdout, "Paused") {
		t.Fatalf("pause: %+v", r)
	}
	if r := runCLI("", "client", "show", "Alex's iPhone", sock); !strings.Contains(r.stdout, "State:       paused") {
		t.Fatalf("show after pause:\n%s", r.stdout)
	}
	if r := runCLI("", "client", "resume", "Alex's iPhone", sock); r.code != 0 || !strings.Contains(r.stdout, "Resumed") {
		t.Fatalf("resume: %+v", r)
	}

	// No endpoint yet, so there's no config.
	if r := runCLI("", "client", "config", "Alex's iPhone", sock); r.code != 1 || !strings.Contains(r.stderr, "server set --endpoint") {
		t.Fatalf("config without an endpoint: %+v", r)
	}
	if r := runCLI("", "server", "set", "--endpoint", "vpn.example.com:443", sock); r.code != 0 ||
		!strings.Contains(r.stdout, "vpn.example.com:443") {
		t.Fatalf("server set: %+v", r)
	}
	r = runCLI("", "client", "config", "Alex's iPhone", sock)
	if r.code != 0 || !strings.HasPrefix(r.stdout, "[Interface]\n") || !strings.Contains(r.stdout, "Endpoint = vpn.example.com:443") {
		t.Fatalf("config: %+v", r)
	}
	r = runCLI("", "client", "qr", "Alex's iPhone", sock)
	if r.code != 0 || !strings.Contains(r.stdout, "▀") || !strings.Contains(r.stderr, "Scan from QR code") {
		t.Fatalf("qr: code %d, stderr %q", r.code, r.stderr)
	}
	if r := runCLI("", "client", "add", "laptop", "--qr", sock); r.code != 0 || !strings.Contains(r.stdout, "▀") {
		t.Fatalf("add --qr: %+v", r.stderr)
	}

	// The columns line up differently with and without a peer, so compare the words.
	words := func(s string) string { return strings.Join(strings.Fields(s), " ") }

	// Handing out the config is what the list and show report on: it's current until the
	// server changes, and then it's outdated until it's handed out again.
	if r := runCLI("", "client", "show", "Alex's iPhone", sock); !strings.Contains(words(r.stdout), "Config: current, last handed out") {
		t.Fatalf("show after the config was handed out:\n%s", r.stdout)
	}
	if r := runCLI("", "client", "list", sock); !strings.Contains(r.stdout, "current") {
		t.Fatalf("list after the config was handed out:\n%s", r.stdout)
	}
	if r := runCLI("", "server", "set", "--mtu", "1380", sock); r.code != 0 {
		t.Fatalf("server set --mtu: %+v", r)
	}
	if r := runCLI("", "client", "show", "Alex's iPhone", sock); !strings.Contains(words(r.stdout), "Config: outdated (last handed out") {
		t.Fatalf("show after the MTU changed:\n%s", r.stdout)
	}
	if r := runCLI("", "client", "list", sock); !strings.Contains(r.stdout, "outdated") {
		t.Fatalf("list after the MTU changed:\n%s", r.stdout)
	}
	// (laptop was added with --qr, which hands its config out; tablet wasn't.)
	if r := runCLI("", "client", "add", "tablet", sock); r.code != 0 {
		t.Fatalf("add: %+v", r)
	}
	if r := runCLI("", "client", "show", "tablet", sock); !strings.Contains(words(r.stdout), "Config: no record of it being handed out") {
		t.Fatalf("show of a client that was never given a config:\n%s", r.stdout)
	}
	if r := runCLI("", "client", "delete", "tablet", "--yes", sock); r.code != 0 {
		t.Fatalf("delete: %+v", r)
	}

	// rotate-keys asks first, like delete, and says what to do next.
	before := runCLI("", "client", "show", "Alex's iPhone", sock).stdout
	if r := runCLI("n\n", "client", "rotate-keys", "Alex's iPhone", sock); r.code != 1 || !strings.Contains(r.stdout, "Keys not rotated") {
		t.Fatalf("declined rotation: %+v", r)
	}
	if runCLI("", "client", "show", "Alex's iPhone", sock).stdout != before {
		t.Fatal("a declined rotation changed the client")
	}
	r = runCLI("y\n", "client", "rotate-keys", "Alex's iPhone", sock)
	if r.code != 0 {
		t.Fatalf("confirmed rotation: %+v", r)
	}
	for _, want := range []string{`Rotated the keys of client "Alex's iPhone"`, `drawbridge client qr 'Alex'\''s iPhone'`, "> Alex-s-iPhone.conf"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("rotate-keys output lacks %q:\n%s", want, r.stdout)
		}
	}
	if after := runCLI("", "client", "show", "Alex's iPhone", sock).stdout; after == before ||
		!strings.Contains(words(after), "Config: outdated") {
		t.Fatalf("show after the rotation:\n%s", after)
	}
	if r := runCLI("", "client", "rotate-keys", "laptop", "--yes", sock); r.code != 0 {
		t.Fatalf("rotate-keys --yes: %+v", r)
	}
	if r := runCLI("", "client", "rotate-keys", "nobody", "--yes", sock); r.code != 1 || !strings.Contains(r.stderr, "no such client") {
		t.Fatalf("rotating an unknown client: %+v", r)
	}
	// No answer is a no.
	if r := runCLI("", "client", "rotate-keys", "laptop", sock); r.code != 1 || !strings.Contains(r.stdout, "Keys not rotated") {
		t.Fatalf("rotating with no answer: %+v", r)
	}

	// delete asks first.
	if r := runCLI("n\n", "client", "delete", "laptop", sock); r.code != 1 || !strings.Contains(r.stdout, "Not deleted") {
		t.Fatalf("declined delete: %+v", r)
	}
	if r := runCLI("y\n", "client", "delete", "laptop", sock); r.code != 0 || !strings.Contains(r.stdout, `Deleted client "laptop"`) {
		t.Fatalf("confirmed delete: %+v", r)
	}
	if r := runCLI("", "client", "delete", "Alex's iPhone", "--yes", sock); r.code != 0 {
		t.Fatalf("delete --yes: %+v", r)
	}
	if r := runCLI("", "client", "show", "Alex's iPhone", sock); r.code != 1 || !strings.Contains(r.stderr, "no such client") {
		t.Fatalf("show after delete: %+v", r)
	}
}

func TestRenameAndEvents(t *testing.T) {
	env := newFakeEnv(t)
	startDaemon(t, env)
	sock := "--control=" + env.socket

	if r := runCLI("", "events", sock); r.code != 0 || !strings.Contains(r.stdout, "No events yet") {
		t.Fatalf("empty events: %+v", r)
	}
	runCLI("", "client", "add", "phone", sock)
	if r := runCLI("", "client", "rename", "phone", "Pixel 9", sock); r.code != 0 ||
		!strings.Contains(r.stdout, `Renamed client "phone" to "Pixel 9"`) {
		t.Fatalf("rename: %+v", r)
	}
	if r := runCLI("", "client", "show", "pixel 9", sock); r.code != 0 {
		t.Fatalf("show the renamed client: %+v", r)
	}
	runCLI("", "client", "add", "laptop", sock)
	if r := runCLI("", "client", "rename", "laptop", "PIXEL 9", sock); r.code != 1 || !strings.Contains(r.stderr, "already exists") {
		t.Fatalf("rename onto another client's name: %+v", r)
	}

	r := runCLI("", "events", "--client", "Pixel 9", sock)
	if r.code != 0 {
		t.Fatalf("events: %+v", r)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "client.renamed") || !strings.Contains(lines[2], "client.added") {
		t.Fatalf("events for one client:\n%s", r.stdout)
	}
	// The CLI's changes are attributed to the account that ran it, through the socket.
	if !strings.Contains(lines[1], "(cli)") || !strings.Contains(lines[1], "from: phone") ||
		!strings.Contains(lines[1], "Pixel 9") {
		t.Fatalf("rename event: %s", lines[1])
	}
	if r := runCLI("", "events", "--limit", "1", sock); strings.Count(r.stdout, "\n") != 2 {
		t.Fatalf("events --limit 1:\n%s", r.stdout)
	}
	if r := runCLI("", "events", "--limit", "0", sock); r.code != 2 || !strings.Contains(r.stderr, "limit must be") {
		t.Fatalf("events --limit 0: %+v", r)
	}
	if r := runCLI("", "events", "--client", "ghost", sock); r.code != 1 || !strings.Contains(r.stderr, "no such client") {
		t.Fatalf("events for an unknown client: %+v", r)
	}
}

func TestAdminCommands(t *testing.T) {
	env := newFakeEnv(t)
	startDaemon(t, env)
	sock := "--control=" + env.socket

	r := runCLI("", "admin", "setup-token", sock)
	if r.code != 0 || !strings.Contains(r.stdout, "Setup token: ") || !strings.Contains(r.stdout, ":51821") {
		t.Fatalf("setup-token: %+v", r)
	}
	if again := runCLI("", "admin", "setup-token", sock); again.stdout != r.stdout {
		t.Fatalf("the setup token changed:\n%s\n%s", r.stdout, again.stdout)
	}
	if r := runCLI("", "admin", "reset-password", sock); r.code != 1 || !strings.Contains(r.stderr, "no such account") {
		t.Fatalf("reset before the account exists: %+v", r)
	}
	if r := runCLI("", "admin", "create", "bad name", sock); r.code != 1 || !strings.Contains(r.stderr, "can't contain") {
		t.Fatalf("create with an invalid name: %+v", r)
	}

	r = runCLI("", "admin", "create", "admin", sock)
	if r.code != 0 || !strings.Contains(r.stdout, `Created the admin account "admin".`) {
		t.Fatalf("create: %+v", r)
	}
	password := strings.TrimSpace(strings.SplitN(strings.SplitN(r.stdout, "Password: ", 2)[1], "\n", 2)[0])
	if len(password) != 23 {
		t.Fatalf("password %q", password)
	}
	ctx := context.Background()
	if _, err := env.svc.Login(ctx, "admin", password, "test"); err != nil {
		t.Fatalf("logging in with the printed password: %v", err)
	}
	if r := runCLI("", "admin", "setup-token", sock); r.code != 1 || !strings.Contains(r.stderr, "admin reset-password") {
		t.Fatalf("setup-token after setup: %+v", r)
	}
	if r := runCLI("", "admin", "create", "second", sock); r.code != 1 || !strings.Contains(r.stderr, "already exists") {
		t.Fatalf("a second admin: %+v", r)
	}

	r = runCLI("", "admin", "reset-password", sock)
	if r.code != 0 || !strings.Contains(r.stdout, `New password for "admin": `) {
		t.Fatalf("reset-password: %+v", r)
	}
	if _, err := env.svc.Login(ctx, "admin", password, "test"); !errors.Is(err, service.ErrBadLogin) {
		t.Fatalf("the old password after a reset: %v", err)
	}
	if r := runCLI("", "events", sock); !strings.Contains(r.stdout, "auth.password_reset") ||
		!strings.Contains(r.stdout, "auth.admin_created") {
		t.Fatalf("admin events:\n%s", r.stdout)
	}
}

func TestServerCommands(t *testing.T) {
	env := newFakeEnv(t)
	// Which VPN addresses a resolver answers on: 0 none, 1 IPv4 only, 2 both.
	var answering atomic.Int32
	env.svc.DNSProbe = func(_ context.Context, a netip.Addr) service.DNSProbe {
		n := answering.Load()
		return service.DNSProbe{Answered: n == 2 || (n == 1 && a.Is4()), Detail: "probed " + a.String()}
	}
	startDaemon(t, env)
	sock := "--control=" + env.socket

	r := runCLI("", "server", "show", sock)
	for _, want := range []string{"Interface:", "wg0", "not set; run: drawbridge server set --endpoint", "10.8.0.1 in 10.8.0.0/24", "Client isolation:   on"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("show lacks %q:\n%s", want, r.stdout)
		}
	}

	if r := runCLI("", "server", "set", sock); r.code != 2 || !strings.Contains(r.stderr, "nothing to change") {
		t.Errorf("set without flags: %+v", r)
	}
	if r := runCLI("", "server", "set", "--port", "70000", sock); r.code != 2 {
		t.Errorf("bad port: %+v", r)
	}
	if r := runCLI("", "server", "set", "--dns", "not-an-ip", sock); r.code != 2 {
		t.Errorf("bad DNS: %+v", r)
	}
	if r := runCLI("", "server", "set", "--mtu", "9000", sock); r.code != 1 || !strings.Contains(r.stderr, "MTU") {
		t.Errorf("bad MTU: %+v", r)
	}

	r = runCLI("", "server", "set", "--dns", "9.9.9.9, 2620:fe::fe", "--client-isolation=false", "--keepalive", "0", "--mtu", "1412", sock)
	if r.code != 0 {
		t.Fatalf("set: %+v", r)
	}
	for _, want := range []string{"9.9.9.9, 2620:fe::fe", "Client isolation:   off", "Keepalive:          off", "MTU:                1412"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("set output lacks %q:\n%s", want, r.stdout)
		}
	}
	if d, _ := env.wg.Device("wg0"); d.MTU != 1412 {
		t.Errorf("the MTU change wasn't applied: %d", d.MTU)
	}

	// "--dns server" saves only the addresses that answer a test query.
	dnsLine := regexp.MustCompile(`(?m)^DNS:\s+(.*)$`)
	dnsOf := func(out string) string {
		if m := dnsLine.FindStringSubmatch(out); m != nil {
			return m[1]
		}
		return "no DNS line:\n" + out
	}
	r = runCLI("", "server", "set", "--dns", "server", sock)
	if r.code != 1 || !strings.Contains(r.stderr, "no DNS resolver answers") || !strings.Contains(r.stderr, "--force") {
		t.Errorf("no resolver answers: %+v", r)
	}
	if got := dnsOf(runCLI("", "server", "show", sock).stdout); got != "9.9.9.9, 2620:fe::fe" {
		t.Errorf("a refused --dns server changed the DNS to %q", got)
	}
	if r := runCLI("", "server", "set", "--force", sock); r.code != 2 || !strings.Contains(r.stderr, "nothing to change") {
		t.Errorf("--force alone: %+v", r)
	}
	r = runCLI("", "server", "set", "--dns", "server", "--force", sock)
	if got := dnsOf(r.stdout); r.code != 0 || !strings.HasPrefix(got, "10.8.0.1, fd") {
		t.Errorf("--force with nothing answering: %q, %+v", got, r)
	}
	answering.Store(1)
	r = runCLI("", "server", "set", "--dns", "9.9.9.9", sock)
	r = runCLI("", "server", "set", "--dns", "server", sock)
	if got := dnsOf(r.stdout); r.code != 0 || got != "10.8.0.1" {
		t.Errorf("IPv4 answers only: %q, %+v", got, r)
	}
	if !strings.Contains(r.stderr, "DNS check: fd") {
		t.Errorf("the skipped IPv6 address isn't reported:\n%s", r.stderr)
	}
	answering.Store(2)
	r = runCLI("", "server", "set", "--dns", "server", sock)
	if got := dnsOf(r.stdout); r.code != 0 || !strings.HasPrefix(got, "10.8.0.1, fd") {
		t.Errorf("both answer: %q, %+v", got, r)
	}
}

func TestCommandsWithoutDaemon(t *testing.T) {
	sock := "--control=" + filepath.Join(t.TempDir(), "missing.sock")
	r := runCLI("", "client", "list", sock)
	if r.code != 1 || !strings.Contains(r.stderr, "is drawbridge.service running?") {
		t.Fatalf("%+v", r)
	}
}

func TestRunTunnel(t *testing.T) {
	ctx := context.Background()
	env := newFakeEnv(t)
	if err := runTunnel(ctx, "down", env.store, env.rec, discard); err != nil {
		t.Fatal(err)
	}
	if _, err := env.wg.Device("wg0"); !errors.Is(err, wg.ErrNoDevice) {
		t.Fatal("tunnel down left the interface")
	}
	if err := runTunnel(ctx, "up", env.store, env.rec, discard); err != nil {
		t.Fatal(err)
	}
	if d, err := env.wg.Device("wg0"); err != nil || !d.Up {
		t.Fatalf("tunnel up: %+v, %v", d, err)
	}
}

func TestParseEndpoint(t *testing.T) {
	cases := map[string]struct {
		host string
		port uint16
	}{
		"vpn.example.com":      {"vpn.example.com", 0},
		"vpn.example.com:443":  {"vpn.example.com", 443},
		"203.0.113.5":          {"203.0.113.5", 0},
		"203.0.113.5:51820":    {"203.0.113.5", 51820},
		"2001:db8::1":          {"2001:db8::1", 0},
		"[2001:db8::1]:51820":  {"2001:db8::1", 51820},
		" vpn.example.com ":    {"vpn.example.com", 0},
		"2001:DB8:0:0:0:0:0:1": {"2001:db8::1", 0},
	}
	for in, want := range cases {
		host, port, err := parseEndpoint(in)
		if err != nil || host != want.host || port != want.port {
			t.Errorf("%q: got %q, %d, %v", in, host, port, err)
		}
	}
	for _, bad := range []string{"vpn.example.com:0", "vpn.example.com:99999", "vpn.example.com:http"} {
		if _, _, err := parseEndpoint(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFormatting(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		12 * time.Second: "12s ago", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago", 72 * time.Hour: "3d ago",
	} {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("ago(%s) = %q, want %q", d, got, want)
		}
	}
	if ago(time.Time{}, now) != "never" {
		t.Error("zero time isn't never")
	}
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"} {
		if got := bytesText(n); got != want {
			t.Errorf("bytesText(%d) = %q, want %q", n, got, want)
		}
	}
	for in, want := range map[string]string{"phone": "phone", "Alex's iPhone": `'Alex'\''s iPhone'`, "a b": "'a b'"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMain keeps the machine's own journal out of the tests. A test machine can itself run
// under systemd (a CI runner does), which sets JOURNAL_STREAM and makes the daemon log to the
// real journal, and then the tests that read its standard error find nothing. The tests that
// want the journal set the variable themselves, and name a socket of their own.
func TestMain(m *testing.M) {
	_ = os.Unsetenv("JOURNAL_STREAM")
	os.Exit(m.Run())
}

func TestLoggerDropsTimeUnderJournald(t *testing.T) {
	var buf bytes.Buffer
	missing := filepath.Join(t.TempDir(), "no-journal.sock")
	t.Setenv("JOURNAL_STREAM", "8:12345")
	newLoggerAt(&buf, missing).Info("hello")
	if strings.Contains(buf.String(), "time=") {
		t.Fatalf("log line %q has a timestamp; journald adds its own", buf.String())
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("log line %q: with no journal to reach, the text goes to the writer", buf.String())
	}

	buf.Reset()
	t.Setenv("JOURNAL_STREAM", "")
	newLoggerAt(&buf, missing).Info("hello")
	if !strings.Contains(buf.String(), "time=") {
		t.Fatalf("log line %q has no timestamp outside journald", buf.String())
	}
}

func TestLoggerSendsFieldsToJournald(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	read := func() string {
		buf := make([]byte, 64<<10)
		_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		n, _ := conn.Read(buf)
		return string(buf[:n])
	}

	// Under systemd, a record is a journal entry with its attributes as fields, and the
	// writer (standard error) gets nothing, so the journal doesn't have it twice.
	var buf bytes.Buffer
	t.Setenv("JOURNAL_STREAM", "8:12345")
	newLoggerAt(&buf, path).Info("client added", "client", "phone")
	entry := read()
	for _, want := range []string{"PRIORITY=6\n", "DRAWBRIDGE_CLIENT=phone\n", `MESSAGE=level=INFO msg="client added" client=phone` + "\n"} {
		if !strings.Contains(entry, want) {
			t.Errorf("entry %q lacks %q", entry, want)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("standard error got %q as well", buf.String())
	}

	// Run by hand, the daemon logs to the terminal, and never to the journal of the host.
	t.Setenv("JOURNAL_STREAM", "")
	newLoggerAt(&buf, path).Info("by hand")
	if entry := read(); entry != "" {
		t.Errorf("a daemon run by hand wrote %q to the journal", entry)
	}
	if !strings.Contains(buf.String(), "by hand") {
		t.Errorf("a daemon run by hand logged %q to the terminal", buf.String())
	}
}

func TestDaemonServesTheAPIOverTLS(t *testing.T) {
	env := newFakeEnv(t)
	certs, err := tlscert.Open(filepath.Join(t.TempDir(), "tls"), tlscert.Options{
		Names: func() tlscert.Names { return tlscert.DefaultNames("server") },
	})
	if err != nil {
		t.Fatal(err)
	}
	env.svc.TLS = certs
	cert, _ := certs.GetCertificate(nil)
	web, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctl, err := control.Listen(env.socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- daemon{web: web, control: ctl, svc: env.svc, drift: time.Hour, log: discard,
			tls:     certs.Config(),
			allowed: allowlistFor(env.svc, func() []netip.Prefix { return nil })}.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})

	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	base := "https://localhost:" + strconv.Itoa(web.Addr().(*net.TCPAddr).Port)
	waitFor(t, func() bool {
		resp, err := client.Get(base + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	// Plain HTTP gets nowhere.
	if resp, err := http.Get("http://" + web.Addr().String() + "/healthz"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("the daemon answered plain HTTP")
		}
	}

	// The token comes from the CLI, with the certificate's fingerprint to check.
	r := runCLI("", "admin", "setup-token", "--control="+env.socket)
	if r.code != 0 || !strings.Contains(r.stdout, tlscert.Fingerprint(*cert)) {
		t.Fatalf("setup-token: %+v", r)
	}
	token := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(r.stdout, "Setup token: "), "\n", 2)[0])

	post := func(path string, body any) int {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", base+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Drawbridge", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := post("/api/setup", views.SetupRequest{Token: token, Username: "admin", Password: "a long password"}); status != http.StatusCreated {
		t.Fatalf("setup: status %d", status)
	}
	if status := post("/api/clients", views.NewClientRequest{Name: "phone"}); status != http.StatusCreated {
		t.Fatalf("add a client: status %d", status)
	}
	// The CLI sees what the web did, and the log says who did it.
	if r := runCLI("", "client", "list", "--control="+env.socket); !strings.Contains(r.stdout, "phone") {
		t.Fatalf("client list: %+v", r)
	}
	r = runCLI("", "events", "--control="+env.socket)
	if !strings.Contains(r.stdout, "admin (web 127.0.0.1)") || !strings.Contains(r.stdout, "auth.setup_completed") {
		t.Fatalf("events:\n%s", r.stdout)
	}
}

// TestDaemonStopsWithAStreamOpen checks that the daemon's shutdown doesn't wait on a browser's
// open stream, which would never end by itself: the graceful shutdown would run out its ten
// seconds and the daemon would exit with an error.
func TestDaemonStopsWithAStreamOpen(t *testing.T) {
	env := newFakeEnv(t)
	cert, _, err := tlscert.Ensure(filepath.Join(t.TempDir(), "tls"), tlscert.DefaultNames("server"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	web, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- daemon{web: web, svc: env.svc, drift: time.Hour, log: discard, tls: tlscert.Config(cert),
			sessionInterval: 0}.run(ctx)
	}()

	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	base := "https://localhost:" + strconv.Itoa(web.Addr().(*net.TCPAddr).Port)
	waitFor(t, func() bool {
		resp, err := client.Get(base + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	password, err := env.svc.CreateAdmin(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(views.LoginRequest{Username: "admin", Password: password})
	req, _ := http.NewRequest("POST", base+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Drawbridge", "1")
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v, %v", resp, err)
	}
	resp.Body.Close()

	stream, err := client.Get(base + "/api/stream")
	if err != nil || stream.StatusCode != http.StatusOK {
		t.Fatalf("stream: %v, %v", stream, err)
	}
	defer stream.Body.Close()
	first := make([]byte, 64)
	if _, err := stream.Body.Read(first); err != nil || !strings.Contains(string(first), "retry:") {
		t.Fatalf("the stream's first bytes %q, %v", first, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the daemon stopped with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't stop with a stream open")
	}
}

func TestServeWithTheFakeBackend(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.key")
	if err := os.WriteFile(secret, bytes.Repeat([]byte{4}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		done <- run(ctx, []string{"serve", "--backend", "fake", "--listen", "127.0.0.1:0",
			"--db", filepath.Join(dir, "db"), "--secret-key", secret, "--control", socket}, nil, io.Discard, &stderr)
	}()
	waitFor(t, func() bool {
		return runCLI("", "server", "show", "--control="+socket).code == 0
	})
	// The fake tunnel is up, so changes apply to it like the real one.
	runCLI("", "client", "add", "phone", "--control="+socket)
	if r := runCLI("", "client", "list", "--control="+socket); !strings.Contains(r.stdout, "active") {
		t.Fatalf("client list with the fake backend:\n%s", r.stdout)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("serve exited %d:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "fake WireGuard backend") {
		t.Fatalf("serve didn't warn about the fake backend:\n%s", stderr.String())
	}
}

func TestAdminAllowSetsTheUIAllowlist(t *testing.T) {
	env := newFakeEnv(t)
	startDaemon(t, env)
	sock := "--control=" + env.socket
	allowed := allowlistFor(env.svc, func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("192.168.4.0/22")} })
	reaches := func(addr string) bool { return lan.Contains(allowed(context.Background()), netip.MustParseAddr(addr)) }

	if reaches("100.64.10.9") {
		t.Fatal("Tailscale reaches the UI before it's allowed")
	}
	r := runCLI("", "server", "set", "--admin-allow", "100.64.10.75/24", sock)
	if r.code != 0 || !strings.Contains(r.stdout, "Admin sources:") || !strings.Contains(r.stdout, "100.64.10.0/24") {
		t.Fatalf("set: %+v", r)
	}
	if !reaches("100.64.10.9") || !reaches("192.168.4.20") || reaches("100.64.11.9") || reaches("203.0.113.5") {
		t.Fatalf("allowlist %v", allowed(context.Background()))
	}
	if r := runCLI("", "server", "show", sock); !strings.Contains(r.stdout, "100.64.10.0/24") {
		t.Errorf("show:\n%s", r.stdout)
	}
	if r := runCLI("", "events", sock); !strings.Contains(r.stdout, "server.settings_changed") || !strings.Contains(r.stdout, "admin_allowed") {
		t.Errorf("events:\n%s", r.stdout)
	}

	if r := runCLI("", "server", "set", "--admin-allow", "not-a-prefix", sock); r.code != 2 {
		t.Errorf("bad prefix: %+v", r)
	}
	if r := runCLI("", "server", "set", "--admin-allow", "203.0.113.0/24", sock); r.code != 1 || !strings.Contains(r.stderr, "private range") {
		t.Errorf("public range: %+v", r)
	}
	if r := runCLI("", "server", "set", "--admin-allow", "0.0.0.0/0", sock); r.code != 1 {
		t.Errorf("everything: %+v", r)
	}
	if !reaches("100.64.10.9") {
		t.Fatal("a rejected change removed the allowed source")
	}

	if r := runCLI("", "server", "set", "--admin-allow", "none", sock); r.code != 0 || strings.Contains(r.stdout, "100.64.10.0/24") {
		t.Fatalf("none: %+v", r)
	}
	if reaches("100.64.10.9") {
		t.Fatal("Tailscale still reaches the UI after removing it")
	}
}

func TestDoctorCommand(t *testing.T) {
	env := newFakeEnv(t)
	env.svc.Diag = &diag.Host{
		FS: fstest.MapFS{
			"proc/sys/net/ipv4/ip_forward":          {Data: []byte("0\n")},
			"proc/sys/net/ipv6/conf/all/forwarding": {Data: []byte("1\n")},
		},
		Network: func() (lan.Snapshot, error) { return lan.Snapshot{}, errors.New("no netlink here") },
		Nft:     func(context.Context) ([]byte, error) { return nil, errors.New("no nft here") },
	}
	startDaemon(t, env)
	sock := "--control=" + env.socket

	r := runCLI("", "doctor", sock)
	if r.code != 1 {
		t.Fatalf("doctor with a failing check exited %d:\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{
		"PASS  Tunnel",
		"FAIL  Forwarding sysctls",
		"      IPv4 forwarding is off, so VPN clients' traffic isn't routed.",
		"      Fix: sudo sysctl -w net.ipv4.ip_forward=1. ",
		"SKIP  Uplink",
		" failed, ",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("doctor's output lacks %q:\n%s", want, r.stdout)
		}
	}
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(line, "      ") && !strings.Contains(line, "Fix: ") && len(line) > doctorWrap {
			t.Errorf("a detail line is %d columns wide: %q", len(line), line)
		}
	}
	if r.stderr != "" {
		t.Errorf("doctor wrote to stderr: %q", r.stderr)
	}

	if r := runCLI("", "doctor", "extra", sock); r.code != 2 || !strings.Contains(r.stderr, `unexpected argument "extra"`) {
		t.Errorf("doctor with an argument: %+v", r)
	}

	// The diagnostics change nothing, so they leave no events.
	if r := runCLI("", "events", sock); strings.Contains(r.stdout, "doctor") || strings.Contains(r.stdout, "diagnos") {
		t.Errorf("doctor recorded an event:\n%s", r.stdout)
	}
}

func TestDoctorWithoutDiagnostics(t *testing.T) {
	env := newFakeEnv(t)
	startDaemon(t, env)
	if r := runCLI("", "doctor", "--control="+env.socket); r.code != 1 || !strings.Contains(r.stderr, "drawbridge:") || r.stdout != "" {
		t.Errorf("doctor without diagnostics: %+v", r)
	}
	if r := runCLI("", "doctor", "--control="+filepath.Join(t.TempDir(), "none.sock")); r.code != 1 || !strings.Contains(r.stderr, "is drawbridge.service running?") {
		t.Errorf("doctor without a daemon: %+v", r)
	}
}

func TestPrintDiagnostics(t *testing.T) {
	var out bytes.Buffer
	failed := printDiagnostics(&out, views.Diagnostics{Checks: []views.DiagnosticCheck{
		{Name: "One", Status: "pass", Detail: "Fine."},
		{Name: "Two", Status: "warn", Detail: strings.Repeat("word ", 30), Hint: "do this && that"},
		{Name: "Three", Status: "skip", Detail: "Couldn't read it.", Hint: "not shown"},
	}})
	if failed != 0 {
		t.Errorf("%d failed, want 0: warnings don't count", failed)
	}
	want := `PASS  One
      Fine.
WARN  Two
      word word word word word word word word word word word word word word
      word word word word word word word word word word word word word word
      word word
      Fix: do this && that
SKIP  Three
      Couldn't read it.

1 passed, 1 warning, 0 failed, 1 skipped
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// Stopping the daemon doesn't wait on a connection that never asked for anything. net/http's
// Shutdown does, for five seconds, and a browser opens spare connections that it may never use:
// `systemctl stop` shouldn't take that long because of one.
func TestDaemonStopsWithAnUnusedConnectionOpen(t *testing.T) {
	for name, open := range map[string]func(t *testing.T, addr string, pool *x509.CertPool) net.Conn{
		// The handshake is done, and no request follows: what a client's spare connection is.
		"after the handshake": func(t *testing.T, addr string, pool *x509.CertPool) net.Conn {
			c, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12})
			if err != nil {
				t.Fatal(err)
			}
			return c
		},
		// Connected, and nothing sent at all: what a scanner leaves behind.
		"before the handshake": func(t *testing.T, addr string, _ *x509.CertPool) net.Conn {
			c, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			return c
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := newFakeEnv(t)
			cert, _, err := tlscert.Ensure(filepath.Join(t.TempDir(), "tls"), tlscert.DefaultNames("server"), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			web, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- daemon{web: web, svc: env.svc, drift: time.Hour, log: discard, tls: tlscert.Config(cert)}.run(ctx)
			}()
			pool := x509.NewCertPool()
			pool.AddCert(cert.Leaf)
			waitFor(t, func() bool {
				c, err := net.Dial("tcp", web.Addr().String())
				if err != nil {
					return false
				}
				_ = c.Close()
				return true
			})

			conn := open(t, web.Addr().String(), pool)
			defer conn.Close()
			// Let the server see the connection before it's asked to stop.
			time.Sleep(200 * time.Millisecond)

			start := time.Now()
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("the daemon stopped with %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the daemon didn't stop with an unused connection open")
			}
			if took := time.Since(start); took > 2*time.Second {
				t.Errorf("stopping took %v", took)
			}
		})
	}
}

func TestServerSafeCommands(t *testing.T) {
	env := newFakeEnv(t)
	startDaemon(t, env)
	sock := "--control=" + env.socket
	port := func() string {
		m := regexp.MustCompile(`Listen port:\s+(\d+)`).FindStringSubmatch(runCLI("", "server", "show", sock).stdout)
		if m == nil {
			t.Fatal("no listen port in server show")
		}
		return m[1]
	}

	// Without --safe the CLI applies at once and nothing waits: the person at the host can't be
	// cut off by it.
	if r := runCLI("", "server", "set", "--port", "51900", sock); r.code != 0 || strings.Contains(r.stdout, "Waiting to be kept") {
		t.Fatalf("set --port: %+v", r)
	}
	if got := port(); got != "51900" {
		t.Fatalf("port %s", got)
	}
	if r := runCLI("", "server", "show", sock); strings.Contains(r.stdout, "Waiting to be kept") {
		t.Fatalf("show with nothing waiting:\n%s", r.stdout)
	}
	if r := runCLI("", "server", "confirm", sock); r.code != 1 || !strings.Contains(r.stderr, "no settings change is waiting") {
		t.Fatalf("confirm with nothing waiting: %+v", r)
	}

	// With --safe it does, and says how to keep it.
	r := runCLI("", "server", "set", "--port", "51999", "--safe", sock)
	if r.code != 0 {
		t.Fatalf("set --safe: %+v", r)
	}
	for _, want := range []string{"Waiting to be kept", "listen port: 51900 → 51999", "undone in", "drawbridge server confirm", "drawbridge server revert"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("set --safe output lacks %q:\n%s", want, r.stdout)
		}
	}
	if got := port(); got != "51999" {
		t.Fatalf("the change wasn't applied: %s", got)
	}
	if r := runCLI("", "server", "show", sock); !strings.Contains(r.stdout, "Waiting to be kept") || !strings.Contains(r.stdout, "51900 → 51999") {
		t.Fatalf("show with a change waiting:\n%s", r.stdout)
	}

	// Nothing else changes meanwhile, and the error says what to do.
	r = runCLI("", "server", "set", "--mtu", "1380", sock)
	if r.code != 1 || !strings.Contains(r.stderr, "waiting to be kept") || !strings.Contains(r.stderr, "drawbridge server confirm") {
		t.Fatalf("set while a change waits: %+v", r)
	}

	if r := runCLI("", "server", "confirm", sock); r.code != 0 || !strings.Contains(r.stdout, "Kept the change.") {
		t.Fatalf("confirm: %+v", r)
	}
	if got := port(); got != "51999" {
		t.Fatalf("keeping the change undid it: %s", got)
	}

	// Undoing one brings the old settings back.
	if r := runCLI("", "server", "set", "--port", "52000", "--safe", sock); r.code != 0 {
		t.Fatalf("set --safe: %+v", r)
	}
	if r := runCLI("", "server", "revert", sock); r.code != 0 || !strings.Contains(r.stdout, "Undid the change") {
		t.Fatalf("revert: %+v", r)
	}
	if got := port(); got != "51999" {
		t.Fatalf("after revert the port is %s, want 51999", got)
	}

	// A change that can't lock anyone out has nothing to wait for.
	r = runCLI("", "server", "set", "--mtu", "1380", "--safe", sock)
	if r.code != 0 || !strings.Contains(r.stdout, "nothing to confirm") || strings.Contains(r.stdout, "Waiting to be kept") {
		t.Fatalf("set --mtu --safe: %+v", r)
	}
	// --safe alone is not a change.
	if r := runCLI("", "server", "set", "--safe", sock); r.code != 2 || !strings.Contains(r.stderr, "nothing to change") {
		t.Fatalf("set --safe alone: %+v", r)
	}
}

func TestServerRotateKeyCommand(t *testing.T) {
	env := newFakeEnv(t)
	startDaemon(t, env)
	sock := "--control=" + env.socket
	publicKey := func() string {
		m := regexp.MustCompile(`Public key:\s+(\S+)`).FindStringSubmatch(runCLI("", "server", "show", sock).stdout)
		if m == nil {
			t.Fatal("no public key in server show")
		}
		return m[1]
	}
	if r := runCLI("", "server", "set", "--endpoint", "vpn.example.com", sock); r.code != 0 {
		t.Fatalf("set endpoint: %+v", r)
	}
	if r := runCLI("", "client", "add", "phone", "--qr", sock); r.code != 0 { // --qr hands its config out
		t.Fatalf("add: %+v", r)
	}
	old := publicKey()

	// It asks first, because every client stops working, and no answer is a no.
	if r := runCLI("n\n", "server", "rotate-key", sock); r.code != 1 || !strings.Contains(r.stdout, "The key was not rotated") {
		t.Fatalf("declined: %+v", r)
	}
	if r := runCLI("", "server", "rotate-key", sock); r.code != 1 || !strings.Contains(r.stdout, "The key was not rotated") {
		t.Fatalf("no answer: %+v", r)
	}
	if publicKey() != old {
		t.Fatal("a declined rotation changed the key")
	}

	// Confirmed, it applies at once (the person at the host can't be cut off by it), says what
	// the new key is, and how many clients need a new config.
	r := runCLI("y\n", "server", "rotate-key", sock)
	if r.code != 0 {
		t.Fatalf("confirmed: %+v", r)
	}
	now := publicKey()
	if now == old {
		t.Fatal("the key didn't change")
	}
	for _, want := range []string{"The server has a new key.", "Public key: " + now, "1 of the clients hold a config with the old key", "drawbridge client config NAME"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("rotate-key output lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "Waiting to be kept") {
		t.Errorf("a rotation without --safe waits:\n%s", r.stdout)
	}
	if r := runCLI("", "client", "list", sock); !strings.Contains(r.stdout, "outdated") {
		t.Fatalf("the client isn't flagged:\n%s", r.stdout)
	}

	// --safe puts it on probation, and `server revert` brings the old key back.
	r = runCLI("", "server", "rotate-key", "--yes", "--safe", sock)
	if r.code != 0 {
		t.Fatalf("--safe: %+v", r)
	}
	safeKey := publicKey()
	if safeKey == now {
		t.Fatal("the key didn't change")
	}
	for _, want := range []string{"Waiting to be kept", "server public key: " + now + " → " + safeKey, "drawbridge server confirm", "drawbridge server revert"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("rotate-key --safe output lacks %q:\n%s", want, r.stdout)
		}
	}
	if r := runCLI("", "server", "rotate-key", "--yes", sock); r.code != 1 || !strings.Contains(r.stderr, "waiting to be kept") || !strings.Contains(r.stderr, "drawbridge server confirm") {
		t.Fatalf("rotating while a change waits: %+v", r)
	}
	if r := runCLI("", "server", "revert", sock); r.code != 0 {
		t.Fatalf("revert: %+v", r)
	}
	if got := publicKey(); got != now {
		t.Fatalf("after revert the key is %s, want %s", got, now)
	}

	// Nothing else is accepted along with it.
	if r := runCLI("", "server", "rotate-key", "extra", sock); r.code != 2 || !strings.Contains(r.stderr, `unexpected argument "extra"`) {
		t.Fatalf("rotate-key extra: %+v", r)
	}
	if r := runCLI("", "events", "--limit", "20", sock); !strings.Contains(r.stdout, "server.key_rotated") {
		t.Fatalf("no key_rotated event:\n%s", r.stdout)
	}
}

func TestApplyCommand(t *testing.T) {
	env := newFakeEnv(t)
	startDaemon(t, env)
	sock := "--control=" + env.socket
	for _, args := range [][]string{{"apply", sock}, {"apply", "--dry-run", sock}} {
		r := runCLI("", args...)
		if r.code != 0 || !strings.Contains(r.stdout, "Nothing to change") {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	if r := runCLI("", "apply", "extra", sock); r.code != 2 || !strings.Contains(r.stderr, `unexpected argument "extra"`) {
		t.Fatalf("apply extra: %+v", r)
	}
	if r := runCLI("", "apply", "--control="+filepath.Join(t.TempDir(), "nope.sock")); r.code != 1 || !strings.Contains(r.stderr, "can't reach the Drawbridge daemon") {
		t.Fatalf("apply without a daemon: %+v", r)
	}
}

func TestTLSCommand(t *testing.T) {
	env := newFakeEnv(t)
	certs, err := tlscert.Open(filepath.Join(t.TempDir(), "tls"), tlscert.Options{
		Names: func() tlscert.Names { return tlscert.DefaultNames("server") },
	})
	if err != nil {
		t.Fatal(err)
	}
	env.svc.TLS = certs
	startDaemon(t, env)
	sock := "--control=" + env.socket
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// Out of the box: the self-signed certificate, which the setup token tells the admin to check
	// a browser's warning against.
	r := runCLI("", "tls", "show", sock)
	if r.code != 0 || !strings.Contains(r.stdout, "Certificate: self-signed by Drawbridge") ||
		!strings.Contains(r.stdout, "SHA-256:     "+certs.Fingerprint()) || strings.Contains(r.stdout, "Note:") {
		t.Fatalf("show: %+v", r)
	}
	if r := runCLI("", "admin", "setup-token", sock); !strings.Contains(r.stdout, certs.Fingerprint()) {
		t.Fatalf("the setup token doesn't show the fingerprint:\n%s", r.stdout)
	}

	mine := tlscerttest.New(t, time.Now(), "vpn.example.com", "192.168.4.10")
	certFile, keyFile := write("fullchain.pem", mine.Cert), write("privkey.pem", mine.Key)

	// Mistakes are the command's to catch: both files are needed, and a file that isn't a
	// certificate and its key is refused with what's wrong, changing nothing.
	if r := runCLI("", "tls", "install", "--cert", certFile, sock); r.code != 2 || !strings.Contains(r.stderr, "--cert and --key are both needed") {
		t.Errorf("without --key: %+v", r)
	}
	other := tlscerttest.New(t, time.Now(), "vpn.example.com")
	if r := runCLI("", "tls", "install", "--cert", certFile, "--key", write("other.pem", other.Key), sock); r.code != 1 ||
		!strings.Contains(r.stderr, "doesn't belong to the first certificate") {
		t.Errorf("a key that doesn't match: %+v", r)
	}
	if r := runCLI("", "tls", "install", "--cert", certFile, "--key", filepath.Join(dir, "missing.pem"), sock); r.code != 1 || !strings.Contains(r.stderr, "missing.pem") {
		t.Errorf("a missing file: %+v", r)
	}
	if r := runCLI("", "tls", "install", "--cert", certFile, "--key", write("huge.pem", strings.Repeat("x", maxCertFile+1)), sock); r.code != 1 ||
		!strings.Contains(r.stderr, "is it the right file?") {
		t.Errorf("a huge file: %+v", r)
	}
	if certs.Info().Source != tlscert.SelfSigned {
		t.Fatal("a refused install changed the certificate")
	}

	// Installed, it's what a new connection is shown, with no restart, and the command says what
	// the certificate is, as `show` does afterward.
	r = runCLI("", "tls", "install", "--cert", certFile, "--key", keyFile, sock)
	if r.code != 0 {
		t.Fatalf("install: %+v", r)
	}
	for _, want := range []string{"now serves your certificate", "Certificate: installed by you", "Names:       vpn.example.com, 192.168.4.10",
		"Chain:       1 certificate", "(89 days left)", "SHA-256:     " + certs.Fingerprint()} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("install output lacks %q:\n%s", want, r.stdout)
		}
	}
	if !bytes.Equal(tlscerttest.Shown(t, certs.Config()), mine.DER) {
		t.Fatal("a new connection isn't shown the installed certificate")
	}
	if shown := runCLI("", "tls", "show", sock); !strings.Contains(shown.stdout, "installed by you") || !strings.Contains(shown.stdout, certs.Fingerprint()) {
		t.Errorf("show after install:\n%s", shown.stdout)
	}
	// A certificate the admin installed needs no fingerprint check, so the token doesn't ask for one.
	if r := runCLI("", "admin", "setup-token", sock); strings.Contains(r.stdout, "self-signed") || strings.Contains(r.stdout, certs.Fingerprint()) {
		t.Errorf("the setup token still asks for a fingerprint:\n%s", r.stdout)
	}

	// One file with both is fine for each flag, as some tools write them.
	both := write("both.pem", mine.Key+mine.Cert)
	if r := runCLI("", "tls", "install", "--cert", both, "--key", both, sock); r.code != 0 {
		t.Errorf("one file with both: %+v", r)
	}

	// Going back, and saying so when there is nothing to go back from.
	if r := runCLI("", "tls", "reset", sock); r.code != 0 || !strings.Contains(r.stdout, "back on its self-signed certificate") ||
		!strings.Contains(r.stdout, "Certificate: self-signed by Drawbridge") {
		t.Errorf("reset: %+v", r)
	}
	if bytes.Equal(tlscerttest.Shown(t, certs.Config()), mine.DER) {
		t.Error("a new connection is still shown the installed certificate")
	}
	if r := runCLI("", "tls", "reset", sock); r.code != 1 || !strings.Contains(r.stderr, "already using its self-signed certificate") {
		t.Errorf("a second reset: %+v", r)
	}

	// Usage.
	if r := runCLI("", "tls"); r.code != 2 || !strings.Contains(r.stderr, "Usage: drawbridge tls") {
		t.Errorf("tls alone: %+v", r)
	}
	if r := runCLI("", "tls", "renew"); r.code != 2 || !strings.Contains(r.stderr, `unknown command "renew"`) {
		t.Errorf("an unknown command: %+v", r)
	}
	if r := runCLI("", "tls", "show", "extra", sock); r.code != 2 {
		t.Errorf("an extra argument: %+v", r)
	}
}
