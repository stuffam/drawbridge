package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/stuffam/drawbridge/internal/store"
)

// deviceKey is the private key the tunnel is running with.
func deviceKey(t *testing.T, s *Service) wgtypes.Key {
	t.Helper()
	dev, err := s.WG.Device("wg0")
	if err != nil {
		t.Fatal(err)
	}
	return dev.PrivateKey
}

func serverKey(t *testing.T, s *Service) wgtypes.Key {
	t.Helper()
	st, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.PrivateKey
}

// Rotating the server's key changes it in the database and in the tunnel, leaves the peers
// alone, flags every client that holds a config, and records the public keys and nothing else.
func TestRotateServerKey(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	withEndpoint(t, s, "vpn.example.com")
	phone, _, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddClient(ctx, "laptop"); err != nil { // never handed a config
		t.Fatal(err)
	}
	_, oldConf, err := s.Config(ctx, store.ByName("phone"))
	if err != nil {
		t.Fatal(err)
	}
	oldKey := serverKey(t, s)
	if deviceKey(t, s) != oldKey || outdatedCount(t, s) != 0 {
		t.Fatalf("before: tunnel key %v, stored %v, %d outdated", deviceKey(t, s), oldKey, outdatedCount(t, s))
	}
	peersBefore := peerKeys(t, s)

	st, applied, err := s.RotateServerKey(ctx, false)
	if err != nil || applied.Warning() != "" || applied.Pending != nil {
		t.Fatalf("rotating: %v, %q, pending %+v", err, applied.Warning(), applied.Pending)
	}
	if st.PrivateKey == oldKey || st.PrivateKey == (wgtypes.Key{}) {
		t.Fatal("the key didn't change")
	}

	// The effect: the tunnel runs with the new key, and the same peers.
	if got := deviceKey(t, s); got != st.PrivateKey || serverKey(t, s) != st.PrivateKey {
		t.Errorf("tunnel key %v, stored %v, want %v", got, serverKey(t, s), st.PrivateKey)
	}
	if peers := peerKeys(t, s); len(peers) != len(peersBefore) || !peers[phone.PublicKey] {
		t.Errorf("peers %v, want %v", peers, peersBefore)
	}

	// The config the phone holds names the old server key, so it's flagged; the laptop was never
	// handed one, so it isn't. Handing out the new one clears it, and it names the new key.
	if !clientStatus(t, s, "phone").ConfigOutdated || clientStatus(t, s, "laptop").ConfigOutdated ||
		outdatedCount(t, s) != 1 {
		t.Errorf("flags: phone %v, laptop %v, count %d", clientStatus(t, s, "phone").ConfigOutdated,
			clientStatus(t, s, "laptop").ConfigOutdated, outdatedCount(t, s))
	}
	_, newConf, err := s.Config(ctx, store.ByName("phone"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(newConf, "PublicKey = "+st.PublicKey().String()) ||
		strings.Contains(newConf, oldKey.PublicKey().String()) || newConf == oldConf {
		t.Errorf("the new config doesn't carry the new server key:\n%s", newConf)
	}
	if clientStatus(t, s, "phone").ConfigOutdated {
		t.Error("handing out the new config left the client flagged")
	}

	// One event, from the admin, with the public keys and no secrets.
	events, err := s.Events(ctx, store.EventFilter{Kind: "server.key_rotated"})
	if err != nil || len(events) != 1 {
		t.Fatalf("events %v, err %v", events, err)
	}
	e := events[0]
	if e.Actor != "admin" || e.Via != ViaWeb || e.Category != CategoryAdmin || len(e.Data) != 1 ||
		e.Data["server_public_key"] != oldKey.PublicKey().String()+" → "+st.PublicKey().String() {
		t.Errorf("event %+v", e)
	}
	for _, v := range e.Data {
		if strings.Contains(v, oldKey.String()) || strings.Contains(v, st.PrivateKey.String()) {
			t.Fatalf("the event carries a private key: %q", v)
		}
	}
	// The one ordinary settings change is the endpoint, set above.
	if all := strings.Join(kinds(t, s), " "); strings.Count(all, "server.settings_changed") != 1 {
		t.Errorf("a rotation was recorded as an ordinary settings change: %s", all)
	}

	// Rotating again gives yet another key.
	again, _, err := s.RotateServerKey(ctx, false)
	if err != nil || again.PrivateKey == st.PrivateKey || again.PrivateKey == oldKey {
		t.Fatalf("rotating twice: %v", err)
	}
}

// A rotation on probation is in the tunnel at once, and kept, undone, or run out like any
// change that could lock the admin out. Undoing it puts the old key back for real, and the
// configs clients hold are current again.
func TestRotateServerKeySafely(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(*testing.T, *Service, *clock)
		kept bool
	}{
		{"kept", func(t *testing.T, s *Service, _ *clock) {
			if _, err := s.ConfirmChange(web(context.Background(), "admin")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"undone", func(t *testing.T, s *Service, _ *clock) {
			if _, applied, err := s.RevertChange(web(context.Background(), "admin")); err != nil || applied.Err != nil {
				t.Fatalf("%v, %v", err, applied.Err)
			}
		}, false},
		{"not kept in time", func(t *testing.T, s *Service, clk *clock) {
			clk.advance(DefaultSafeApplyWindow)
			s.expirePending(context.Background())
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, clk := newTestService(t)
			ctx := web(context.Background(), "admin")
			withEndpoint(t, s, "vpn.example.com")
			if _, _, err := s.AddClient(ctx, "phone"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Config(ctx, store.ByName("phone")); err != nil {
				t.Fatal(err)
			}
			oldKey := serverKey(t, s)

			st, applied, err := s.RotateServerKey(ctx, true)
			if err != nil || applied.Err != nil {
				t.Fatalf("rotating: %v, %v", err, applied.Err)
			}
			p := applied.Pending
			if p == nil || p.Changes["server_public_key"] != oldKey.PublicKey().String()+" → "+st.PublicKey().String() ||
				p.Actor != "admin" || p.Via != ViaWeb {
				t.Fatalf("pending %+v", p)
			}
			if deviceKey(t, s) != st.PrivateKey || outdatedCount(t, s) != 1 {
				t.Fatalf("not applied: tunnel key %v, %d outdated", deviceKey(t, s), outdatedCount(t, s))
			}
			// Nothing else about the settings changes meanwhile, and the key doesn't rotate twice.
			if _, _, err := s.RotateServerKey(ctx, true); !errors.Is(err, store.ErrChangePending) {
				t.Errorf("rotating while one waits: %v", err)
			}
			for _, v := range p.Changes {
				if strings.Contains(v, st.PrivateKey.String()) || strings.Contains(v, oldKey.String()) {
					t.Fatalf("the pending change carries a private key: %q", v)
				}
			}

			tc.end(t, s, clk)

			if p, _ := s.PendingChange(ctx); p != nil {
				t.Errorf("still pending: %+v", p)
			}
			want, outdated := oldKey, 0
			if tc.kept {
				want, outdated = st.PrivateKey, 1
			}
			if deviceKey(t, s) != want || serverKey(t, s) != want {
				t.Errorf("tunnel key %v, stored %v, want %v", deviceKey(t, s), serverKey(t, s), want)
			}
			// Undone, the config the client holds names the right server key again.
			if got := outdatedCount(t, s); got != outdated {
				t.Errorf("%d clients outdated, want %d", got, outdated)
			}
		})
	}
}

// A rotation made without safe apply (the CLI's default) applies at once and waits for nothing.
func TestRotateServerKeyWithoutSafeApplyDoesNotWait(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	if _, applied, err := s.RotateServerKey(ctx, false); err != nil || applied.Pending != nil {
		t.Fatalf("%v, pending %+v", err, applied.Pending)
	}
	if p, _ := s.PendingChange(ctx); p != nil {
		t.Fatalf("something is pending: %+v", p)
	}
}

// With the tunnel stopped, the new key is saved and the tunnel picks it up when it starts.
func TestRotateServerKeyWithTheTunnelDown(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	if err := s.Rec.Down(ctx); err != nil {
		t.Fatal(err)
	}
	st, applied, err := s.RotateServerKey(ctx, false)
	if err != nil || !applied.TunnelDown {
		t.Fatalf("%v, tunnel down %v", err, applied.TunnelDown)
	}
	if _, err := s.Rec.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if got := deviceKey(t, s); got != st.PrivateKey {
		t.Errorf("the tunnel came up with %v, want %v", got, st.PrivateKey)
	}
}
