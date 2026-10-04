package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/store"
)

func listenPort(t *testing.T, s *Service) int {
	t.Helper()
	dev, err := s.WG.Device("wg0")
	if err != nil {
		t.Fatal(err)
	}
	return dev.ListenPort
}

func storedPort(t *testing.T, s *Service) uint16 {
	t.Helper()
	st, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.ListenPort
}

func portPatch(port uint16, safe bool) SettingsPatch {
	return SettingsPatch{ListenPort: &port, SafeApply: safe}
}

func TestNeedsConfirmation(t *testing.T) {
	base := model.Settings{ListenPort: 51820, MTU: 1420, Keepalive: 25, ClientIsolation: true,
		AdminAllowed: []netip.Prefix{netip.MustParsePrefix("100.64.10.0/24"), netip.MustParsePrefix("192.168.9.0/24")}}
	for name, tc := range map[string]struct {
		change func(*model.Settings)
		want   bool
	}{
		"nothing":                 {func(*model.Settings) {}, false},
		"listen port":             {func(s *model.Settings) { s.ListenPort = 51999 }, true},
		"an admin source removed": {func(s *model.Settings) { s.AdminAllowed = s.AdminAllowed[:1] }, true},
		"every admin source gone": {func(s *model.Settings) { s.AdminAllowed = nil }, true},
		"one swapped for another": {func(s *model.Settings) {
			s.AdminAllowed = []netip.Prefix{s.AdminAllowed[0], netip.MustParsePrefix("10.9.0.0/16")}
		}, true},
		"an admin source added": {func(s *model.Settings) { s.AdminAllowed = append(s.AdminAllowed, netip.MustParsePrefix("10.9.0.0/16")) }, false},
		"MTU":                   {func(s *model.Settings) { s.MTU = 1380 }, false},
		"keepalive":             {func(s *model.Settings) { s.Keepalive = 0 }, false},
		"client isolation":      {func(s *model.Settings) { s.ClientIsolation = false }, false},
		"endpoint":              {func(s *model.Settings) { s.EndpointHost = "vpn.example.com"; s.EndpointPort = 443 }, false},
		"DNS":                   {func(s *model.Settings) { s.DNS = []netip.Addr{netip.MustParseAddr("9.9.9.9")} }, false},
	} {
		after := base
		after.AdminAllowed = append([]netip.Prefix(nil), base.AdminAllowed...)
		tc.change(&after)
		if got := needsConfirmation(base, after); got != tc.want {
			t.Errorf("%s: needsConfirmation = %v, want %v", name, got, tc.want)
		}
	}
}

// The whole life of a change that's kept.
func TestSafeApplyKeptChange(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")

	_, applied, err := s.UpdateSettings(ctx, portPatch(51999, true))
	if err != nil || applied.Err != nil {
		t.Fatalf("update: %v, %v", err, applied.Err)
	}
	// It's applied at once, and waiting.
	if listenPort(t, s) != 51999 || storedPort(t, s) != 51999 {
		t.Fatalf("the change wasn't applied: kernel %d, stored %d", listenPort(t, s), storedPort(t, s))
	}
	if applied.Pending == nil || applied.Pending.Changes["listen_port"] != "51820 → 51999" ||
		applied.Pending.Actor != "admin" || applied.Pending.Via != ViaWeb {
		t.Fatalf("pending in the response: %+v", applied.Pending)
	}
	if want := s.now().Add(DefaultSafeApplyWindow); !applied.Pending.Deadline.Equal(want) {
		t.Errorf("deadline %v, want %v", applied.Pending.Deadline, want)
	}
	if st, _ := s.Status(ctx); st.Pending == nil || !st.Pending.Deadline.Equal(applied.Pending.Deadline) {
		t.Errorf("Status.Pending = %+v", st.Pending)
	}
	if p, err := s.PendingChange(ctx); err != nil || p == nil || p.Changes["listen_port"] != "51820 → 51999" {
		t.Errorf("PendingChange = %+v, %v", p, err)
	}

	kept, err := s.ConfirmChange(ctx)
	if err != nil || kept.Changes["listen_port"] != "51820 → 51999" {
		t.Fatalf("confirm: %+v, %v", kept, err)
	}
	if listenPort(t, s) != 51999 || storedPort(t, s) != 51999 {
		t.Fatal("keeping the change undid it")
	}
	if p, _ := s.PendingChange(ctx); p != nil {
		t.Error("the change is still pending after it was kept")
	}
	if st, _ := s.Status(ctx); st.Pending != nil {
		t.Error("Status still shows it")
	}
	// A kept change isn't undone later, and keeping it again says there's nothing to keep.
	s.Now = func() time.Time { return s.now().Add(time.Hour) }
	s.expirePending(ctx)
	if storedPort(t, s) != 51999 {
		t.Fatal("a kept change was undone when its time ran out")
	}
	if _, err := s.ConfirmChange(ctx); !errors.Is(err, store.ErrNoPending) {
		t.Errorf("confirming twice: err %v, want ErrNoPending", err)
	}

	if got := strings.Join(kinds(t, s), " "); got != "server.settings_changed server.settings_kept" {
		t.Errorf("events %s", got)
	}
	events, _ := s.Events(ctx, store.EventFilter{})
	changed, keptEvent := events[1], events[0]
	if changed.Data["listen_port"] != "51820 → 51999" || changed.Data["waiting_to_be_kept"] != "1m0s" {
		t.Errorf("settings_changed data %v", changed.Data)
	}
	if keptEvent.Actor != "admin" || keptEvent.Via != ViaWeb || keptEvent.Category != CategoryAdmin ||
		keptEvent.Data["listen_port"] != "51820 → 51999" {
		t.Errorf("settings_kept %+v", keptEvent)
	}
}

// A change nobody keeps is undone, in the kernel and in the database, by the daemon.
func TestSafeApplyUndoesAChangeThatIsNotKept(t *testing.T) {
	s, clk := newTestService(t)
	ctx := web(context.Background(), "admin")
	if _, _, err := s.UpdateSettings(ctx, portPatch(51999, true)); err != nil {
		t.Fatal(err)
	}

	// Not yet.
	clk.advance(DefaultSafeApplyWindow - time.Second)
	s.expirePending(context.Background())
	if listenPort(t, s) != 51999 {
		t.Fatal("the change was undone before its time was up")
	}

	clk.advance(time.Second)
	s.expirePending(context.Background())
	if listenPort(t, s) != 51820 || storedPort(t, s) != 51820 {
		t.Fatalf("the change wasn't undone: kernel %d, stored %d", listenPort(t, s), storedPort(t, s))
	}
	if p, _ := s.PendingChange(ctx); p != nil {
		t.Error("the change is still pending")
	}
	events, _ := s.Events(ctx, store.EventFilter{Kind: "server.settings_expired"})
	if len(events) != 1 {
		t.Fatalf("%d expired events", len(events))
	}
	if undone, _ := s.Events(ctx, store.EventFilter{Kind: "server.settings_undone"}); len(undone) != 0 {
		t.Fatalf("a change that ran out was recorded as undone by someone: %+v", undone)
	}
	e := events[0]
	// The daemon did it, not the admin who made the change.
	if e.Actor != "drawbridge" || e.Via != ViaSystem || e.Category != CategorySystem ||
		e.Data["listen_port"] != "51820 → 51999" || e.Data["reason"] != "not kept within 1m0s" {
		t.Errorf("event %+v", e)
	}
	// And the settings can change again.
	if _, _, err := s.UpdateSettings(ctx, portPatch(51888, false)); err != nil {
		t.Fatalf("after the undo: %v", err)
	}
}

// A click that comes after the deadline, before the daemon's next look, doesn't keep the change.
func TestSafeApplyConfirmAfterTheDeadlineUndoes(t *testing.T) {
	s, clk := newTestService(t)
	ctx := web(context.Background(), "admin")
	if _, _, err := s.UpdateSettings(ctx, portPatch(51999, true)); err != nil {
		t.Fatal(err)
	}
	clk.advance(DefaultSafeApplyWindow)
	if _, err := s.ConfirmChange(ctx); !errors.Is(err, ErrChangeExpired) {
		t.Fatalf("err %v, want ErrChangeExpired", err)
	}
	if listenPort(t, s) != 51820 || storedPort(t, s) != 51820 {
		t.Fatal("the late confirmation kept the change")
	}
	events, _ := s.Events(ctx, store.EventFilter{Kind: "server.settings_expired"})
	if len(events) != 1 || events[0].Actor != "drawbridge" {
		t.Fatalf("events %+v: the daemon undid it, not the admin who clicked late", events)
	}
	for _, k := range kinds(t, s) {
		if k == "server.settings_kept" {
			t.Fatal("a late confirmation was recorded as kept")
		}
	}
}

func TestSafeApplyRevertNow(t *testing.T) {
	s, _ := newTestService(t)
	web1 := web(context.Background(), "admin")
	if _, _, err := s.UpdateSettings(web1, portPatch(51999, true)); err != nil {
		t.Fatal(err)
	}
	cli := WithActor(context.Background(), Actor{Name: "root", Via: ViaCLI})
	undone, applied, err := s.RevertChange(cli)
	if err != nil || applied.Err != nil || undone.Changes["listen_port"] != "51820 → 51999" {
		t.Fatalf("revert: %+v, %+v, %v", undone, applied, err)
	}
	if listenPort(t, s) != 51820 || storedPort(t, s) != 51820 {
		t.Fatal("the change wasn't undone")
	}
	events, _ := s.Events(cli, store.EventFilter{Kind: "server.settings_undone"})
	if len(events) != 1 || events[0].Actor != "root" || events[0].Via != ViaCLI || events[0].Category != CategoryAdmin {
		t.Fatalf("events %+v", events)
	}
	if _, _, err := s.RevertChange(cli); !errors.Is(err, store.ErrNoPending) {
		t.Errorf("reverting twice: err %v, want ErrNoPending", err)
	}
}

// Only a change that can cut the admin off waits, and only when it's asked to.
func TestSafeApplyOnlyForRiskyChangesThatAskForIt(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")

	mtu := 1380
	_, applied, err := s.UpdateSettings(ctx, SettingsPatch{MTU: &mtu, SafeApply: true})
	if err != nil || applied.Pending != nil {
		t.Fatalf("an MTU change: pending %+v, err %v", applied.Pending, err)
	}
	// The CLI applies at once, risky or not.
	_, applied, err = s.UpdateSettings(ctx, portPatch(51999, false))
	if err != nil || applied.Pending != nil || listenPort(t, s) != 51999 {
		t.Fatalf("a risky change without SafeApply: pending %+v, err %v", applied.Pending, err)
	}
	if p, _ := s.PendingChange(ctx); p != nil {
		t.Fatal("something is pending")
	}
	// A patch that changes nothing has nothing to wait for.
	_, applied, err = s.UpdateSettings(ctx, portPatch(51999, true))
	if err != nil || applied.Pending != nil {
		t.Fatalf("an unchanged port: pending %+v, err %v", applied.Pending, err)
	}
}

func TestSafeApplyAdminSources(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	sources := []netip.Prefix{netip.MustParsePrefix("100.64.10.0/24")}
	_, applied, err := s.UpdateSettings(ctx, SettingsPatch{AdminAllowed: &sources, SafeApply: true})
	if err != nil || applied.Pending != nil {
		t.Fatalf("adding a source: pending %+v, err %v", applied.Pending, err)
	}
	none := []netip.Prefix{}
	_, applied, err = s.UpdateSettings(ctx, SettingsPatch{AdminAllowed: &none, SafeApply: true})
	if err != nil || applied.Pending == nil {
		t.Fatalf("removing a source: pending %+v, err %v", applied.Pending, err)
	}
	if st, _ := s.Settings(ctx); len(st.AdminAllowed) != 0 {
		t.Fatalf("the removal wasn't applied: %v", st.AdminAllowed)
	}
	// Not kept: the source comes back.
	if _, _, err := s.RevertChange(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Settings(ctx); len(st.AdminAllowed) != 1 || st.AdminAllowed[0] != sources[0] {
		t.Fatalf("after the undo: %v", st.AdminAllowed)
	}
}

// While a change waits, nothing else about the settings changes, and clients still do.
func TestSafeApplyBlocksOtherSettingsChanges(t *testing.T) {
	s, _ := newTestService(t)
	ctx := web(context.Background(), "admin")
	if _, _, err := s.UpdateSettings(ctx, portPatch(51999, true)); err != nil {
		t.Fatal(err)
	}
	mtu := 1380
	if _, _, err := s.UpdateSettings(ctx, SettingsPatch{MTU: &mtu}); !errors.Is(err, store.ErrChangePending) {
		t.Fatalf("err %v, want ErrChangePending", err)
	}
	if _, _, err := s.UpdateSettings(ctx, portPatch(52000, true)); !errors.Is(err, store.ErrChangePending) {
		t.Fatalf("a second risky change: err %v, want ErrChangePending", err)
	}
	if listenPort(t, s) != 51999 {
		t.Fatal("a refused change altered the kernel")
	}
	if _, _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatalf("adding a client while a change waits: %v", err)
	}
	// Undoing it doesn't touch the client.
	if _, _, err := s.RevertChange(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Client(ctx, store.ByName("phone")); err != nil {
		t.Fatal(err)
	}
}

// The daemon restarts, or the host reboots, inside the window: the new daemon finds the change
// waiting and undoes it when its time is up.
func TestSafeApplySurvivesARestart(t *testing.T) {
	s, clk := newTestService(t)
	ctx := web(context.Background(), "admin")
	if _, _, err := s.UpdateSettings(ctx, portPatch(51999, true)); err != nil {
		t.Fatal(err)
	}

	again := &Service{Store: s.Store, Rec: s.Rec, WG: s.WG, Now: clk.now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if again.cachedPending() != nil {
		t.Fatal("a new service already knows about the change")
	}
	again.loadPending(context.Background())
	if p := again.cachedPending(); p == nil || p.Changes["listen_port"] != "51820 → 51999" {
		t.Fatalf("after the restart: %+v", p)
	}
	again.expirePending(context.Background())
	if listenPort(t, s) != 51999 {
		t.Fatal("the restart undid the change early")
	}
	clk.advance(DefaultSafeApplyWindow)
	again.expirePending(context.Background())
	if listenPort(t, s) != 51820 || storedPort(t, s) != 51820 {
		t.Fatalf("the restarted daemon didn't undo it: kernel %d, stored %d", listenPort(t, s), storedPort(t, s))
	}
}

func TestSafeApplyWindowIsConfigurable(t *testing.T) {
	s, clk := newTestService(t)
	s.SafeApplyWindow = 5 * time.Second
	ctx := web(context.Background(), "admin")
	_, applied, err := s.UpdateSettings(ctx, portPatch(51999, true))
	if err != nil || !applied.Pending.Deadline.Equal(clk.t.Add(5*time.Second)) {
		t.Fatalf("pending %+v, err %v", applied.Pending, err)
	}
	clk.advance(5 * time.Second)
	s.expirePending(ctx)
	if storedPort(t, s) != 51820 {
		t.Fatal("not undone after the configured window")
	}
	events, _ := s.Events(ctx, store.EventFilter{Kind: "server.settings_expired"})
	if len(events) != 1 || events[0].Data["reason"] != "not kept within 5s" {
		t.Fatalf("events %+v", events)
	}
}

func TestApplyDryRunAndReal(t *testing.T) {
	s, _ := newTestService(t)
	ctx := WithActor(context.Background(), Actor{Name: "root", Via: ViaCLI})
	if res, err := s.Apply(ctx, false); err != nil || len(res.Changes) != 0 {
		t.Fatalf("nothing has drifted: %+v, %v", res, err)
	}
	if got := strings.Join(kinds(t, s), " "); got != "" {
		t.Fatalf("an apply that changed nothing was recorded: %s", got)
	}

	// Drift the kernel by hand.
	if err := s.WG.SetMTU("wg0", 1500); err != nil {
		t.Fatal(err)
	}
	planned, err := s.Apply(ctx, true)
	if err != nil || len(planned.Changes) != 1 || !strings.Contains(planned.Changes[0], "MTU") {
		t.Fatalf("dry run: %+v, %v", planned, err)
	}
	if dev, _ := s.WG.Device("wg0"); dev.MTU != 1500 {
		t.Fatal("a dry run changed the MTU")
	}
	if got := strings.Join(kinds(t, s), " "); got != "" {
		t.Fatalf("a dry run was recorded: %s", got)
	}

	done, err := s.Apply(ctx, false)
	if err != nil || strings.Join(done.Changes, ";") != strings.Join(planned.Changes, ";") {
		t.Fatalf("apply: %+v, %v; the plan said %v", done, err, planned.Changes)
	}
	if dev, _ := s.WG.Device("wg0"); dev.MTU != model.DefaultMTU {
		t.Fatalf("MTU %d after apply", dev.MTU)
	}
	events, _ := s.Events(ctx, store.EventFilter{Kind: "tunnel.applied"})
	if len(events) != 1 || events[0].Actor != "root" || events[0].Via != ViaCLI || events[0].Category != CategoryAdmin ||
		!strings.Contains(events[0].Data["changes"], "MTU") {
		t.Fatalf("events %+v", events)
	}
}
