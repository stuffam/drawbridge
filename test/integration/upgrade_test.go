//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vishvananda/netns"

	"github.com/stuffam/drawbridge/internal/snapshot"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/store/storetest"
)

// TestUpgradeFromAnOlderBuild is the kernel half of the upgrade matrix (docs/PLAN.md §12): a
// real older build sets up a host, with a client that's connected through the tunnel, and this
// build takes it over the way the package does. The older build is the binary in
// DRAWBRIDGE_OLD_BIN (`make test-upgrade` builds one from each ref in test/integration/upgrade-from
// and runs this once for each); without it the test skips.
//
// What an upgrade owes the admin is that the VPN never notices: the interface, its peers, and the
// client's packets are untouched while the daemon is swapped, nothing the older build stored is
// lost, the account still logs in, and the config a client was handed still works after the host
// reboots, which brings the tunnel up again from the migrated database.
func TestUpgradeFromAnOlderBuild(t *testing.T) {
	oldBin := os.Getenv("DRAWBRIDGE_OLD_BIN")
	if oldBin == "" {
		t.Skip("set DRAWBRIDGE_OLD_BIN to an older build to run the upgrade test (make test-upgrade builds them)")
	}
	tp := newTopology(t)
	startWeb(t, tp)
	srv := newServer(t, tp)
	newBin := srv.bin
	srv.bin = oldBin

	// The older build's host: the tunnel unit's job, the daemon, two clients (one paused), and
	// an admin account that's logged in.
	srv.tunnel("up")
	srv.startDaemonWith("--drift-interval", "1s")
	srv.cli("server", "set", "--endpoint", srvAddr4)
	srv.cli("client", "add", "phone")
	srv.cli("client", "add", "tablet")
	srv.cli("client", "pause", "tablet")
	password := passwordIn(t, srv.cli("admin", "create", "admin"))
	oldConfig := srv.cli("client", "config", "phone")
	cfg := parseConfig(t, oldConfig)
	dev, err := srv.device()
	if err != nil {
		t.Fatal(err)
	}
	var vpn4, vpn6 netip.Prefix
	for _, a := range dev.Addrs {
		if a.Addr().Is4() {
			vpn4 = a.Masked()
		} else {
			vpn6 = a.Masked()
		}
	}
	cl := newClient(t, tp)
	cl.configure(cfg, vpn4, vpn6)
	wantSource(t, cl, web4, srvAddr4)
	wantSource(t, cl, web6, srvAddr6)

	certFile := filepath.Join(filepath.Dir(srv.db), "tls", "cert.pem")
	oldCert, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	browser := srv.adminClient(tp.netNS) // trusts the certificate the older build made
	if browser.Jar, err = cookiejar.New(nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "the admin to log in to the older build", func() error {
		return apiLogin(browser, "admin", password)
	})
	was := kernelState(t, srv)

	// The package upgrade: the daemon stops and the new one starts on the same database. The
	// tunnel unit isn't restarted, so the interface stays as it is, with the client sending
	// through it the whole time.
	p := startProbe(tp, web4)
	srv.stopDaemon()
	oldSchema := storetest.Version(t, srv.db)
	before := storetest.Rows(t, srv.db)
	t.Logf("upgrading from schema %d to %d", oldSchema, store.LatestSchema())

	srv.bin = newBin
	srv.logMu.Lock()
	srv.daemonLg.Reset()
	srv.logMu.Unlock()
	srv.startDaemonWith("--drift-interval", "1s", "--safe-apply-window", "4s")

	// What the older build stored is all there: checked at once, before the daemon's own
	// monitoring (connections and traffic, which it keeps writing) can change a row.
	storetest.Kept(t, before, srv.db, storetest.Except("client_sessions", "traffic"))
	// Several drift checks, and the first of the daemon's monitoring, run with the probe going.
	time.Sleep(4 * time.Second)
	attempts, failures, lastErr := p.end()
	t.Logf("%d fetches through the tunnel while the daemon was swapped, %d failed", attempts, failures)
	if attempts < 20 || failures > 0 {
		t.Errorf("%d of %d fetches through the tunnel failed while the daemon was swapped (last: %v)", failures, attempts, lastErr)
	}

	// The migration, and the snapshot taken first.
	srv.logMu.Lock()
	log := srv.daemonLg.String()
	srv.logMu.Unlock()
	backups := filepath.Join(filepath.Dir(srv.db), "backups")
	snaps, _ := snapshot.List(backups)
	if oldSchema < store.LatestSchema() {
		if !strings.Contains(log, "upgraded the database") || !strings.Contains(log, fmt.Sprintf("from_schema=%d", oldSchema)) {
			t.Errorf("the daemon didn't say it upgraded the database from schema %d:\n%s", oldSchema, log)
		}
		if len(snaps) != 1 || snaps[0].Kind != snapshot.PreMigration || snaps[0].Schema != oldSchema {
			t.Fatalf("snapshots %+v, want one from before the migration from schema %d", snaps, oldSchema)
		}
		// The snapshot is the older build's own database, row for row.
		snap := filepath.Join(backups, snaps[0].Name)
		if got := storetest.Rows(t, snap); !reflect.DeepEqual(got, before) {
			t.Errorf("the snapshot isn't the database the older build left")
		}
	} else if strings.Contains(log, "upgraded the database") || len(snaps) != 0 {
		t.Errorf("a database that's current was migrated or snapshotted: %v\n%s", snaps, log)
	}
	if got := storetest.Version(t, srv.db); got != store.LatestSchema() {
		t.Errorf("the database is at schema %d, want %d", got, store.LatestSchema())
	}

	// The kernel never noticed: the same interface (not a new one with the same name), the
	// same key and port, and the same peers.
	if now := kernelState(t, srv); !reflect.DeepEqual(now, was) {
		t.Errorf("the tunnel changed under the upgrade:\nbefore: %+v\nafter:  %+v", was, now)
	}
	wantSource(t, cl, web4, srvAddr4)
	wantSource(t, cl, web6, srvAddr6)

	// The clients, the paused one included, and the log.
	list := srv.cli("client", "list")
	if !strings.Contains(list, "phone") || !strings.Contains(list, "tablet") || !strings.Contains(list, "paused") {
		t.Errorf("client list after the upgrade:\n%s", list)
	}
	if events := srv.cli("events", "--limit", "100"); !strings.Contains(events, "client.added") || !strings.Contains(events, "phone") {
		t.Errorf("the older build's events are gone:\n%s", events)
	}
	if show := srv.cli("client", "show", "phone"); strings.Contains(show, "outdated") {
		t.Errorf("the upgrade flagged the client's config as outdated:\n%s", show)
	}

	// The admin: the browser that was logged in still is, the old password still works, and
	// it's the certificate the older build made.
	if err := apiMe(browser); err != nil {
		t.Errorf("the login from before the upgrade: %v", err)
	}
	if err := apiLogin(srv.adminClient(tp.netNS), "admin", password); err != nil {
		t.Errorf("logging in with the old password: %v", err)
	}
	if cert, err := os.ReadFile(certFile); err != nil || !bytes.Equal(cert, oldCert) {
		t.Errorf("the TLS certificate was replaced by the upgrade (%v)", err)
	}

	// The host reboots: the tunnel unit runs the new build, which brings the tunnel up from the
	// migrated database, and the client reconnects with the config it already had.
	srv.tunnel("down")
	srv.tunnel("up")
	wantSource(t, cl, web4, srvAddr4)
	wantSource(t, cl, web6, srvAddr6)
	after := kernelState(t, srv)
	if !reflect.DeepEqual(after.Peers, was.Peers) || after.PrivateKey != was.PrivateKey || after.ListenPort != was.ListenPort {
		t.Errorf("the tunnel came back different after the restart:\nbefore: %+v\nafter:  %+v", was, after)
	}
	// From schema 9 on, the older build recorded which config it handed out, and a config that
	// the new build renders differently would flag every client as outdated.
	if oldSchema >= 9 {
		if got := srv.cli("client", "config", "phone"); got != oldConfig {
			t.Errorf("the client's config reads differently after the upgrade:\nbefore:\n%s\nafter:\n%s", oldConfig, got)
		}
		if show := srv.cli("client", "show", "phone"); !strings.Contains(show, "current, last handed out") {
			t.Errorf("the config the older build handed out isn't current after the upgrade:\n%s", show)
		}
	}
}

// TestANewerDatabaseStopsTheDaemonNotTheTunnel is a downgrade (docs/PLAN.md §11): the host runs
// an older Drawbridge than the one that last used its database. The VPN matters more than the
// web UI, and the tunnel unit only reads the database, so it comes up and the client connects;
// the daemon, which writes, refuses, says why, exits with the status the unit won't restart on,
// and leaves the database as it was.
func TestANewerDatabaseStopsTheDaemonNotTheTunnel(t *testing.T) {
	tp := newTopology(t)
	startWeb(t, tp)
	srv := newServer(t, tp)
	srv.tunnel("up")
	srv.startDaemon()
	srv.cli("server", "set", "--endpoint", srvAddr4)
	srv.cli("client", "add", "phone")
	cfg := parseConfig(t, srv.cli("client", "config", "phone"))
	dev, err := srv.device()
	if err != nil {
		t.Fatal(err)
	}
	var vpn4, vpn6 netip.Prefix
	for _, a := range dev.Addrs {
		if a.Addr().Is4() {
			vpn4 = a.Masked()
		} else {
			vpn6 = a.Masked()
		}
	}
	cl := newClient(t, tp)
	cl.configure(cfg, vpn4, vpn6)
	wantSource(t, cl, web4, srvAddr4)
	srv.stopDaemon()

	newer := store.LatestSchema() + 1
	storetest.MakeNewer(t, srv.db, newer)
	before := storetest.Rows(t, srv.db)

	// The host reboots into the older build: the tunnel unit stops the tunnel and starts it.
	srv.tunnel("down")
	out, err := srv.inSrv("tunnel", "up", "--db", srv.db, "--secret-key", srv.secret)
	if err != nil {
		t.Fatalf("tunnel up on a newer database: %v\n%s", err, out)
	}
	if !strings.Contains(out, "newer Drawbridge") || !strings.Contains(out, fmt.Sprintf("schema=%d", newer)) {
		t.Errorf("tunnel up didn't say the database is newer:\n%s", out)
	}
	wantSource(t, cl, web4, srvAddr4)
	wantSource(t, cl, web6, srvAddr6)

	// Then the daemon starts, and won't.
	out, err = srv.inSrv("serve", "--db", srv.db, "--secret-key", srv.secret, "--control", srv.socket)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 78 {
		t.Fatalf("serve on a newer database: %v, want exit status 78\n%s", err, out)
	}
	for _, want := range []string{"from a newer Drawbridge", fmt.Sprintf("schema %d", newer), "restore a backup"} {
		if !strings.Contains(out, want) {
			t.Errorf("serve's message doesn't say %q:\n%s", want, out)
		}
	}

	// The refusal touched nothing: the VPN is up and the database is as the newer build left it.
	wantSource(t, cl, web4, srvAddr4)
	if got := storetest.Version(t, srv.db); got != newer {
		t.Errorf("the database is at schema %d, want it left at %d", got, newer)
	}
	if after := storetest.Rows(t, srv.db); !reflect.DeepEqual(after, before) {
		t.Error("the older build changed a database from a newer one")
	}
	if snaps, _ := snapshot.List(filepath.Join(filepath.Dir(srv.db), "backups")); len(snaps) != 0 {
		t.Errorf("a newer database was snapshotted: %v", snaps)
	}
}

// passwordIn finds the password `drawbridge admin create` printed.
func passwordIn(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "Password: "); ok {
			return strings.TrimSpace(p)
		}
	}
	t.Fatalf("no password in:\n%s", out)
	return ""
}

func apiLogin(c *http.Client, user, password string) error {
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, user, password)
	req, err := http.NewRequest(http.MethodPost, "https://"+net.JoinHostPort(srvAddr4, "51821")+"/api/auth/login", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Drawbridge", "1")
	return wantOK(c, req)
}

func apiMe(c *http.Client) error {
	req, err := http.NewRequest(http.MethodGet, "https://"+net.JoinHostPort(srvAddr4, "51821")+"/api/auth/me", nil)
	if err != nil {
		return err
	}
	return wantOK(c, req)
}

func wantOK(c *http.Client, req *http.Request) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, b)
	}
	return nil
}

// kernelPeer is what an upgrade must leave alone about a peer: not its counters or handshake.
type kernelPeer struct {
	Key, PSK string
	Allowed  string
}

// kernel is the server's wg0 as far as an upgrade is concerned.
type kernel struct {
	// Link is the interface's line from `ip link`, which starts with its index: a new
	// interface with the same name would have another.
	Link       string
	PrivateKey string
	ListenPort int
	MTU        int
	Addrs      string
	Peers      []kernelPeer
}

func kernelState(t *testing.T, s *server) kernel {
	t.Helper()
	dev, err := s.device()
	if err != nil {
		t.Fatal(err)
	}
	k := kernel{
		Link:       strings.Fields(run(t, "ip", "-n", s.tp.srv, "-o", "link", "show", "wg0"))[0],
		PrivateKey: dev.PrivateKey.String(), ListenPort: dev.ListenPort, MTU: dev.MTU,
		Addrs: fmt.Sprint(dev.Addrs),
	}
	for _, p := range dev.Peers {
		k.Peers = append(k.Peers, kernelPeer{Key: p.PublicKey.String(), PSK: p.PresharedKey.String(), Allowed: fmt.Sprint(p.AllowedIPs)})
	}
	return k
}

// probe fetches from the web host through the client's tunnel every 100 ms, from a goroutine of
// its own, and counts the fetches that fail.
type probe struct {
	stop, done chan struct{}
	mu         sync.Mutex
	attempts   int
	failures   int
	lastErr    error
}

func startProbe(tp *topology, host string) *probe {
	p := &probe{stop: make(chan struct{}), done: make(chan struct{})}
	c := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialIn(ctx, tp.cliNS, network, addr)
		},
	}}
	go func() {
		defer close(p.done)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-tick.C:
			}
			err := func() error {
				resp, err := c.Get("http://" + net.JoinHostPort(host, webPort) + "/")
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				_, err = io.Copy(io.Discard, resp.Body)
				return err
			}()
			p.mu.Lock()
			p.attempts++
			if err != nil {
				p.failures++
				p.lastErr = err
			}
			p.mu.Unlock()
		}
	}()
	return p
}

// end stops the probe and returns what it saw.
func (p *probe) end() (attempts, failures int, lastErr error) {
	close(p.stop)
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts, p.failures, p.lastErr
}

// dialIn connects from inside a network namespace. It's inNS for a goroutine that has no
// testing.T to fail, so the error comes back.
func dialIn(ctx context.Context, ns netns.NsHandle, network, addr string) (net.Conn, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	orig, err := netns.Get()
	if err != nil {
		return nil, err
	}
	defer orig.Close()
	if err := netns.Set(ns); err != nil {
		return nil, err
	}
	defer func() {
		if err := netns.Set(orig); err != nil {
			panic(fmt.Sprintf("can't return to the original network namespace: %v", err))
		}
	}()
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}
