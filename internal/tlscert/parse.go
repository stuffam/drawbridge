package tlscert

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// InvalidError is a certificate or a key that can't be used. Its message says why, for the
// admin who supplied it; nothing in it quotes the key.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

const (
	// maxChain is the most certificates a file may hold: the server's own and its
	// intermediates. Real chains have two or three.
	maxChain = 8
	// minRSABits is the smallest RSA key that's accepted.
	minRSABits = 2048
)

// Parse reads a certificate chain and its private key (both PEM) and returns the pair, ready
// to serve, and the normalized PEM it would be saved as: the certificates first, then the key
// as PKCS#8, with anything else in the input (comments, other blocks) dropped. Either input may
// be a file that holds both, as some tools write.
//
// The server's own certificate must come first in the chain. Parse checks that the key belongs
// to it, and that the pair is something a browser could use: the certificate is valid now, names
// something (browsers ignore the common name), allows server authentication, and isn't signed
// with a weak RSA key.
func Parse(certPEM, keyPEM []byte, now time.Time) (tls.Certificate, []byte, error) {
	cert, stored, err := parsePair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := usable(cert.Leaf, now); err != nil {
		return tls.Certificate{}, nil, err
	}
	return cert, stored, nil
}

// parsePair is Parse without the checks that depend on the clock or the certificate's purpose.
// The daemon uses it alone to load a certificate it already accepted, which stays in use after
// it expires: replacing one silently is not the daemon's call.
func parsePair(certPEM, keyPEM []byte) (tls.Certificate, []byte, error) {
	var certs [][]byte
	sawKey := false
	for rest := certPEM; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		switch {
		case b.Type == "CERTIFICATE":
			certs = append(certs, b.Bytes)
		case strings.HasSuffix(b.Type, "PRIVATE KEY"):
			sawKey = true
		}
	}
	switch {
	case len(certs) == 0 && sawKey:
		return tls.Certificate{}, nil, invalid("the certificate is a private key; the certificate goes in the certificate field, and the key in the key field")
	case len(certs) == 0:
		return tls.Certificate{}, nil, invalid("no certificate found: it should be PEM text that starts with -----BEGIN CERTIFICATE-----")
	case len(certs) > maxChain:
		return tls.Certificate{}, nil, invalid("the file holds %d certificates; a chain has at most %d", len(certs), maxChain)
	}

	var key *pem.Block
	sawCert := false
	for rest := keyPEM; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		switch {
		case b.Type == "CERTIFICATE":
			sawCert = true
		case strings.HasSuffix(b.Type, "PRIVATE KEY"):
			if b.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(b.Headers["Proc-Type"], "ENCRYPTED") {
				return tls.Certificate{}, nil, invalid("the private key is encrypted with a passphrase; remove the passphrase first (openssl pkey -in key.pem -out key-plain.pem), and keep the plain file somewhere private")
			}
			if key != nil {
				return tls.Certificate{}, nil, invalid("the key field holds more than one private key")
			}
			key = b
		}
	}
	switch {
	case key == nil && sawCert:
		return tls.Certificate{}, nil, invalid("the private key is a certificate; the key goes in the key field")
	case key == nil:
		return tls.Certificate{}, nil, invalid("no private key found: it should be PEM text that starts with -----BEGIN PRIVATE KEY-----")
	}

	var chain []byte
	for _, der := range certs {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	cert, err := tls.X509KeyPair(chain, pem.EncodeToMemory(&pem.Block{Type: key.Type, Bytes: key.Bytes}))
	if err != nil {
		return tls.Certificate{}, nil, explain(err)
	}
	// One format on disk, whatever the key came as (PKCS#1, SEC1, or PKCS#8).
	der, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return tls.Certificate{}, nil, invalid("the private key is of a kind that can't be used for TLS")
	}
	stored := append(chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})...)
	return cert, stored, nil
}

// explain turns crypto/tls's messages about a pair into ones that say what to do.
func explain(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "does not match"):
		return invalid("the private key doesn't belong to the first certificate in the file; the server's own certificate must come first, then the intermediates, and the key must be the one the certificate was issued for")
	case strings.Contains(msg, "private key"):
		return invalid("can't read the private key; it may be in a format TLS can't use (RSA, ECDSA, and Ed25519 keys work)")
	default:
		return invalid("can't read the certificate: %s", strings.TrimPrefix(msg, "tls: "))
	}
}

// usable reports why a certificate that parses still can't serve the web UI now.
func usable(leaf *x509.Certificate, now time.Time) error {
	switch {
	case now.Before(leaf.NotBefore):
		return invalid("the certificate isn't valid until %s", leaf.NotBefore.UTC().Format(time.DateOnly))
	case !now.Before(leaf.NotAfter):
		return invalid("the certificate expired on %s", leaf.NotAfter.UTC().Format(time.DateOnly))
	case len(leaf.DNSNames) == 0 && len(leaf.IPAddresses) == 0:
		return invalid("the certificate names no host or address, and browsers ignore the common name; it needs at least one subject alternative name")
	}
	if len(leaf.ExtKeyUsage) > 0 {
		ok := false
		for _, u := range leaf.ExtKeyUsage {
			ok = ok || u == x509.ExtKeyUsageServerAuth || u == x509.ExtKeyUsageAny
		}
		if !ok {
			return invalid("the certificate isn't for servers: its extended key usage doesn't include server authentication")
		}
	}
	if pub, ok := leaf.PublicKey.(*rsa.PublicKey); ok && pub.N.BitLen() < minRSABits {
		return invalid("the certificate's RSA key is %d bits; at least %d are needed", pub.N.BitLen(), minRSABits)
	}
	return nil
}
