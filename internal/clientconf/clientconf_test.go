package clientconf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/stuffam/drawbridge/internal/model"
)

func mustKey(t *testing.T, s string) wgtypes.Key {
	t.Helper()
	k, err := wgtypes.ParseKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func fixture(t *testing.T) (model.Settings, model.Client) {
	t.Helper()
	serverKey := mustKey(t, "YGqmUc6XZ4dPC6hDFxYlZ2JZhTzZoq9M4b/7Szs9pVw=")
	s, err := model.NewSettings(serverKey, netip.MustParsePrefix("fd3a:5c1e:92b0:1::/64"))
	if err != nil {
		t.Fatal(err)
	}
	s.EndpointHost = "vpn.example.com"
	// A server whose admin chose "this server" as the clients' DNS.
	srvAddrs, err := s.ServerAddrs()
	if err != nil {
		t.Fatal(err)
	}
	s.DNS = []netip.Addr{srvAddrs.IPv4, srvAddrs.IPv6}
	clientKey := mustKey(t, "gIq0lCBAyEeEVJTTaPUBeFgjNYHLnw7ARXdJwqhpzmg=")
	c := model.Client{
		Name:         "Alex's phone",
		IPv4:         netip.MustParseAddr("10.8.0.23"),
		IPv6:         netip.MustParseAddr("fd3a:5c1e:92b0:1::23"),
		PublicKey:    clientKey.PublicKey(),
		PrivateKey:   &clientKey,
		PresharedKey: mustKey(t, "RvD5N2a6c2W5BEo+kLKeL2Hcn3pO4ZfV6SWsZMUuHuo="),
	}
	return s, c
}

func TestRender(t *testing.T) {
	s, c := fixture(t)
	got, err := Render(s, c)
	if err != nil {
		t.Fatal(err)
	}
	want := `[Interface]
PrivateKey = gIq0lCBAyEeEVJTTaPUBeFgjNYHLnw7ARXdJwqhpzmg=
Address = 10.8.0.23/32, fd3a:5c1e:92b0:1::23/128
DNS = 10.8.0.1, fd3a:5c1e:92b0:1::1
MTU = 1420

[Peer]
PublicKey = ` + s.PublicKey().String() + `
PresharedKey = RvD5N2a6c2W5BEo+kLKeL2Hcn3pO4ZfV6SWsZMUuHuo=
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, c.Name) || strings.Contains(got, "phone") {
		t.Fatal("the client's name reached the config file")
	}
}

func TestRenderVariants(t *testing.T) {
	s, c := fixture(t)

	s.EndpointHost, s.EndpointPort = "2001:db8::1", 443
	s.Keepalive = 0
	s.DNS = nil
	c.IPv6 = netip.Addr{}
	c.PresharedKey = wgtypes.Key{}
	got, err := Render(s, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Endpoint = [2001:db8::1]:443\n", "Address = 10.8.0.23/32\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"PersistentKeepalive", "DNS =", "PresharedKey"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in:\n%s", unwanted, got)
		}
	}
}

func TestRenderErrors(t *testing.T) {
	s, c := fixture(t)
	s.EndpointHost = ""
	if _, err := Render(s, c); !errors.Is(err, model.ErrNoEndpoint) {
		t.Errorf("no endpoint: err %v", err)
	}
	s, c = fixture(t)
	c.PrivateKey = nil
	if _, err := Render(s, c); !errors.Is(err, ErrNoPrivateKey) {
		t.Errorf("no private key: err %v", err)
	}
}

func TestWriteQR(t *testing.T) {
	s, c := fixture(t)
	conf, err := Render(s, c)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteQR(&buf, conf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	width := strings.Count(lines[0], "▀")
	// A QR code is 17 + 4v modules wide, plus the quiet zone on both sides.
	if (width-2*quietZone-17)%4 != 0 || width <= 2*quietZone+17 {
		t.Fatalf("width %d isn't a QR code size plus the quiet zone", width)
	}
	if want := (width + 1) / 2; len(lines) != want {
		t.Fatalf("%d lines, want %d for a %d-module code", len(lines), want, width)
	}
	for i, l := range lines {
		if strings.Count(l, "▀") != width {
			t.Fatalf("line %d has %d cells, want %d", i, strings.Count(l, "▀"), width)
		}
		if !strings.HasSuffix(l, reset) {
			t.Fatalf("line %d doesn't reset the colors", i)
		}
	}
	// The top quiet zone is light on both halves of every cell.
	if strings.Contains(lines[0], fgDark) || strings.Contains(lines[0], bgDark) {
		t.Fatal("the quiet zone contains dark modules")
	}
}

// Everything that's in the config a client holds is in its fingerprint: a change to any of it
// is a change the client has to import.
func TestFingerprintChangesWithTheConfig(t *testing.T) {
	base, c := fixture(t)
	want, err := Fingerprint(base, c)
	if err != nil {
		t.Fatal(err)
	}
	other := mustKey(t, "6Cn0pZ9yCk4vVQ6pPzm3j4mT5b8D2e3fW1sH7gJ0xUg=")

	for name, change := range map[string]func(*model.Settings, *model.Client){
		"endpoint host": func(s *model.Settings, _ *model.Client) { s.EndpointHost = "vpn2.example.com" },
		"endpoint port": func(s *model.Settings, _ *model.Client) { s.EndpointPort = 443 },
		"listen port":   func(s *model.Settings, _ *model.Client) { s.ListenPort = 51999 },
		"DNS":           func(s *model.Settings, _ *model.Client) { s.DNS = []netip.Addr{netip.MustParseAddr("9.9.9.9")} },
		"MTU":           func(s *model.Settings, _ *model.Client) { s.MTU = 1380 },
		"keepalive":     func(s *model.Settings, _ *model.Client) { s.Keepalive = 0 },
		"allowed IPs": func(s *model.Settings, _ *model.Client) {
			s.ClientAllowedIPs = []netip.Prefix{netip.MustParsePrefix("10.8.0.0/24")}
		},
		"server key":         func(s *model.Settings, _ *model.Client) { s.PrivateKey = other },
		"client IPv4":        func(_ *model.Settings, c *model.Client) { c.IPv4 = netip.MustParseAddr("10.8.0.24") },
		"client IPv6":        func(_ *model.Settings, c *model.Client) { c.IPv6 = netip.Addr{} },
		"client key":         func(_ *model.Settings, c *model.Client) { c.PublicKey = other },
		"preshared key gone": func(_ *model.Settings, c *model.Client) { c.PresharedKey = wgtypes.Key{} },
	} {
		s, cl := base, c
		change(&s, &cl)
		got, err := Fingerprint(s, cl)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got == want {
			t.Errorf("changing the %s left the fingerprint as it was", name)
		}
	}
}

// What isn't in the config a client holds doesn't make it outdated.
func TestFingerprintIgnoresWhatIsntInTheConfig(t *testing.T) {
	base, c := fixture(t)
	base.EndpointPort = 443 // so the listen port isn't advertised
	want, err := Fingerprint(base, c)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*model.Settings, *model.Client){
		"client name":      func(_ *model.Settings, c *model.Client) { c.Name = "Alex's tablet" },
		"paused":           func(_ *model.Settings, c *model.Client) { c.Enabled = false },
		"listen port":      func(s *model.Settings, _ *model.Client) { s.ListenPort = 51999 },
		"client isolation": func(s *model.Settings, _ *model.Client) { s.ClientIsolation = !s.ClientIsolation },
		"admin sources": func(s *model.Settings, _ *model.Client) {
			s.AdminAllowed = []netip.Prefix{netip.MustParsePrefix("100.64.10.0/24")}
		},
		"client created at": func(_ *model.Settings, c *model.Client) { c.ID = "another" },
	} {
		s, cl := base, c
		change(&s, &cl)
		got, err := Fingerprint(s, cl)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("changing the %s changed the fingerprint", name)
		}
	}
}

// The fingerprint is computed without any secret, so it's the same for a client whose private
// key the server doesn't keep, and a different private key (with the same public key, which
// can't happen for real) can't change it.
func TestFingerprintHoldsNoSecret(t *testing.T) {
	s, c := fixture(t)
	want, err := Fingerprint(s, c)
	if err != nil {
		t.Fatal(err)
	}
	c.PrivateKey = nil
	if got, err := Fingerprint(s, c); err != nil || got != want {
		t.Fatalf("without the private key: %q, %v; want %q", got, err, want)
	}
	flipped := mustKey(t, "6Cn0pZ9yCk4vVQ6pPzm3j4mT5b8D2e3fW1sH7gJ0xUg=")
	c.PrivateKey = &flipped
	if got, err := Fingerprint(s, c); err != nil || got != want {
		t.Fatalf("with another private key: %q, %v; want %q", got, err, want)
	}
	c.PresharedKey = mustKey(t, "6Cn0pZ9yCk4vVQ6pPzm3j4mT5b8D2e3fW1sH7gJ0xUg=")
	if got, err := Fingerprint(s, c); err != nil || got != want {
		t.Fatalf("with another preshared key: %q, %v; want %q", got, err, want)
	}
}

func TestFingerprintNeedsAnEndpoint(t *testing.T) {
	s, c := fixture(t)
	s.EndpointHost = ""
	if _, err := Fingerprint(s, c); !errors.Is(err, model.ErrNoEndpoint) {
		t.Fatalf("err %v, want ErrNoEndpoint", err)
	}
}

// The fingerprint is the hash of one fixed text, written out here. Changing how a config is
// rendered changes it for every client, which flags every client that was handed a config as
// outdated the moment a server is upgraded. That can be right (a client really does need the
// new line), but it's a decision: change the text here, and say so in the release notes.
func TestFingerprintIsTheHashOfAKnownText(t *testing.T) {
	s, c := fixture(t)
	got, err := Fingerprint(s, c)
	if err != nil {
		t.Fatal(err)
	}
	text := `[Interface]
PrivateKey = ` + c.PublicKey.String() + `
Address = 10.8.0.23/32, fd3a:5c1e:92b0:1::23/128
DNS = 10.8.0.1, fd3a:5c1e:92b0:1::1
MTU = 1420

[Peer]
PublicKey = ` + s.PublicKey().String() + `
PresharedKey = present
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
`
	sum := sha256.Sum256([]byte(text))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("the fingerprint is %s, not the hash of the known text, %s", got, want)
	}
}
