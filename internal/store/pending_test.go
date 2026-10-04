package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/stuffam/drawbridge/internal/model"
)

func probation(at time.Time) *Probation {
	return &Probation{
		CreatedAt: at, Deadline: at.Add(time.Minute), Actor: "admin", Via: "web", SourceIP: "192.168.4.20",
		Changes: map[string]string{"listen_port": "51820 → 51999"},
	}
}

func always(p *Probation) func(before, after model.Settings) *Probation {
	return func(_, _ model.Settings) *Probation { return p }
}

func setPort(port uint16) func(*model.Settings) error {
	return func(st *model.Settings) error { st.ListenPort = port; return nil }
}

// A change on probation is saved with the settings it replaces, and ending the probation either
// way leaves the database consistent: kept, the new settings stay; undone, every setting is back.
func TestPendingChangeKeptOrUndone(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	for _, keep := range []bool{true, false} {
		t.Run(map[bool]string{true: "kept", false: "undone"}[keep], func(t *testing.T) {
			s := initialized(t)
			before, _ := s.Settings(ctx)
			next, p, err := s.UpdateSettingsWith(ctx, setPort(51999), always(probation(at)))
			if err != nil || p == nil || next.ListenPort != 51999 {
				t.Fatalf("update: %+v, %v, %v", next, p, err)
			}
			got, err := s.PendingApply(ctx)
			if err != nil || got == nil {
				t.Fatalf("PendingApply = %v, %v", got, err)
			}
			if !got.Deadline.Equal(at.Add(time.Minute)) || got.Actor != "admin" || got.Via != "web" ||
				got.SourceIP != "192.168.4.20" || got.Changes["listen_port"] != "51820 → 51999" {
				t.Fatalf("pending change %+v", got)
			}
			if !reflect.DeepEqual(got.Previous, before) {
				t.Fatalf("the settings it would restore:\n%+v\nwere:\n%+v", got.Previous, before)
			}

			resolved, err := s.ResolvePending(ctx, keep)
			if err != nil || resolved.Actor != "admin" {
				t.Fatalf("resolve: %+v, %v", resolved, err)
			}
			now, _ := s.Settings(ctx)
			if keep && now.ListenPort != 51999 {
				t.Errorf("a kept change left the port at %d", now.ListenPort)
			}
			if !keep && !reflect.DeepEqual(now, before) {
				t.Errorf("an undone change left:\n%+v\nwant:\n%+v", now, before)
			}
			if p, _ := s.PendingApply(ctx); p != nil {
				t.Error("the pending change is still there")
			}
			if _, err := s.ResolvePending(ctx, keep); !errors.Is(err, ErrNoPending) {
				t.Errorf("resolving twice: err %v, want ErrNoPending", err)
			}
		})
	}
}

// Nothing else changes the settings while one change waits: undoing it would lose the other.
func TestNoSettingsChangeWhileOneIsPending(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	if _, _, err := s.UpdateSettingsWith(ctx, setPort(51999), always(probation(time.Now()))); err != nil {
		t.Fatal(err)
	}
	mtu := func(st *model.Settings) error { st.MTU = 1380; return nil }
	if _, err := s.UpdateSettings(ctx, mtu); !errors.Is(err, ErrChangePending) {
		t.Fatalf("UpdateSettings: err %v, want ErrChangePending", err)
	}
	if _, _, err := s.UpdateSettingsWith(ctx, mtu, always(probation(time.Now()))); !errors.Is(err, ErrChangePending) {
		t.Fatalf("a second change on probation: err %v, want ErrChangePending", err)
	}
	if st, _ := s.Settings(ctx); st.MTU != model.DefaultMTU || st.ListenPort != 51999 {
		t.Fatalf("a refused change altered the settings: %+v", st)
	}
	if _, err := s.ResolvePending(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSettings(ctx, mtu); err != nil {
		t.Fatalf("after the change was kept: %v", err)
	}
}

// A change that decide doesn't put on probation, or that is refused, leaves no pending row.
func TestNoProbationUnlessDecidedAndSaved(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	if _, p, err := s.UpdateSettingsWith(ctx, setPort(51999), always(nil)); err != nil || p != nil {
		t.Fatalf("decide returned nil: %v, %v", p, err)
	}
	if p, _ := s.PendingApply(ctx); p != nil {
		t.Fatal("a change that wasn't put on probation left a pending row")
	}
	// An invalid change fails validation before decide is asked, and leaves nothing behind.
	bad := func(st *model.Settings) error { st.MTU = 10; return nil }
	asked := false
	_, _, err := s.UpdateSettingsWith(ctx, bad, func(_, _ model.Settings) *Probation { asked = true; return probation(time.Now()) })
	if !model.IsInvalid(err) || asked {
		t.Fatalf("err %v, asked %v: want a validation error without asking", err, asked)
	}
	if p, _ := s.PendingApply(ctx); p != nil {
		t.Fatal("an invalid change left a pending row")
	}
	if st, _ := s.Settings(ctx); st.MTU != model.DefaultMTU {
		t.Fatal("an invalid change was saved")
	}
}

// The server's private key is never in the database unsealed: the snapshot holds the settings
// without it, and the key sits in its own sealed column, which reads back as the old key.
func TestPendingSnapshotKeepsTheServerKeySealed(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Settings(ctx)
	newKey, _ := wgtypes.GeneratePrivateKey()
	rotate := func(st *model.Settings) error { st.PrivateKey = newKey; return nil }
	if _, _, err := s.UpdateSettingsWith(ctx, rotate, always(probation(time.Now()))); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	var keyEnc []byte
	if err := s.db.QueryRowContext(ctx, `SELECT previous, previous_key_enc FROM pending_apply`).Scan(&snapshot, &keyEnc); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{before.PrivateKey.String(), newKey.String()} {
		if strings.Contains(snapshot, secret) || strings.Contains(string(keyEnc), secret) {
			t.Fatalf("a private key is in the pending row unsealed")
		}
	}
	if strings.Contains(strings.ToLower(snapshot), "private") {
		t.Fatalf("the snapshot has a key field: %s", snapshot)
	}
	// It survives a reopen (a restart), and undoing it brings the old key back.
	_ = s.Close()
	again, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	p, err := again.PendingApply(ctx)
	if err != nil || p == nil || p.Previous.PrivateKey != before.PrivateKey {
		t.Fatalf("after reopening: %+v, %v", p, err)
	}
	if _, err := again.ResolvePending(ctx, false); err != nil {
		t.Fatal(err)
	}
	if now, _ := again.Settings(ctx); now.PrivateKey != before.PrivateKey {
		t.Fatal("undoing the change didn't bring the old key back")
	}
}

// The snapshot lists model.Settings' fields by hand. A field added to Settings and forgotten
// there would be silently lost by an undo, so fill every field and round-trip them.
func TestSnapshotCarriesEveryField(t *testing.T) {
	var st model.Settings
	v := reflect.ValueOf(&st).Elem()
	for i := range v.NumField() {
		f, field := v.Field(i), v.Type().Field(i)
		switch f.Interface().(type) {
		case string:
			f.SetString("x")
		case uint16:
			f.SetUint(7)
		case int:
			f.SetInt(7)
		case bool:
			f.SetBool(true)
		case netip.Prefix:
			f.Set(reflect.ValueOf(netip.MustParsePrefix("10.7.0.0/24")))
		case []netip.Prefix:
			f.Set(reflect.ValueOf([]netip.Prefix{netip.MustParsePrefix("10.7.0.0/24")}))
		case []netip.Addr:
			f.Set(reflect.ValueOf([]netip.Addr{netip.MustParseAddr("10.7.0.1")}))
		case wgtypes.Key:
			// Kept apart and sealed: not part of the snapshot.
		default:
			t.Fatalf("teach this test about %s (%s), and add it to settingsSnapshot", field.Name, field.Type)
		}
	}
	// Through JSON, which is how it is stored.
	data, err := json.Marshal(snapshotOf(st))
	if err != nil {
		t.Fatal(err)
	}
	var snap settingsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	got := snap.settings()
	got.PrivateKey = st.PrivateKey
	if !reflect.DeepEqual(got, st) {
		t.Fatalf("the snapshot lost something:\n got %+v\nwant %+v", got, st)
	}
	// An IPv6-less VPN (the zero Prefix) and empty lists come back as they went.
	var none settingsSnapshot
	data, _ = json.Marshal(snapshotOf(model.Settings{}))
	if err := json.Unmarshal(data, &none); err != nil || !reflect.DeepEqual(none.settings(), model.Settings{}) {
		t.Fatalf("the snapshot of empty settings: %+v, %v", none.settings(), err)
	}
}

// The upgrade adds the table without touching what's there.
func TestMigrationAddsPendingApplyToAnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	rollBackTo(t, s, 9)
	_ = s.Close()

	again, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if p, err := again.PendingApply(ctx); err != nil || p != nil {
		t.Fatalf("after the upgrade: %v, %v", p, err)
	}
	if cs, err := again.Clients(ctx); err != nil || len(cs) != 1 {
		t.Fatalf("clients after the upgrade: %v, %v", cs, err)
	}
	if _, _, err := again.UpdateSettingsWith(ctx, setPort(51999), always(probation(time.Now()))); err != nil {
		t.Fatal(err)
	}
}
