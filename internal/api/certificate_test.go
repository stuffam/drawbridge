package api

import (
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/service"
	"github.com/stuffam/drawbridge/internal/tlscert"
	"github.com/stuffam/drawbridge/internal/tlscert/tlscerttest"
	"github.com/stuffam/drawbridge/internal/views"
)

func withCertificates(t *testing.T, svc *service.Service) *tlscert.Store {
	t.Helper()
	certs, err := tlscert.Open(filepath.Join(t.TempDir(), "tls"), tlscert.Options{
		Names: func() tlscert.Names { return tlscert.DefaultNames("server", netip.MustParseAddr("192.168.4.10")) },
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.TLS = certs
	return certs
}

func TestCertificateNeedsALogin(t *testing.T) {
	srv := newServer(t, newService(t))
	b := newBrowser(t, srv)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		b.expect(http.StatusUnauthorized, method, "/api/system/certificate", nil)
	}
}

// A daemon with no certificate store says so, rather than pretending.
func TestCertificateWithoutAStore(t *testing.T) {
	svc := newService(t)
	b, password := loggedIn(t, svc, newServer(t, svc))
	for _, tc := range []struct {
		method string
		body   any
	}{
		{"GET", nil},
		{"PUT", views.CertificateInstallRequest{Password: password, Certificate: "a", PrivateKey: "b"}},
		{"DELETE", nil},
	} {
		r := b.expect(http.StatusNotImplemented, tc.method, "/api/system/certificate", tc.body)
		if !strings.Contains(r.errorText(), "TLS certificate") {
			t.Errorf("%s: error %q doesn't say what's missing", tc.method, r.errorText())
		}
	}
}

// The whole round: look, install with the password, look again, and go back. The key goes in and
// is never sent back, and everything that changes is in the event log.
func TestCertificateOverTheAPI(t *testing.T) {
	svc := newService(t)
	withCertificates(t, svc)
	srv := newServer(t, svc)
	b, password := loggedIn(t, svc, srv)

	var self views.Certificate
	b.expect(http.StatusOK, "GET", "/api/system/certificate", nil).decode(t, &self)
	if self.Source != "self-signed" || self.Fingerprint == "" || len(self.Names) == 0 || self.Notes == nil || len(self.Notes) != 0 {
		t.Fatalf("self-signed: %+v", self)
	}
	b.expect(http.StatusConflict, "DELETE", "/api/system/certificate", nil) // nothing to go back from

	mine := tlscerttest.New(t, time.Now(), "vpn.example.com", "192.168.4.10")
	req := views.CertificateInstallRequest{Password: password, Certificate: mine.Cert, PrivateKey: mine.Key}

	// A wrong password installs nothing.
	wrong := req
	wrong.Password = "not the password"
	b.expect(http.StatusBadRequest, "PUT", "/api/system/certificate", wrong)
	var still views.Certificate
	b.expect(http.StatusOK, "GET", "/api/system/certificate", nil).decode(t, &still)
	if still.Fingerprint != self.Fingerprint {
		t.Fatal("a wrong password installed the certificate")
	}

	// A pair that doesn't belong together is the caller's mistake, and the answer says so.
	other := tlscerttest.New(t, time.Now(), "vpn.example.com")
	mismatch := req
	mismatch.PrivateKey = other.Key
	r := b.expect(http.StatusBadRequest, "PUT", "/api/system/certificate", mismatch)
	if !strings.Contains(r.errorText(), "doesn't belong to the first certificate") {
		t.Errorf("error %q", r.errorText())
	}

	var got views.Certificate
	r = b.expect(http.StatusOK, "PUT", "/api/system/certificate", req)
	r.decode(t, &got)
	if got.Source != "uploaded" || got.Fingerprint == self.Fingerprint || len(got.Names) != 2 || got.Names[0] != "vpn.example.com" || got.Chain != 1 || !got.SelfIssued {
		t.Fatalf("installed: %+v", got)
	}
	if strings.Contains(string(r.body), "PRIVATE") || strings.Contains(strings.ToLower(string(r.body)), "private_key") {
		t.Fatalf("the response carries the key: %s", r.body)
	}
	b.expect(http.StatusOK, "GET", "/api/system/certificate", nil).decode(t, &still)
	if still.Fingerprint != got.Fingerprint {
		t.Errorf("GET after PUT: %+v", still)
	}

	var back views.Certificate
	b.expect(http.StatusOK, "DELETE", "/api/system/certificate", nil).decode(t, &back)
	if back.Source != "self-signed" || back.Fingerprint != self.Fingerprint {
		t.Errorf("reset: %+v", back)
	}

	var events []views.EventView
	r = b.expect(http.StatusOK, "GET", "/api/events?limit=50", nil)
	r.decode(t, &events)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	for _, want := range []string{"auth.certificate_failed", "tls.certificate_installed", "tls.certificate_reset"} {
		if !strings.Contains(strings.Join(kinds, " "), want) {
			t.Errorf("no %s event in %v", want, kinds)
		}
	}
	if strings.Contains(string(r.body), "PRIVATE") {
		t.Fatalf("the event log carries the key: %s", r.body)
	}
}

// A request that needs a body and doesn't have one is refused, and a field the API doesn't know
// is too, so a typo can't install nothing quietly.
func TestCertificateInstallRefusals(t *testing.T) {
	svc := newService(t)
	withCertificates(t, svc)
	b, _ := loggedIn(t, svc, newServer(t, svc))
	b.expect(http.StatusBadRequest, "PUT", "/api/system/certificate", map[string]string{"cert": "x"})
	b.expect(http.StatusBadRequest, "PUT", "/api/system/certificate", views.CertificateInstallRequest{})
}
