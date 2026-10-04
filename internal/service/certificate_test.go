package service

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/tlscert"
	"github.com/stuffam/drawbridge/internal/tlscert/tlscerttest"
)

// withCertificates gives the service a certificate store in a new directory, and returns the
// store, which is the daemon's own.
func withCertificates(t *testing.T, s *Service, clk *clock) *tlscert.Store {
	t.Helper()
	certs, err := tlscert.Open(filepath.Join(t.TempDir(), "tls"), tlscert.Options{
		Names: func() tlscert.Names { return tlscert.DefaultNames("server", netip.MustParseAddr("192.168.4.10")) },
		Now:   clk.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.TLS = certs
	return certs
}

func certEvents(t *testing.T, s *Service, kind string) []store.Event {
	t.Helper()
	events, err := s.Events(context.Background(), store.EventFilter{Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// The effect of installing a certificate is on what a new connection is shown, so that's what
// this checks; then the record of it, which carries fingerprints and names and never the key.
func TestInstallCertificate(t *testing.T) {
	s, clk := newTestService(t)
	certs := withCertificates(t, s, clk)
	ctx := web(context.Background(), "admin")
	self := tlscerttest.Shown(t, certs.Config())
	selfFingerprint := s.TLS.Fingerprint()

	mine := tlscerttest.New(t, clk.now(), "vpn.example.com", "192.168.4.10")
	info, err := s.InstallCertificate(ctx, mine.Cert, mine.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got := tlscerttest.Shown(t, certs.Config()); !bytes.Equal(got, mine.DER) || bytes.Equal(got, self) {
		t.Fatal("a new connection isn't shown the installed certificate")
	}
	if info.Source != tlscert.Uploaded || info.Fingerprint == selfFingerprint || !slices.Equal(info.Names, []string{"vpn.example.com", "192.168.4.10"}) {
		t.Errorf("info %+v", info)
	}
	// It covers the host's address, and signed itself, so there is nothing to warn about.
	if len(info.Notes) != 0 {
		t.Errorf("notes %v", info.Notes)
	}

	events := certEvents(t, s, "tls.certificate_installed")
	if len(events) != 1 {
		t.Fatalf("events %+v", events)
	}
	e := events[0]
	if e.Actor != "admin" || e.Via != ViaWeb || e.Category != CategoryAdmin ||
		e.Data["fingerprint"] != selfFingerprint+" → "+info.Fingerprint ||
		e.Data["names"] != "vpn.example.com, 192.168.4.10" || e.Data["expires"] != "2026-12-25" {
		t.Errorf("event %+v", e)
	}
	body := mine.Key[strings.Index(mine.Key, "\n")+1 : strings.LastIndex(strings.TrimSpace(mine.Key), "\n")]
	for _, v := range e.Data {
		if strings.Contains(v, strings.TrimSpace(body)) || strings.Contains(v, "PRIVATE KEY") {
			t.Fatalf("the event carries the key: %q", v)
		}
	}

	// Reading it back shows the same, and the setup token has no fingerprint to check any more.
	got, err := s.Certificate(ctx)
	if err != nil || got.Fingerprint != info.Fingerprint || got.Source != tlscert.Uploaded {
		t.Errorf("Certificate = %+v, %v", got, err)
	}
	if fp := s.SetupFingerprint(); fp != "" {
		t.Errorf("SetupFingerprint = %q for a certificate the admin installed", fp)
	}
}

// A pair that can't be used is refused as the caller's mistake, changes nothing, and isn't an
// event: there is nothing to record that happened.
func TestInstallCertificateRefusals(t *testing.T) {
	s, clk := newTestService(t)
	certs := withCertificates(t, s, clk)
	ctx := web(context.Background(), "admin")
	before := certs.Fingerprint()
	a := tlscerttest.New(t, clk.now(), "vpn.example.com")
	b := tlscerttest.New(t, clk.now(), "vpn.example.com")

	for name, pair := range map[string][2]string{
		"the wrong key": {a.Cert, b.Key},
		"no key":        {a.Cert, ""},
		"nothing":       {"", ""},
		"text":          {"hello", "world"},
	} {
		_, err := s.InstallCertificate(ctx, pair[0], pair[1])
		if !model.IsInvalid(err) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	if certs.Fingerprint() != before || certs.Info().Source != tlscert.SelfSigned {
		t.Error("a refused certificate changed what is served")
	}
	if got := certEvents(t, s, "tls.certificate_installed"); len(got) != 0 {
		t.Errorf("a refusal was recorded: %+v", got)
	}
}

// The web's install takes the account's password again, because a session alone (a hijacked
// one) mustn't be able to put a certificate whose key it holds in front of the admin's next
// login. A wrong one installs nothing, is an event of its own, and counts against the login
// limits; a mistake in the pair is found first and costs the account nothing.
func TestInstallCertificateForNeedsThePassword(t *testing.T) {
	s, clk, ctx, login, password := tokenEnv(t)
	certs := withCertificates(t, s, clk)
	mine := tlscerttest.New(t, clk.now(), "vpn.example.com")
	before := certs.Fingerprint()

	for _, wrong := range []string{"not the password", ""} {
		_, err := s.InstallCertificateFor(ctx, login.User, wrong, mine.Cert, mine.Key)
		if !errors.Is(err, ErrWrongPassword) || !model.IsInvalid(err) {
			t.Fatalf("password %q: err = %v", wrong, err)
		}
	}
	if certs.Fingerprint() != before {
		t.Fatal("a wrong password installed the certificate")
	}
	got := kinds(t, s)
	if got[len(got)-1] != "auth.certificate_failed" || slices.Contains(got, "tls.certificate_installed") {
		t.Errorf("events %v: want the failure, and no install", got)
	}

	// A broken pair is refused before the password is asked, so it costs none of the attempts.
	for range 10 {
		_, err := s.InstallCertificateFor(ctx, login.User, "wrong", "hello", "world")
		var inv *tlscert.InvalidError
		if !model.IsInvalid(err) || errors.Is(err, ErrWrongPassword) || !errors.As(errors.Unwrap(err), &inv) {
			t.Fatalf("a broken pair: err = %v", err)
		}
	}
	if n := len(certEvents(t, s, "auth.certificate_failed")); n != 2 {
		t.Errorf("%d failures recorded, want only the 2 wrong passwords", n)
	}

	if _, err := s.InstallCertificateFor(ctx, login.User, password, mine.Cert, mine.Key); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tlscerttest.Shown(t, certs.Config()), mine.DER) {
		t.Error("the right password didn't install the certificate")
	}

	// Five wrong passwords in all are free; after the sixth the right one waits too.
	for range 6 {
		_, _ = s.InstallCertificateFor(ctx, login.User, "wrong", mine.Cert, mine.Key)
	}
	var limited *RateLimitedError
	if _, err := s.InstallCertificateFor(ctx, login.User, password, mine.Cert, mine.Key); !errors.As(err, &limited) {
		t.Errorf("after many wrong passwords: err = %v, want rate limited", err)
	}
}

// Going back is an event, puts the self-signed certificate in front of the next connection, and
// says so when there's nothing to go back from.
func TestResetCertificate(t *testing.T) {
	s, clk := newTestService(t)
	certs := withCertificates(t, s, clk)
	ctx := web(context.Background(), "admin")
	self := tlscerttest.Shown(t, certs.Config())
	selfFingerprint := certs.Fingerprint()

	if _, err := s.ResetCertificate(ctx); !errors.Is(err, ErrNoUploadedCertificate) {
		t.Fatalf("resetting the self-signed certificate: err = %v", err)
	}
	if got := certEvents(t, s, "tls.certificate_reset"); len(got) != 0 {
		t.Errorf("a no-op was recorded: %+v", got)
	}

	mine := tlscerttest.New(t, clk.now(), "vpn.example.com")
	info, err := s.InstallCertificate(ctx, mine.Cert, mine.Key)
	if err != nil {
		t.Fatal(err)
	}
	back, err := s.ResetCertificate(ctx)
	if err != nil || back.Source != tlscert.SelfSigned || back.Fingerprint != selfFingerprint {
		t.Fatalf("reset: %+v, %v", back, err)
	}
	if !bytes.Equal(tlscerttest.Shown(t, certs.Config()), self) {
		t.Error("a new connection isn't shown the self-signed certificate")
	}
	events := certEvents(t, s, "tls.certificate_reset")
	if len(events) != 1 || events[0].Data["fingerprint"] != info.Fingerprint+" → "+selfFingerprint {
		t.Errorf("events %+v", events)
	}
	if s.SetupFingerprint() != selfFingerprint {
		t.Error("the setup token doesn't show the self-signed certificate's fingerprint again")
	}
}

func TestCertificatesWithoutAStore(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	if _, err := s.Certificate(ctx); !errors.Is(err, ErrNoCertificates) {
		t.Errorf("Certificate: %v", err)
	}
	if _, err := s.InstallCertificate(ctx, "a", "b"); !errors.Is(err, ErrNoCertificates) {
		t.Errorf("InstallCertificate: %v", err)
	}
	if _, err := s.ResetCertificate(ctx); !errors.Is(err, ErrNoCertificates) {
		t.Errorf("ResetCertificate: %v", err)
	}
	if s.SetupFingerprint() != "" {
		t.Error("a fingerprint without a store")
	}
}

// The notes compare the certificate with the names the host answers to, and the endpoint.
func TestCertificateNotesKnowTheEndpoint(t *testing.T) {
	s, clk := newTestService(t)
	withCertificates(t, s, clk)
	ctx := web(context.Background(), "admin")
	mine := tlscerttest.New(t, clk.now(), "vpn.example.com")
	info, err := s.InstallCertificate(ctx, mine.Cert, mine.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Notes) != 1 || !strings.Contains(info.Notes[0], "covers none of the names") {
		t.Fatalf("notes %v: the host is server, and server.local, and 192.168.4.10", info.Notes)
	}
	withEndpoint(t, s, "vpn.example.com")
	info, err = s.Certificate(ctx)
	if err != nil || len(info.Notes) != 0 {
		t.Errorf("with the certificate's name as the endpoint: notes %v, %v", info.Notes, err)
	}
}

// The doctor reads the certificate in use now, not the one the daemon started with.
func TestDiagnoseFollowsTheCertificate(t *testing.T) {
	s, clk := newTestService(t)
	withCertificates(t, s, clk)
	s.Diag = diagHost()
	s.Diag.Now = clk.now // the doctor's clock moves with the test's
	ctx := web(context.Background(), "admin")

	tlsCheck := func() (string, string) {
		t.Helper()
		checks, err := s.Diagnose(ctx)
		if err != nil {
			t.Fatal(err)
		}
		c := byID(t, checks, "tls-certificate")
		return string(c.Status), c.Detail
	}
	if _, detail := tlsCheck(); !strings.Contains(detail, "Valid until") {
		t.Errorf("self-signed: %q", detail)
	}
	mine := tlscerttest.New(t, clk.now(), "vpn.example.com")
	if _, err := s.InstallCertificate(ctx, mine.Cert, mine.Key); err != nil {
		t.Fatal(err)
	}
	status, detail := tlsCheck()
	if status != "pass" || !strings.Contains(detail, "uploaded certificate is valid until 2026-12-25") {
		t.Errorf("uploaded: %s, %q", status, detail)
	}
	clk.advance(80 * 24 * time.Hour)
	if status, detail = tlsCheck(); status != "warn" || !strings.Contains(detail, "uploaded certificate expires on 2026-12-25") {
		t.Errorf("uploaded, nearly out: %s, %q", status, detail)
	}
}
