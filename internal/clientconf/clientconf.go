// Package clientconf renders a client's WireGuard configuration (docs/PLAN.md §6.1).
//
// Only validated values reach the file: keys, addresses, the endpoint, and numbers. The
// client's name never does (CLAUDE.md, "Free text never reaches a rendered file").
package clientconf

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/stuffam/drawbridge/internal/model"
)

// ErrNoPrivateKey means the server doesn't store this client's private key, so it can't
// render a complete config.
var ErrNoPrivateKey = errors.New("the server doesn't store this client's private key")

// Render returns the client's config in wg-quick format.
func Render(s model.Settings, c model.Client) (string, error) {
	if c.PrivateKey == nil {
		return "", ErrNoPrivateKey
	}
	psk := ""
	if c.PresharedKey != ([32]byte{}) {
		psk = c.PresharedKey.String()
	}
	return render(s, c, c.PrivateKey.String(), psk)
}

// Fingerprint returns a hash that changes whenever the config Render would produce changes,
// for telling a client that holds an outdated config (docs/PLAN.md §6.1). It's the SHA-256
// of the config with the client's public key where its private key goes, and only whether
// there's a preshared key in place of the key: no secret goes into it, so it can sit in the
// database unsealed, and it works for a client whose private key the server doesn't keep.
// Rotating a client's keys changes its public key, so it changes the fingerprint, and a
// preshared key can't change without the client's keys.
//
// Changing the config's format changes every fingerprint, which flags every client as
// outdated; TestFingerprintGolden makes that a decision.
func Fingerprint(s model.Settings, c model.Client) (string, error) {
	psk := ""
	if c.PresharedKey != ([32]byte{}) {
		psk = "present"
	}
	conf, err := render(s, c, c.PublicKey.String(), psk)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(conf))
	return hex.EncodeToString(sum[:]), nil
}

// render writes the config with the given text where the client's private key goes, and where
// the preshared key goes (no preshared key line when psk is empty).
func render(s model.Settings, c model.Client, privateKey, psk string) (string, error) {
	endpoint, err := s.Endpoint()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", privateKey)
	addrs := []string{netip.PrefixFrom(c.IPv4, 32).String()}
	if c.IPv6.IsValid() {
		addrs = append(addrs, netip.PrefixFrom(c.IPv6, 128).String())
	}
	fmt.Fprintf(&b, "Address = %s\n", strings.Join(addrs, ", "))
	if len(s.DNS) > 0 {
		dns := make([]string, len(s.DNS))
		for i, a := range s.DNS {
			dns[i] = a.String()
		}
		fmt.Fprintf(&b, "DNS = %s\n", strings.Join(dns, ", "))
	}
	fmt.Fprintf(&b, "MTU = %d\n", s.MTU)

	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", s.PublicKey())
	if psk != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", psk)
	}
	fmt.Fprintf(&b, "Endpoint = %s\n", endpoint)
	allowed := make([]string, len(s.ClientAllowedIPs))
	for i, p := range s.ClientAllowedIPs {
		allowed[i] = p.String()
	}
	fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(allowed, ", "))
	if s.Keepalive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", s.Keepalive)
	}
	return b.String(), nil
}
