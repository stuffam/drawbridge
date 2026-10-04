package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/tlscert"
)

// The web UI's TLS certificate (docs/PLAN.md §6.6): the self-signed one the daemon makes, or
// the admin's own, installed here. The daemon swaps it while it runs.

// ErrNoCertificates means the daemon has no certificate store to change.
var ErrNoCertificates = errors.New("this daemon can't change its TLS certificate")

// ErrNoUploadedCertificate means the admin asked to go back to the self-signed certificate
// when it's the one in use.
var ErrNoUploadedCertificate = errors.New("the web UI is already using its self-signed certificate")

// CertificateInfo is the certificate in use, and what the admin should know about it.
type CertificateInfo struct {
	tlscert.Info
	// Notes are warnings that aren't reasons to refuse it: it expires soon, it covers none of
	// the host's names, or it lacks its intermediate certificates. Only an uploaded certificate
	// has any; the self-signed one is made for this host and renewed by the daemon.
	Notes []string
}

// Certificate describes the certificate the web UI is serving.
func (s *Service) Certificate(ctx context.Context) (CertificateInfo, error) {
	if s.TLS == nil {
		return CertificateInfo{}, ErrNoCertificates
	}
	info := CertificateInfo{Info: s.TLS.Info()}
	if info.Source == tlscert.Uploaded {
		info.Notes = s.TLS.Notes(s.certificateHosts(ctx))
	}
	return info, nil
}

// certificateHosts are the names and addresses the web UI is reached by: the host's own, and the
// endpoint clients connect to, which is often the same name.
func (s *Service) certificateHosts(ctx context.Context) []string {
	hosts := s.TLS.HostNames()
	if st, err := s.Store.Settings(ctx); err == nil && st.EndpointHost != "" {
		hosts = append(hosts, st.EndpointHost)
	}
	return hosts
}

// SetupFingerprint is the fingerprint the admin should check a browser's warning against when
// the web UI is first opened. It's empty when the certificate in use isn't the self-signed one:
// an installed certificate is the admin's own, and a browser that trusts it doesn't warn.
func (s *Service) SetupFingerprint() string {
	if s.TLS == nil || s.TLS.Info().Source != tlscert.SelfSigned {
		return ""
	}
	return s.TLS.Fingerprint()
}

// InstallCertificate makes the given certificate chain and private key (PEM) the web UI's. Both
// are checked first (tlscert.Parse), and nothing changes when either is refused. New connections
// use it at once; the daemon needn't restart. It's an event, with the fingerprints and names,
// and never the key.
func (s *Service) InstallCertificate(ctx context.Context, certPEM, keyPEM string) (CertificateInfo, error) {
	if s.TLS == nil {
		return CertificateInfo{}, ErrNoCertificates
	}
	from, to, err := s.TLS.Install([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		var bad *tlscert.InvalidError
		if errors.As(err, &bad) {
			return CertificateInfo{}, &model.InvalidError{Err: err}
		}
		return CertificateInfo{}, err
	}
	s.record(ctx, Event{Kind: "tls.certificate_installed", Data: map[string]string{
		"fingerprint": from.Fingerprint + " → " + to.Fingerprint,
		"names":       strings.Join(to.Names, ", "),
		"expires":     to.NotAfter.Format(time.DateOnly),
	}})
	return s.Certificate(ctx)
}

// InstallCertificateFor is InstallCertificate for a logged-in account, the web UI's. It takes the
// account's password again: a hijacked session that could install a certificate could put one
// whose key it holds in front of the admin's next login. A wrong password counts against the
// same limits as a login. The pair is read first, so a mistake in it costs the account nothing.
func (s *Service) InstallCertificateFor(ctx context.Context, u store.User, password, certPEM, keyPEM string) (CertificateInfo, error) {
	if s.TLS == nil {
		return CertificateInfo{}, ErrNoCertificates
	}
	if err := s.TLS.Check([]byte(certPEM), []byte(keyPEM)); err != nil {
		var bad *tlscert.InvalidError
		if errors.As(err, &bad) {
			return CertificateInfo{}, &model.InvalidError{Err: err}
		}
		return CertificateInfo{}, err
	}
	if err := s.confirmPassword(ctx, u, password, "auth.certificate_failed"); err != nil {
		return CertificateInfo{}, err
	}
	return s.InstallCertificate(ctx, certPEM, keyPEM)
}

// ResetCertificate goes back to the self-signed certificate and forgets the installed one. It
// returns ErrNoUploadedCertificate when there is none to leave. It's an event.
func (s *Service) ResetCertificate(ctx context.Context) (CertificateInfo, error) {
	if s.TLS == nil {
		return CertificateInfo{}, ErrNoCertificates
	}
	from, to, err := s.TLS.Reset()
	if errors.Is(err, tlscert.ErrNotUploaded) {
		return CertificateInfo{}, ErrNoUploadedCertificate
	}
	if err != nil {
		return CertificateInfo{}, err
	}
	s.record(ctx, Event{Kind: "tls.certificate_reset", Data: map[string]string{
		"fingerprint": from.Fingerprint + " → " + to.Fingerprint,
	}})
	return s.Certificate(ctx)
}
