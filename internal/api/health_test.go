package api

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stuffam/drawbridge/internal/diag"
	"github.com/stuffam/drawbridge/internal/views"
)

func TestHealthNeedsALogin(t *testing.T) {
	srv := newServer(t, newService(t))
	newBrowser(t, srv).expect(http.StatusUnauthorized, "GET", "/api/system/health", nil)
}

// A daemon with no host to read says so, rather than reporting a clean bill of health.
func TestHealthWithoutAHost(t *testing.T) {
	svc := newService(t)
	srv := newServer(t, svc)
	b, _ := loggedIn(t, svc, srv)
	r := b.expect(http.StatusNotImplemented, "GET", "/api/system/health", nil)
	if !strings.Contains(r.errorText(), "diagnostics") {
		t.Errorf("error %q doesn't say what's missing", r.errorText())
	}
}

// The page shows what `drawbridge doctor` would: the same checks, in order, with a hint on
// the ones that fail. It reads the host, so the result follows the host, and it records
// nothing in the event log.
func TestHealthReportsTheHost(t *testing.T) {
	svc := newService(t)
	host := diag.Host{FS: fstest.MapFS{
		"proc/sys/net/ipv4/ip_forward":          {Data: []byte("0\n")},
		"proc/sys/net/ipv6/conf/all/forwarding": {Data: []byte("1\n")},
	}}
	svc.Diag = &host
	srv := newServer(t, svc)
	b, _ := loggedIn(t, svc, srv)

	var before, after []views.EventView
	b.expect(http.StatusOK, "GET", "/api/events?limit=200", nil).decode(t, &before)

	var d views.Diagnostics
	b.expect(http.StatusOK, "GET", "/api/system/health", nil).decode(t, &d)
	if len(d.Checks) == 0 || d.Checks[0].ID != "tunnel" {
		t.Fatalf("checks %+v, want the doctor's, tunnel first", d.Checks)
	}
	var fwd *views.DiagnosticCheck
	for i, c := range d.Checks {
		if c.ID == "forwarding" {
			fwd = &d.Checks[i]
		}
		if c.Status == "" || c.Name == "" || c.Detail == "" {
			t.Errorf("check %+v is missing its status, name, or detail", c)
		}
	}
	if fwd == nil || fwd.Status != "fail" || !strings.Contains(fwd.Hint, "net.ipv4.ip_forward=1") {
		t.Fatalf("forwarding check %+v, want a failure that says how to turn IPv4 forwarding on", fwd)
	}

	// The admin fixes it, and the next run sees that.
	host.FS.(fstest.MapFS)["proc/sys/net/ipv4/ip_forward"] = &fstest.MapFile{Data: []byte("1\n")}
	b.expect(http.StatusOK, "GET", "/api/system/health", nil).decode(t, &d)
	for _, c := range d.Checks {
		if c.ID == "forwarding" && c.Status != "pass" {
			t.Errorf("after the fix, forwarding is %q: %s", c.Status, c.Detail)
		}
	}

	b.expect(http.StatusOK, "GET", "/api/events?limit=200", nil).decode(t, &after)
	if len(after) != len(before) {
		t.Errorf("%d events became %d: a read-only report recorded something", len(before), len(after))
	}
}
