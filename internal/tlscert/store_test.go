package tlscert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// pair is a certificate and its key, as the PEM an admin would upload.
type pair struct {
	cert, key []byte
	der       []byte
}

type spec struct {
	dns      []string
	ips      []net.IP
	notAfter time.Time
	usage    []x509.ExtKeyUsage
	// signer, when set, issues the certificate; otherwise it signs itself.
	signer  *x509.Certificate
	signKey any
	key     any
	isCA    bool
}

// issue makes a certificate with a fresh ECDSA key unless the spec brings its own.
func issue(t *testing.T, sp spec) pair {
	t.Helper()
	key := sp.key
	if key == nil {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key = k
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	if sp.notAfter.IsZero() {
		sp.notAfter = now.Add(90 * 24 * time.Hour)
	}
	if sp.usage == nil {
		sp.usage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	cn := "test"
	if len(sp.dns) > 0 {
		cn = sp.dns[0] // distinct, so a certificate and the CA that issued it have different names
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    now.Add(-24 * time.Hour),
		NotAfter:     sp.notAfter,
		DNSNames:     sp.dns,
		IPAddresses:  sp.ips,
		ExtKeyUsage:  sp.usage,
		IsCA:         sp.isCA, BasicConstraintsValid: sp.isCA,
	}
	parent, signKey := tmpl, key
	if sp.signer != nil {
		parent, signKey = sp.signer, sp.signKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, public(key), signKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pair{
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		der:  der,
	}
}

func public(key any) any {
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	case *rsa.PrivateKey:
		return &k.PublicKey
	case ed25519.PrivateKey:
		return k.Public()
	}
	panic("unknown key")
}

func vpn() spec { return spec{dns: []string{"vpn.example.com"}} }

func TestParseAcceptsWhatBrowsersCanUse(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	for name, sp := range map[string]spec{
		"ecdsa":       vpn(),
		"rsa":         {dns: []string{"vpn.example.com"}, key: rsaKey},
		"ed25519":     {dns: []string{"vpn.example.com"}, key: edKey},
		"an address":  {ips: []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("2001:db8::10")}},
		"a wildcard":  {dns: []string{"*.example.com"}},
		"no key use":  {dns: []string{"vpn.example.com"}, usage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}},
		"both client": {dns: []string{"vpn.example.com"}, usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}},
	} {
		t.Run(name, func(t *testing.T) {
			p := issue(t, sp)
			cert, stored, err := Parse(p.cert, p.key, now)
			if err != nil {
				t.Fatal(err)
			}
			if cert.Leaf == nil || !bytes.Equal(cert.Leaf.Raw, p.der) {
				t.Error("the parsed certificate isn't the one supplied")
			}
			// What's saved loads again as the same pair.
			again, _, err := parsePair(stored, stored)
			if err != nil || Fingerprint(again) != Fingerprint(cert) {
				t.Errorf("reloading the saved pair: %v", err)
			}
		})
	}
}

func TestParseKeyFormats(t *testing.T) {
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecDER, _ := x509.MarshalECPrivateKey(ecKey)
	for name, tc := range map[string]struct {
		sp  spec
		key *pem.Block
	}{
		"SEC1":  {spec{dns: []string{"a.example.com"}, key: ecKey}, &pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER}},
		"PKCS1": {spec{dns: []string{"a.example.com"}, key: rsaKey}, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}},
	} {
		t.Run(name, func(t *testing.T) {
			p := issue(t, tc.sp)
			_, stored, err := Parse(p.cert, pem.EncodeToMemory(tc.key), now)
			if err != nil {
				t.Fatal(err)
			}
			// Saved as PKCS#8 whatever it came as, so there is one format to read back.
			var types []string
			for rest := stored; ; {
				var b *pem.Block
				if b, rest = pem.Decode(rest); b == nil {
					break
				}
				types = append(types, b.Type)
			}
			if !slices.Equal(types, []string{"CERTIFICATE", "PRIVATE KEY"}) {
				t.Errorf("saved blocks %v", types)
			}
		})
	}
}

func TestParseTakesOneFileWithBoth(t *testing.T) {
	p := issue(t, vpn())
	both := append(append([]byte("# issued today\n"), p.cert...), p.key...)
	if _, _, err := Parse(both, both, now); err != nil {
		t.Fatalf("a file with the certificate and the key: %v", err)
	}
}

func TestParseKeepsTheChainInOrder(t *testing.T) {
	ca := issue(t, spec{dns: []string{"ca.example.com"}, isCA: true})
	caCert, _ := x509.ParseCertificate(ca.der)
	caKey, _ := parseKey(t, ca.key)
	leaf := issue(t, spec{dns: []string{"vpn.example.com"}, signer: caCert, signKey: caKey})

	chain := append(append([]byte{}, leaf.cert...), ca.cert...)
	cert, stored, err := Parse(chain, leaf.key, now)
	if err != nil || len(cert.Certificate) != 2 || !bytes.Equal(cert.Certificate[0], leaf.der) {
		t.Fatalf("a chain: %d certificates, %v", len(cert.Certificate), err)
	}
	if got := bytes.Count(stored, []byte("BEGIN CERTIFICATE")); got != 2 {
		t.Errorf("saved %d certificates, want 2", got)
	}

	// The intermediate first is a mistake the key gives away.
	_, _, err = Parse(append(append([]byte{}, ca.cert...), leaf.cert...), leaf.key, now)
	wantInvalid(t, err, "doesn't belong to the first certificate")
}

func parseKey(t *testing.T, keyPEM []byte) (any, error) {
	t.Helper()
	b, _ := pem.Decode(keyPEM)
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	return k, err
}

func wantInvalid(t *testing.T, err error, contains string) {
	t.Helper()
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("got %v, want an InvalidError containing %q", err, contains)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("got %q, want it to contain %q", err, contains)
	}
}

func TestParseRefuses(t *testing.T) {
	good := issue(t, vpn())
	other := issue(t, vpn())
	encrypted := pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("x")})
	legacyEncrypted := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"}, Bytes: []byte("x")})

	for name, tc := range map[string]struct {
		cert, key []byte
		want      string
	}{
		"nothing":                   {nil, good.key, "no certificate found"},
		"text":                      {[]byte("hello"), good.key, "no certificate found"},
		"the key in the cert field": {good.key, good.key, "the certificate is a private key"},
		"no key":                    {good.cert, nil, "no private key found"},
		"the cert in the key field": {good.cert, good.cert, "the private key is a certificate"},
		"another certificate's key": {good.cert, other.key, "doesn't belong to the first certificate"},
		"an encrypted key":          {good.cert, encrypted, "encrypted with a passphrase"},
		"an old encrypted key":      {good.cert, legacyEncrypted, "encrypted with a passphrase"},
		"two keys":                  {good.cert, append(append([]byte{}, good.key...), other.key...), "more than one private key"},
		"a key that is garbage":     {good.cert, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("garbage")}), "can't read the private key"},
		"too many certificates":     {bytes.Repeat(good.cert, maxChain+1), good.key, "at most"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := Parse(tc.cert, tc.key, now)
			wantInvalid(t, err, tc.want)
		})
	}
}

func TestParseRefusesWhatABrowserCouldNotUse(t *testing.T) {
	rsaSmall, _ := rsa.GenerateKey(rand.Reader, 1024)
	for name, tc := range map[string]struct {
		sp   spec
		at   time.Time
		want string
	}{
		"expired":        {spec{dns: []string{"a.example.com"}, notAfter: now.Add(-time.Hour)}, now, "expired on 2026-09-26"},
		"not yet valid":  {vpn(), now.Add(-48 * time.Hour), "isn't valid until"},
		"no names":       {spec{}, now, "names no host or address"},
		"a client cert":  {spec{dns: []string{"a.example.com"}, usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}, now, "isn't for servers"},
		"a 1024-bit key": {spec{dns: []string{"a.example.com"}, key: rsaSmall}, now, "1024 bits"},
	} {
		t.Run(name, func(t *testing.T) {
			p := issue(t, tc.sp)
			_, _, err := Parse(p.cert, p.key, tc.at)
			wantInvalid(t, err, tc.want)
		})
	}
	// An error never quotes the key.
	p := issue(t, vpn())
	other := issue(t, vpn())
	_, _, err := Parse(p.cert, other.key, now)
	if err == nil || strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Errorf("the error: %v", err)
	}
}

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tls")
	s, err := Open(dir, Options{
		Names: func() Names { return DefaultNames("server", netip.MustParseAddr("192.0.2.5")) },
		Now:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestOpenServesTheSelfSignedCertificateByDefault(t *testing.T) {
	s, dir := openStore(t)
	if info := s.Info(); info.Source != SelfSigned || len(info.Names) == 0 {
		t.Fatalf("info %+v", info)
	}
	if _, err := os.Stat(filepath.Join(dir, uploadedFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an uploaded file before anything was uploaded: %v", err)
	}
	if got := s.HostNames(); !slices.Equal(got, []string{"server", "server.local", "192.0.2.5"}) {
		t.Errorf("host names %v", got)
	}
	if _, _, err := s.Reset(); !errors.Is(err, ErrNotUploaded) {
		t.Errorf("Reset with nothing uploaded: %v", err)
	}
}

// serve answers TLS handshakes on a new listener with the store's configuration, and returns the
// certificate (the leaf's DER) that a client is shown.
func shownBy(t *testing.T, s *Store) []byte {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", s.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.(*tls.Conn).Handshake()
			_ = c.Close()
		}
	}()
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec // G402: it reads the certificate and trusts nothing.
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].Raw
}

// The effect: a running listener serves the new certificate to the next connection, with no
// restart, and the old one again after a reset.
func TestInstallAndResetChangeWhatIsServed(t *testing.T) {
	s, dir := openStore(t)
	self := shownBy(t, s)
	selfInfo := s.Info()

	p := issue(t, vpn())
	from, to, err := s.Install(p.cert, p.key)
	if err != nil {
		t.Fatal(err)
	}
	if from.Fingerprint != selfInfo.Fingerprint || from.Source != SelfSigned || to.Source != Uploaded ||
		to.Fingerprint == from.Fingerprint || !slices.Equal(to.Names, []string{"vpn.example.com"}) {
		t.Errorf("from %+v, to %+v", from, to)
	}
	if got := shownBy(t, s); !bytes.Equal(got, p.der) || bytes.Equal(got, self) {
		t.Fatal("a new connection wasn't shown the uploaded certificate")
	}
	if s.Fingerprint() != to.Fingerprint || s.Info().Source != Uploaded {
		t.Errorf("Info says %+v", s.Info())
	}

	// On disk it's one private file, and the self-signed pair is still there to go back to.
	info, err := os.Stat(filepath.Join(dir, uploadedFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("uploaded file: %v, %v", info, err)
	}
	for _, f := range []string{"cert.pem", "key.pem"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}

	// A restart uses it.
	reopened, err := Open(dir, s.opts)
	if err != nil || reopened.Fingerprint() != to.Fingerprint || reopened.Info().Source != Uploaded {
		t.Fatalf("after a restart: %v, %+v", err, reopened.Info())
	}

	// Installing another replaces it.
	q := issue(t, spec{dns: []string{"other.example.com"}})
	if _, _, err := s.Install(q.cert, q.key); err != nil || !bytes.Equal(shownBy(t, s), q.der) {
		t.Fatalf("replacing the upload: %v", err)
	}

	// Reset goes back, and forgets the upload for good.
	from, to, err = s.Reset()
	if err != nil || from.Source != Uploaded || to.Source != SelfSigned || !bytes.Equal(shownBy(t, s), self) {
		t.Fatalf("reset: %v, from %+v, to %+v", err, from, to)
	}
	if _, err := os.Stat(filepath.Join(dir, uploadedFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the uploaded file is still there: %v", err)
	}
	reopened, err = Open(dir, s.opts)
	if err != nil || reopened.Info().Source != SelfSigned {
		t.Fatalf("after a restart: %v, %+v", err, reopened.Info())
	}
}

func TestAnInstallThatIsRefusedChangesNothing(t *testing.T) {
	s, dir := openStore(t)
	before := s.Fingerprint()
	p := issue(t, vpn())
	other := issue(t, vpn())
	if _, _, err := s.Install(p.cert, other.key); err == nil {
		t.Fatal("a mismatched pair was installed")
	}
	if s.Fingerprint() != before {
		t.Error("the served certificate changed")
	}
	if _, err := os.Stat(filepath.Join(dir, uploadedFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file was written: %v", err)
	}

	// With one installed already, a refused one leaves it be.
	if _, _, err := s.Install(p.cert, p.key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Install(p.cert, other.key); err == nil {
		t.Fatal("a mismatched pair was installed over a good one")
	}
	if s.Info().Fingerprint != Fingerprint(mustCert(t, p)) {
		t.Error("the good upload was lost")
	}
}

func mustCert(t *testing.T, p pair) tls.Certificate {
	t.Helper()
	c, err := tls.X509KeyPair(p.cert, p.key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A certificate that has run out is still the one served, because swapping it for another
// without being asked isn't the daemon's call; the notes say so.
func TestAnExpiredUploadKeepsServing(t *testing.T) {
	s, dir := openStore(t)
	p := issue(t, spec{dns: []string{"vpn.example.com"}, notAfter: now.Add(48 * time.Hour)})
	if _, _, err := s.Install(p.cert, p.key); err != nil {
		t.Fatal(err)
	}
	later := now.Add(60 * 24 * time.Hour)
	opts := s.opts
	opts.Now = func() time.Time { return later }
	reopened, err := Open(dir, opts)
	if err != nil || reopened.Info().Source != Uploaded {
		t.Fatalf("an expired upload at startup: %v, %+v", err, reopened.Info())
	}
	notes := reopened.Notes(nil)
	if len(notes) != 1 || !strings.Contains(notes[0], "expired on") {
		t.Errorf("notes %v", notes)
	}
}

// A damaged file doesn't stop the daemon or take the web UI away: the self-signed certificate
// serves, the problem is logged, and the file is left for the admin to deal with.
func TestADamagedUploadFallsBack(t *testing.T) {
	s, dir := openStore(t)
	p := issue(t, vpn())
	if _, _, err := s.Install(p.cert, p.key); err != nil {
		t.Fatal(err)
	}
	damaged := []byte("-----BEGIN CERTIFICATE-----\nbm90IG9uZQ==\n-----END CERTIFICATE-----\n")
	if err := os.WriteFile(filepath.Join(dir, uploadedFile), damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	opts := s.opts
	opts.Log = slog.New(slog.NewTextHandler(&logged, nil))
	reopened, err := Open(dir, opts)
	if err != nil || reopened.Info().Source != SelfSigned {
		t.Fatalf("open: %v, %+v", err, reopened.Info())
	}
	if !strings.Contains(logged.String(), "can't use the uploaded TLS certificate") || !strings.Contains(logged.String(), "drawbridge tls reset") {
		t.Errorf("log: %s", logged.String())
	}
	if _, err := os.Stat(filepath.Join(dir, uploadedFile)); err != nil {
		t.Errorf("the damaged file was removed: %v", err)
	}
	// Reset clears it, even though it wasn't in use.
	if _, _, err := reopened.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, uploadedFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("reset left the damaged file: %v", err)
	}
}

func TestNotes(t *testing.T) {
	ca := issue(t, spec{dns: []string{"ca.example.com"}, isCA: true})
	caCert, _ := x509.ParseCertificate(ca.der)
	caKey, _ := parseKey(t, ca.key)
	issued := issue(t, spec{dns: []string{"vpn.example.com"}, signer: caCert, signKey: caKey, notAfter: now.Add(10 * 24 * time.Hour)})
	cert := mustCert(t, issued)

	notes := Notes(cert, []string{"server", "192.0.2.5"}, now)
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"expires on 2026-10-06 (10 days)", "covers none of the names", "only the server's own certificate"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes %q lack %q", joined, want)
		}
	}

	// Covering any one of the host's names is enough, a wildcard counts, and a certificate that
	// signed itself has no chain to be missing.
	wildcard := mustCert(t, issue(t, spec{dns: []string{"*.example.com"}}))
	if got := Notes(wildcard, []string{"server", "vpn.example.com"}, now); len(got) != 0 {
		t.Errorf("a wildcard that covers the endpoint: %v", got)
	}
	byAddress := mustCert(t, issue(t, spec{ips: []net.IP{net.ParseIP("192.0.2.5")}}))
	if got := Notes(byAddress, []string{"server", "192.0.2.5"}, now); len(got) != 0 {
		t.Errorf("a certificate for the host's address: %v", got)
	}
	// Nothing known about the host, nothing to say about names.
	if got := Notes(byAddress, nil, now); len(got) != 0 {
		t.Errorf("no hosts: %v", got)
	}
}
