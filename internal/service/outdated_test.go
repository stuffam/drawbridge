package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/store"
)

// withEndpoint sets the endpoint, so configs can be rendered.
func withEndpoint(t *testing.T, s *Service, host string) {
	t.Helper()
	if _, _, err := s.UpdateSettings(context.Background(), SettingsPatch{EndpointHost: &host}); err != nil {
		t.Fatal(err)
	}
}

func clientStatus(t *testing.T, s *Service, name string) ClientStatus {
	t.Helper()
	c, err := s.Client(context.Background(), store.ByName(name))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func outdatedCount(t *testing.T, s *Service) int {
	t.Helper()
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.Outdated
}

func peerKeys(t *testing.T, s *Service) map[wgtypes.Key]bool {
	t.Helper()
	dev, err := s.WG.Device("wg0")
	if err != nil {
		t.Fatal(err)
	}
	out := map[wgtypes.Key]bool{}
	for _, p := range dev.Peers {
		out[p.PublicKey] = true
	}
	return out
}

// A client is outdated when the config it was last given differs from the one the server would
// give it now: not before it was given one, and not once it's been given the current one.
func TestConfigGoesOutdatedWhenTheServerChanges(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	withEndpoint(t, s, "vpn.example.com")
	if _, _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddClient(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}

	// Nothing has been handed out, so nothing can be out of date.
	if c := clientStatus(t, s, "phone"); c.ConfigOutdated || !c.ConfigDeliveredAt.IsZero() {
		t.Fatalf("a new client: %+v", c.Client)
	}

	if _, _, err := s.Config(ctx, store.ByName("phone")); err != nil {
		t.Fatal(err)
	}
	c := clientStatus(t, s, "phone")
	if c.ConfigOutdated || c.ConfigDeliveredAt.IsZero() {
		t.Fatalf("after handing out its config: outdated %v, delivered %v, want not outdated and a time",
			c.ConfigOutdated, c.ConfigDeliveredAt)
	}

	// A change that isn't in the config leaves it alone.
	off := false
	if _, _, err := s.UpdateSettings(ctx, SettingsPatch{ClientIsolation: &off}); err != nil {
		t.Fatal(err)
	}
	if clientStatus(t, s, "phone").ConfigOutdated || outdatedCount(t, s) != 0 {
		t.Fatal("a firewall setting flagged a client's config as outdated")
	}

	// A change that is in it flags the client that was handed one, and only that one.
	mtu := 1380
	if _, _, err := s.UpdateSettings(ctx, SettingsPatch{MTU: &mtu}); err != nil {
		t.Fatal(err)
	}
	if !clientStatus(t, s, "phone").ConfigOutdated {
		t.Fatal("changing the MTU didn't flag the client")
	}
	if clientStatus(t, s, "laptop").ConfigOutdated {
		t.Error("a client that was never handed a config is flagged")
	}
	if got := outdatedCount(t, s); got != 1 {
		t.Errorf("Status.Outdated = %d, want 1", got)
	}
	all, err := s.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if want := c.Name == "phone"; c.ConfigOutdated != want {
			t.Errorf("Clients(): %s outdated %v, want %v", c.Name, c.ConfigOutdated, want)
		}
	}
	_, snap, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap {
		if want := c.Name == "phone"; c.ConfigOutdated != want {
			t.Errorf("Snapshot(): %s outdated %v, want %v", c.Name, c.ConfigOutdated, want)
		}
	}

	// Handing the client the current config clears it.
	if _, _, err := s.Config(ctx, store.ByName("phone")); err != nil {
		t.Fatal(err)
	}
	if clientStatus(t, s, "phone").ConfigOutdated || outdatedCount(t, s) != 0 {
		t.Fatal("handing out the new config left the client flagged")
	}

	// It's a comparison with what was handed out, not a count of changes: undoing a change
	// before the client imports it leaves nothing for it to import.
	original := 1420
	if _, _, err := s.UpdateSettings(ctx, SettingsPatch{MTU: &original}); err != nil {
		t.Fatal(err)
	}
	if !clientStatus(t, s, "phone").ConfigOutdated {
		t.Fatal("expected the change back to differ from the config handed out")
	}
	if _, _, err := s.UpdateSettings(ctx, SettingsPatch{MTU: &mtu}); err != nil {
		t.Fatal(err)
	}
	if clientStatus(t, s, "phone").ConfigOutdated {
		t.Fatal("putting the setting back should match the config handed out")
	}
}

// Each setting a client's config carries flags it.
func TestEachSettingInTheConfigFlagsTheClient(t *testing.T) {
	ctx := web(context.Background(), "admin")
	host, port, mtu, keepalive := "vpn2.example.com", uint16(443), 1300, 10
	for name, patch := range map[string]SettingsPatch{
		"endpoint":      {EndpointHost: &host},
		"endpoint port": {EndpointPort: &port},
		"listen port":   {ListenPort: &port},
		"MTU":           {MTU: &mtu},
		"keepalive":     {Keepalive: &keepalive},
		"DNS":           {DNSDefault: true},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newTestService(t)
			withEndpoint(t, s, "vpn.example.com")
			if _, _, err := s.AddClient(ctx, "phone"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Config(ctx, store.ByName("phone")); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.UpdateSettings(ctx, patch); err != nil {
				t.Fatal(err)
			}
			if !clientStatus(t, s, "phone").ConfigOutdated {
				t.Fatalf("changing the %s didn't flag the client", name)
			}
		})
	}
}

// Without an endpoint there's no config to compare, so nothing is flagged.
func TestNoEndpointFlagsNothing(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	withEndpoint(t, s, "vpn.example.com")
	if _, _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Config(ctx, store.ByName("phone")); err != nil {
		t.Fatal(err)
	}
	withEndpoint(t, s, "")
	if clientStatus(t, s, "phone").ConfigOutdated || outdatedCount(t, s) != 0 {
		t.Fatal("a client was flagged although its config can't be rendered to compare")
	}
}

// A config that can't be rendered isn't handed out, so it isn't recorded as given.
func TestAConfigThatFailsIsNotRecordedAsGiven(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	if _, _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Config(ctx, store.ByName("phone")); !errors.Is(err, model.ErrNoEndpoint) {
		t.Fatalf("err %v, want ErrNoEndpoint", err)
	}
	if c := clientStatus(t, s, "phone"); !c.ConfigDeliveredAt.IsZero() || c.ConfigHash != "" {
		t.Fatalf("a failed render recorded a delivery: %+v", c.Client)
	}
	for _, k := range kinds(t, s) {
		if k == "client.config_viewed" {
			t.Fatal("a failed render was logged as a view")
		}
	}
}

func TestRotateClientKeys(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	withEndpoint(t, s, "vpn.example.com")
	phone, _, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	laptop, _, err := s.AddClient(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	_, oldConf, err := s.Config(ctx, store.ByName("phone"))
	if err != nil {
		t.Fatal(err)
	}
	if peers := peerKeys(t, s); !peers[phone.PublicKey] || !peers[laptop.PublicKey] {
		t.Fatal("the clients aren't in the tunnel to begin with")
	}

	rotated, applied, err := s.RotateClientKeys(ctx, store.ByName("phone"))
	if err != nil || applied.Warning() != "" {
		t.Fatalf("rotating: %v, %q", err, applied.Warning())
	}
	if rotated.PublicKey == phone.PublicKey || rotated.ID != phone.ID {
		t.Fatalf("the public key is %v, was %v", rotated.PublicKey, phone.PublicKey)
	}

	// The tunnel has the new key and not the old one, and the other client wasn't touched.
	peers := peerKeys(t, s)
	if peers[phone.PublicKey] {
		t.Error("the old key is still a peer of the tunnel")
	}
	if !peers[rotated.PublicKey] || !peers[laptop.PublicKey] || len(peers) != 2 {
		t.Errorf("peers after rotating: %v", peers)
	}

	// The config the client holds is out of date, and the new one carries the new keys.
	if !clientStatus(t, s, "phone").ConfigOutdated {
		t.Error("the client's old config isn't flagged")
	}
	if clientStatus(t, s, "laptop").ConfigOutdated {
		t.Error("another client is flagged")
	}
	_, newConf, err := s.Config(ctx, store.ByName("phone"))
	if err != nil {
		t.Fatal(err)
	}
	if newConf == oldConf || !strings.Contains(newConf, "PrivateKey = "+rotated.PrivateKey.String()) ||
		strings.Contains(newConf, phone.PrivateKey.String()) || strings.Contains(newConf, phone.PresharedKey.String()) {
		t.Errorf("the new config isn't the rotated one:\n%s", newConf)
	}
	if clientStatus(t, s, "phone").ConfigOutdated {
		t.Error("handing out the new config left the client flagged")
	}

	// It's an event, from the admin, with the public key and none of the secrets.
	events, err := s.Events(ctx, store.EventFilter{Kind: "client.keys_rotated"})
	if err != nil || len(events) != 1 {
		t.Fatalf("events %v, err %v", events, err)
	}
	e := events[0]
	if e.Actor != "admin" || e.Via != ViaWeb || e.ClientID != phone.ID || e.ClientName != "phone" ||
		e.Category != CategoryAdmin || e.Data["public_key"] != rotated.PublicKey.String() || len(e.Data) != 1 {
		t.Errorf("event %+v", e)
	}
}

func TestRotateKeysOfAPausedClient(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	phone, _, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetEnabled(ctx, store.ByID(phone.ID), false); err != nil {
		t.Fatal(err)
	}
	rotated, _, err := s.RotateClientKeys(ctx, store.ByID(phone.ID))
	if err != nil {
		t.Fatal(err)
	}
	// Paused clients stay out of the tunnel, under any key.
	if peers := peerKeys(t, s); len(peers) != 0 {
		t.Fatalf("a paused client's rotation put a peer in the tunnel: %v", peers)
	}
	if _, _, err := s.SetEnabled(ctx, store.ByID(phone.ID), true); err != nil {
		t.Fatal(err)
	}
	if peers := peerKeys(t, s); !peers[rotated.PublicKey] || peers[phone.PublicKey] || len(peers) != 1 {
		t.Fatalf("after resuming: %v", peers)
	}
}

func TestRotateKeysOfAnUnknownClient(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	if _, _, err := s.RotateClientKeys(ctx, store.ByName("nobody")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err %v, want ErrNotFound", err)
	}
	for _, k := range kinds(t, s) {
		if k == "client.keys_rotated" {
			t.Fatal("a refused rotation was recorded")
		}
	}
}
