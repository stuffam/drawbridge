package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/views"
)

func patchPort(port uint16) views.SettingsPatch { return views.SettingsPatch{ListenPort: &port} }

// A change that could lock the admin out waits to be kept, and the web API says so every way
// the UI could ask: the response to the change, the state route, and the stream.
func TestSafeApplyOverTheAPI(t *testing.T) {
	svc := newService(t)
	srv := newServer(t, svc)
	b, _ := loggedIn(t, svc, srv)

	var state views.ApplyState
	b.expect(http.StatusOK, "GET", "/api/server/apply", nil).decode(t, &state)
	if state.PendingChange != nil {
		t.Fatalf("something is pending before anything changed: %+v", state)
	}

	// An MTU change can't lock anyone out, so it isn't held.
	mtu := 1380
	var res views.SettingsResult
	b.expect(http.StatusOK, "PATCH", "/api/server", views.SettingsPatch{MTU: &mtu}).decode(t, &res)
	if res.PendingChange != nil || res.Settings.MTU != 1380 {
		t.Fatalf("an MTU change: %+v", res)
	}

	// The listen port can: it's applied, and held.
	r := b.expect(http.StatusOK, "PATCH", "/api/server", patchPort(51999))
	res = views.SettingsResult{}
	r.decode(t, &res)
	p := res.PendingChange
	if res.Settings.ListenPort != 51999 || p == nil || p.Changes["listen_port"] != "51820 → 51999" ||
		p.Actor != "admin" || p.Via != "web" || p.ExpiresIn < 58 || p.ExpiresIn > 60 ||
		time.Until(p.ExpiresAt) > time.Minute {
		t.Fatalf("a listen port change: %+v", res)
	}

	b.expect(http.StatusOK, "GET", "/api/server/apply", nil).decode(t, &state)
	if state.PendingChange == nil || state.PendingChange.Changes["listen_port"] != "51820 → 51999" {
		t.Fatalf("state %+v", state)
	}

	// The stream carries it, so every open page can show it; the server status a token can read doesn't.
	s, _ := openStream(t, b)
	var st views.StreamStatus
	mustJSON(t, s.until("status", 2*time.Second).data, &st)
	if st.PendingChange == nil || st.PendingChange.Changes["listen_port"] != "51820 → 51999" {
		t.Fatalf("the stream's status has no pending change: %+v", st)
	}
	if body := string(b.expect(http.StatusOK, "GET", "/api/server/status", nil).body); strings.Contains(body, "pending") || strings.Contains(body, "51999") {
		t.Fatalf("the server status shows the change: %s", body)
	}

	// Until it's kept, nothing else about the settings changes.
	er := b.expect(http.StatusConflict, "PATCH", "/api/server", views.SettingsPatch{MTU: &mtu})
	if !strings.Contains(er.errorText(), "waiting to be kept") {
		t.Errorf("error %q", er.errorText())
	}

	res = views.SettingsResult{}
	b.expect(http.StatusOK, "POST", "/api/server/apply/confirm", nil).decode(t, &res)
	if res.Settings.ListenPort != 51999 || res.PendingChange != nil {
		t.Fatalf("after confirming: %+v", res)
	}
	state = views.ApplyState{} // a field that's absent from the JSON isn't reset by decoding
	b.expect(http.StatusOK, "GET", "/api/server/apply", nil).decode(t, &state)
	if state.PendingChange != nil {
		t.Fatalf("still pending after it was kept: %+v", state)
	}
	b.expect(http.StatusConflict, "POST", "/api/server/apply/confirm", nil)
	b.expect(http.StatusConflict, "POST", "/api/server/apply/revert", nil)
	b.expect(http.StatusOK, "PATCH", "/api/server", views.SettingsPatch{MTU: &mtu}) // free again

	// Undoing one puts everything back.
	b.expect(http.StatusOK, "PATCH", "/api/server", patchPort(52000))
	res = views.SettingsResult{}
	b.expect(http.StatusOK, "POST", "/api/server/apply/revert", nil).decode(t, &res)
	if res.Settings.ListenPort != 51999 {
		t.Fatalf("after reverting the port is %d, want the 51999 it was", res.Settings.ListenPort)
	}

	// Every step is in the log, from the admin on the web.
	var events []views.EventView
	b.expect(http.StatusOK, "GET", "/api/events?category=admin&limit=50", nil).decode(t, &events)
	var kinds []string
	for _, e := range events {
		if strings.HasPrefix(e.Kind, "server.") {
			kinds = append(kinds, e.Kind)
		}
	}
	// (The second MTU patch repeated the first one's value, which is no change and no event.)
	want := "server.settings_undone server.settings_changed server.settings_kept server.settings_changed server.settings_changed"
	if strings.Join(kinds, " ") != want {
		t.Fatalf("events %v\nwant %s", kinds, want)
	}
}

// A change needs the same CSRF header as any other.
func TestSafeApplyChangesNeedTheHeader(t *testing.T) {
	svc := newService(t)
	srv := newServer(t, svc)
	b, _ := loggedIn(t, svc, srv)
	b.expect(http.StatusOK, "PATCH", "/api/server", patchPort(51999))
	for _, path := range []string{"/api/server/apply/confirm", "/api/server/apply/revert"} {
		if r := b.do("POST", path, nil, csrfHeader, ""); r.status != http.StatusForbidden {
			t.Errorf("POST %s without the header: status %d, want 403", path, r.status)
		}
	}
	var state views.ApplyState
	b.expect(http.StatusOK, "GET", "/api/server/apply", nil).decode(t, &state)
	if state.PendingChange == nil {
		t.Fatal("a refused request resolved the change")
	}
}

// Rotating the server's key over the API waits to be kept (the browser is likely on the very
// tunnel it cuts), flags every client that holds a config, and can be undone.
func TestRotateServerKeyOverTheAPI(t *testing.T) {
	svc := newService(t)
	srv := newServer(t, svc)
	b, _ := loggedIn(t, svc, srv)

	host := "vpn.example.com"
	b.expect(http.StatusOK, "PATCH", "/api/server", views.SettingsPatch{EndpointHost: &host})
	var phone views.ClientResult
	b.expect(http.StatusCreated, "POST", "/api/clients", views.NewClientRequest{Name: "phone"}).decode(t, &phone)
	b.expect(http.StatusOK, "GET", "/api/clients/"+phone.Client.ID+"/config", nil)
	var before views.SettingsView
	b.expect(http.StatusOK, "GET", "/api/server", nil).decode(t, &before)

	var res views.SettingsResult
	r := b.expect(http.StatusOK, "POST", "/api/server/rotate-key", nil)
	r.decode(t, &res)
	p := res.PendingChange
	if res.Settings.PublicKey == before.PublicKey || res.Settings.PublicKey == "" || p == nil ||
		p.Changes["server_public_key"] != before.PublicKey+" → "+res.Settings.PublicKey ||
		p.Actor != "admin" || p.Via != "web" || p.ExpiresIn < 58 || p.ExpiresIn > 60 {
		t.Fatalf("rotating: %+v (the key was %s)", res, before.PublicKey)
	}
	if strings.Contains(strings.ToLower(string(r.body)), "private") {
		t.Fatalf("the response shows a private key: %s", r.body)
	}

	// The clients that hold a config are flagged, and the stream and the state route say so.
	var status views.ServerStatus
	b.expect(http.StatusOK, "GET", "/api/server/status", nil).decode(t, &status)
	if status.Outdated != 1 {
		t.Errorf("status.outdated = %d, want 1", status.Outdated)
	}
	var state views.ApplyState
	b.expect(http.StatusOK, "GET", "/api/server/apply", nil).decode(t, &state)
	if state.PendingChange == nil || state.PendingChange.Changes["server_public_key"] == "" {
		t.Fatalf("state %+v", state)
	}

	// Nothing else changes, and the key doesn't rotate again, until it's kept or undone.
	er := b.expect(http.StatusConflict, "POST", "/api/server/rotate-key", nil)
	if !strings.Contains(er.errorText(), "waiting to be kept") {
		t.Errorf("error %q", er.errorText())
	}

	// Undone, the old key is back and the clients' configs are current.
	res = views.SettingsResult{}
	b.expect(http.StatusOK, "POST", "/api/server/apply/revert", nil).decode(t, &res)
	if res.Settings.PublicKey != before.PublicKey {
		t.Fatalf("after reverting the key is %s, want %s", res.Settings.PublicKey, before.PublicKey)
	}
	status = views.ServerStatus{}
	b.expect(http.StatusOK, "GET", "/api/server/status", nil).decode(t, &status)
	if status.Outdated != 0 {
		t.Errorf("status.outdated = %d after the undo, want 0", status.Outdated)
	}

	// Kept, it stays.
	res = views.SettingsResult{}
	b.expect(http.StatusOK, "POST", "/api/server/rotate-key", nil).decode(t, &res)
	rotated := res.Settings.PublicKey
	b.expect(http.StatusOK, "POST", "/api/server/apply/confirm", nil)
	res = views.SettingsResult{}
	b.expect(http.StatusOK, "GET", "/api/server", nil).decode(t, &res.Settings)
	if res.Settings.PublicKey != rotated || rotated == before.PublicKey {
		t.Fatalf("after keeping it the key is %s, want %s", res.Settings.PublicKey, rotated)
	}

	// It's in the log, with the public keys.
	var events []views.EventView
	b.expect(http.StatusOK, "GET", "/api/events?kind=server.key_rotated", nil).decode(t, &events)
	if len(events) != 2 || events[0].Actor != "admin" || events[0].Category != "admin" ||
		!strings.HasSuffix(events[0].Data["server_public_key"], rotated) {
		t.Fatalf("events %+v", events)
	}

	// It needs the CSRF header like every other change, and a session.
	if r := b.do("POST", "/api/server/rotate-key", nil, csrfHeader, ""); r.status != http.StatusForbidden {
		t.Errorf("rotating without the header: status %d, want 403", r.status)
	}
	if r := newBrowser(t, srv).do("POST", "/api/server/rotate-key", nil); r.status != http.StatusUnauthorized {
		t.Errorf("rotating without a session: status %d, want 401", r.status)
	}
}
