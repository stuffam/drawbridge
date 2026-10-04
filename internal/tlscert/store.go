package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Where a certificate came from.
const (
	SelfSigned = "self-signed"
	Uploaded   = "uploaded"
)

// uploadedFile holds the certificate the admin installed: its chain, then its key, in one file
// (mode 0600), so replacing it is one rename and a crash can't leave a certificate beside the
// wrong key. It's in the same directory as the self-signed pair, which stays there as the way
// back.
const uploadedFile = "uploaded.pem"

// ErrNotUploaded means Reset was asked to go back to the self-signed certificate when there's
// no uploaded one to leave.
var ErrNotUploaded = errors.New("the web UI is already using its self-signed certificate")

// Options are what a Store needs besides its directory.
type Options struct {
	// Names are what a new self-signed certificate is valid for. It's called whenever one is
	// made, so it can read the host's addresses as they are then.
	Names func() Names
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Log gets what the daemon should say about the certificate; nil means nothing.
	Log *slog.Logger
}

// Store holds the certificate the web UI serves, and replaces it while the daemon runs: the
// TLS configuration asks it for the certificate on every handshake, so a new one is used by the
// next connection with no restart. It starts with the admin's uploaded certificate, if there
// is one, and otherwise the self-signed one it makes.
type Store struct {
	dir  string
	opts Options
	// mu serializes Install and Reset, which write files.
	mu  sync.Mutex
	cur atomic.Pointer[serving]
}

// serving is the certificate in use, and where it came from.
type serving struct {
	cert   tls.Certificate
	source string
}

// Open loads the web UI's certificate from dir. The self-signed certificate is made if it's
// missing, damaged, or about to expire, so there's always one to go back to. An uploaded
// certificate is used instead whenever it loads; it stays in use after it expires, and a
// pair that can't be loaded at all is reported and left alone, with the self-signed one
// serving meanwhile.
func Open(dir string, opts Options) (*Store, error) {
	if opts.Names == nil {
		opts.Names = func() Names { return DefaultNames("") }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Store{dir: dir, opts: opts}
	self, created, err := Ensure(dir, opts.Names(), opts.Now())
	if err != nil {
		return nil, err
	}
	if created {
		s.info("created a self-signed TLS certificate for the web UI", "dir", dir)
	}
	cur := &serving{cert: self, source: SelfSigned}
	switch up, err := loadUploaded(dir); {
	case err == nil:
		cur = &serving{cert: up, source: Uploaded}
	case !errors.Is(err, os.ErrNotExist):
		if s.opts.Log != nil {
			s.opts.Log.Error("can't use the uploaded TLS certificate, so the web UI serves its self-signed one; "+
				"install a certificate again, or run: drawbridge tls reset", "err", err)
		}
	}
	s.cur.Store(cur)
	s.info("web UI TLS certificate", "source", cur.source, "sha256", Fingerprint(cur.cert),
		"expires", cur.cert.Leaf.NotAfter.Format(time.DateOnly))
	return s, nil
}

func (s *Store) info(msg string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log.Info(msg, args...)
	}
}

func loadUploaded(dir string) (tls.Certificate, error) {
	data, err := os.ReadFile(filepath.Join(dir, uploadedFile)) //nolint:gosec // G304: a fixed name in the TLS directory.
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, _, err := parsePair(data, data)
	return cert, err
}

// GetCertificate is tls.Config's GetCertificate: the certificate in use now.
func (s *Store) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return &s.cur.Load().cert, nil
}

// Config returns the TLS configuration for the web UI.
func (s *Store) Config() *tls.Config {
	return &tls.Config{GetCertificate: s.GetCertificate, MinVersion: tls.VersionTLS12}
}

// Info describes a certificate for the admin.
type Info struct {
	// Source is SelfSigned or Uploaded.
	Source string
	// Subject and Issuer are the distinguished names, such as "CN=vpn.example.com".
	Subject, Issuer string
	// Names are what the certificate is valid for: its DNS names, then its addresses.
	Names               []string
	NotBefore, NotAfter time.Time
	// Fingerprint is the SHA-256 fingerprint as browsers show it.
	Fingerprint string
	// Chain is how many certificates the file holds, the server's own included.
	Chain int
	// SelfIssued is whether the certificate signed itself, so no CA vouches for it.
	SelfIssued bool
}

// Describe returns what the admin should be told about a certificate.
func Describe(c tls.Certificate, source string) Info {
	leaf := c.Leaf
	info := Info{
		Source:      source,
		Subject:     leaf.Subject.String(),
		Issuer:      leaf.Issuer.String(),
		NotBefore:   leaf.NotBefore,
		NotAfter:    leaf.NotAfter,
		Fingerprint: Fingerprint(c),
		Chain:       len(c.Certificate),
		SelfIssued:  string(leaf.RawIssuer) == string(leaf.RawSubject),
	}
	info.Names = append(info.Names, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		info.Names = append(info.Names, ip.String())
	}
	return info
}

// Info describes the certificate in use.
func (s *Store) Info() Info {
	c := s.cur.Load()
	return Describe(c.cert, c.source)
}

// Fingerprint is the SHA-256 fingerprint of the certificate in use.
func (s *Store) Fingerprint() string { return Fingerprint(s.cur.Load().cert) }

// HostNames are the names and addresses this host answers to, which an uploaded certificate
// should cover for a browser to be happy: the hostname and its .local form, and the host's
// addresses. Loopback isn't one of them.
func (s *Store) HostNames() []string {
	n := s.opts.Names()
	var out []string
	for _, d := range n.DNS {
		if d != "localhost" {
			out = append(out, d)
		}
	}
	for _, a := range n.IPs {
		if !a.IsLoopback() {
			out = append(out, a.String())
		}
	}
	return out
}

// Check reads a certificate and key as Install would, and changes nothing. It's for refusing a
// mistake before anything costly is asked of the admin.
func (s *Store) Check(certPEM, keyPEM []byte) error {
	_, _, err := Parse(certPEM, keyPEM, s.opts.Now())
	return err
}

// Install makes an uploaded certificate the one in use, replacing any earlier upload, and
// returns what was in use and what is now. The pair is checked first (Parse), and nothing
// changes if it's refused. It's saved before it's served, so a restart finds it.
func (s *Store) Install(certPEM, keyPEM []byte) (from, to Info, err error) {
	cert, stored, err := Parse(certPEM, keyPEM, s.opts.Now())
	if err != nil {
		return Info{}, Info{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	from = s.Info()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Info{}, Info{}, fmt.Errorf("creating the TLS directory: %w", err)
	}
	if err := writeFile(filepath.Join(s.dir, uploadedFile), stored, 0o600); err != nil {
		return Info{}, Info{}, err
	}
	s.cur.Store(&serving{cert: cert, source: Uploaded})
	return from, Describe(cert, Uploaded), nil
}

// Reset goes back to the self-signed certificate and forgets the uploaded one. The self-signed
// one is made again first if it has run out, or the host's names have moved on, so there's
// always one to go back to. It returns ErrNotUploaded when there's nothing to leave.
func (s *Store) Reset() (from, to Info, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, uploadedFile)
	_, statErr := os.Stat(path)
	from = s.Info()
	if from.Source != Uploaded && errors.Is(statErr, os.ErrNotExist) {
		return Info{}, Info{}, ErrNotUploaded
	}
	self, _, err := Ensure(s.dir, s.opts.Names(), s.opts.Now())
	if err != nil {
		return Info{}, Info{}, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Info{}, Info{}, fmt.Errorf("removing the uploaded certificate: %w", err)
	}
	s.cur.Store(&serving{cert: self, source: SelfSigned})
	return from, Describe(self, SelfSigned), nil
}

// expiringSoon is how close to its end an uploaded certificate gets a warning. It matches
// RenewBefore, which is how close a self-signed one is replaced.
const expiringSoon = RenewBefore

// Notes returns what the admin should know about serving the certificate in use that isn't a
// reason to refuse it: see the function of the same name.
func (s *Store) Notes(hosts []string) []string {
	return Notes(s.cur.Load().cert, hosts, s.opts.Now())
}

// Notes returns what the admin should know about serving c that isn't a reason to refuse it:
// it expires soon (or has), it covers none of the names the host is reached by, or it comes
// without the intermediate certificates its issuer's signature needs. hosts are those names
// and addresses.
func Notes(c tls.Certificate, hosts []string, now time.Time) []string {
	leaf := c.Leaf
	var notes []string
	switch left := leaf.NotAfter.Sub(now); {
	case left <= 0:
		notes = append(notes, fmt.Sprintf("The certificate expired on %s, so browsers refuse it. Install a renewed one, or go back to the self-signed one.", leaf.NotAfter.Format(time.DateOnly)))
	case left < expiringSoon:
		notes = append(notes, fmt.Sprintf("The certificate expires on %s (%d days). Nothing renews an uploaded certificate: install a new one before then.", leaf.NotAfter.Format(time.DateOnly), int(left/(24*time.Hour))))
	}
	if len(hosts) > 0 && !coversAny(leaf, hosts) {
		notes = append(notes, "The certificate covers none of the names or addresses of this host, so a browser warns about it unless the web UI is opened by one of the names it does cover.")
	}
	if len(c.Certificate) == 1 && string(leaf.RawIssuer) != string(leaf.RawSubject) {
		notes = append(notes, "The file holds only the server's own certificate. Some browsers and apps need the issuer's intermediate certificates too (a \"full chain\" file has them).")
	}
	return notes
}

func coversAny(leaf *x509.Certificate, hosts []string) bool {
	for _, h := range hosts {
		// VerifyHostname reads an address as an address, and matches wildcard names.
		if leaf.VerifyHostname(h) == nil {
			return true
		}
	}
	return false
}
