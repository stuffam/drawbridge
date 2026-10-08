package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/model"
)

func testSealer(t *testing.T, fill byte) *keys.Sealer {
	t.Helper()
	s, err := keys.NewSealer(bytes.Repeat([]byte{fill}, keys.SecretSize))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "drawbridge.db")
	s, err := Open(context.Background(), path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func initialized(t *testing.T) *Store {
	t.Helper()
	s, _ := openTest(t)
	if _, err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// undo is what takes back each migration after the first two: the statements that remove what
// it added. A new migration needs an entry here, and TestEveryMigrationCanBeRolledBack says so.
var undo = map[int][]string{
	3: {`ALTER TABLE server DROP COLUMN admin_allowed`},
	4: {`DROP TABLE client_sessions`},
	5: {`DROP TABLE traffic`},
	6: {`DROP TABLE dns_integration`},
	7: {`DROP TABLE dns_integration_clients`, `ALTER TABLE dns_integration DROP COLUMN sync_names`,
		`ALTER TABLE dns_integration DROP COLUMN enabled`},
	8:  {`DROP TABLE api_tokens`},
	9:  {`ALTER TABLE clients DROP COLUMN delivered_hash`, `ALTER TABLE clients DROP COLUMN delivered_at`},
	10: {`DROP TABLE pending_apply`},
	11: {`ALTER TABLE users DROP COLUMN totp_secret_enc`, `ALTER TABLE users DROP COLUMN totp_enabled_at`,
		`ALTER TABLE users DROP COLUMN totp_last_step`, `ALTER TABLE users DROP COLUMN recovery_codes_hash`},
}

// rollBackTo puts the database back as it was after the given migration, so a test can open it
// with a newer build and see the upgrade. migrate() tracks one high-water mark, not a set of
// applied versions, so every later migration has to go, not just the one under test.
func rollBackTo(t *testing.T, s *Store, version int) {
	t.Helper()
	ctx := context.Background()
	for v := len(undo) + 2; v > version; v-- {
		for _, stmt := range undo[v] {
			if _, err := s.db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("rolling back migration %d: %v", v, err)
			}
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version > ?`, version); err != nil {
		t.Fatal(err)
	}
}

func TestEveryMigrationCanBeRolledBack(t *testing.T) {
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	// The first two build the tables everything else changes, and nothing rolls them back.
	if want := len(files) - 2; len(undo) != want {
		t.Errorf("%d migrations after the first two, but %d entries in undo; add the new migration's", want, len(undo))
	}
	for v := 3; v <= len(files); v++ {
		if len(undo[v]) == 0 {
			t.Errorf("no way to roll back migration %d", v)
		}
	}
}

func TestSettingsBeforeInitialize(t *testing.T) {
	s, _ := openTest(t)
	if _, err := s.Settings(context.Background()); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("err %v, want ErrNotInitialized", err)
	}
}

func TestInitializeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	created, err := s.Initialize(ctx)
	if err != nil || !created {
		t.Fatalf("first Initialize: created %v, err %v", created, err)
	}
	first, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err = s.Initialize(ctx)
	if err != nil || created {
		t.Fatalf("second Initialize: created %v, err %v", created, err)
	}
	second, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.PrivateKey != second.PrivateKey || first.IPv6 != second.IPv6 {
		t.Fatal("a second Initialize changed the settings")
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("initial settings are invalid: %v", err)
	}
	if !first.IPv6.IsValid() {
		t.Fatal("no IPv6 subnet was generated")
	}
}

func TestReopenKeepsDataAndSchema(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	again, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, err := again.Client(ctx, ByName("phone")); err != nil {
		t.Fatalf("client lost after reopening: %v", err)
	}
	var migrations int
	if err := again.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if migrations != len(files) {
		t.Fatalf("%d migrations recorded, want %d (reopening mustn't re-run them)", migrations, len(files))
	}
}

func TestSecretsAreSealedAtRest(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := s.Settings(ctx)
	_ = s.Close()

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var serverKey, clientKey, psk []byte
	if err := raw.QueryRow(`SELECT private_key_enc FROM server`).Scan(&serverKey); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT private_key_enc, psk_enc FROM clients`).Scan(&clientKey, &psk); err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2][]byte{
		"server key": {serverKey, settings.PrivateKey[:]},
		"client key": {clientKey, c.PrivateKey[:]},
		"psk":        {psk, c.PresharedKey[:]},
	} {
		if bytes.Contains(pair[0], pair[1]) {
			t.Errorf("%s is stored in the clear", name)
		}
	}

	// The wrong secret key can't read the settings.
	wrong, err := Open(ctx, path, testSealer(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	if _, err := wrong.Settings(ctx); !errors.Is(err, keys.ErrOpen) {
		t.Fatalf("wrong secret key: err %v, want keys.ErrOpen", err)
	}
}

func TestAddClientAllocatesAndGeneratesKeys(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	settings, _ := s.Settings(ctx)

	a, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddClient(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if a.IPv4 != netip.MustParseAddr("10.8.0.2") || b.IPv4 != netip.MustParseAddr("10.8.0.3") {
		t.Fatalf("IPv4: %s, %s", a.IPv4, b.IPv4)
	}
	if !settings.IPv6.Contains(a.IPv6) || a.IPv6.As16()[15] != 0x02 {
		t.Fatalf("IPv6 %s doesn't mirror host 2 in %s", a.IPv6, settings.IPv6)
	}
	if a.PrivateKey == nil || a.PrivateKey.PublicKey() != a.PublicKey {
		t.Fatal("the stored key pair doesn't match")
	}
	if a.PresharedKey == b.PresharedKey || a.PublicKey == b.PublicKey {
		t.Fatal("two clients share key material")
	}
	if !a.Enabled {
		t.Fatal("a new client should be enabled")
	}

	got, err := s.Client(ctx, ByName("PHONE"))
	if err != nil {
		t.Fatalf("case-insensitive lookup: %v", err)
	}
	if got.ID != a.ID || got.PresharedKey != a.PresharedKey || *got.PrivateKey != *a.PrivateKey {
		t.Fatal("the client read back differs from the one created")
	}

	all, err := s.Clients(ctx)
	if err != nil || len(all) != 2 || all[0].Name != "phone" {
		t.Fatalf("Clients() = %v, %v", all, err)
	}
}

func TestAddClientRejectsDuplicateAndInvalidNames(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	if _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddClient(ctx, "Phone"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name: err %v, want ErrNameTaken", err)
	}
	if _, err := s.AddClient(ctx, "bad\nname"); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestAddClientReusesFreedAddress(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	a, _ := s.AddClient(ctx, "a")
	if _, err := s.AddClient(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteClient(ctx, ByName("a")); err != nil {
		t.Fatal(err)
	}
	c, err := s.AddClient(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if c.IPv4 != a.IPv4 || c.IPv6 != a.IPv6 {
		t.Fatalf("got %s/%s, want the freed %s/%s", c.IPv4, c.IPv6, a.IPv4, a.IPv6)
	}
}

func TestSetEnabledAndDelete(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	if _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	c, err := s.SetEnabled(ctx, ByName("phone"), false)
	if err != nil || c.Enabled {
		t.Fatalf("pause: %+v, %v", c, err)
	}
	if got, _ := s.Client(ctx, ByName("phone")); got.Enabled {
		t.Fatal("pause wasn't saved")
	}
	if c, err = s.SetEnabled(ctx, ByName("phone"), true); err != nil || !c.Enabled {
		t.Fatalf("resume: %+v, %v", c, err)
	}
	if _, err := s.DeleteClient(ctx, ByName("phone")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Client(ctx, ByName("phone")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err %v, want ErrNotFound", err)
	}
	if _, err := s.SetEnabled(ctx, ByName("ghost"), true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown client: err %v, want ErrNotFound", err)
	}
	if _, err := s.DeleteClient(ctx, ByName("ghost")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown client: err %v, want ErrNotFound", err)
	}
}

func TestUpdateSettings(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	updated, err := s.UpdateSettings(ctx, func(st *model.Settings) error {
		st.EndpointHost = "vpn.example.com"
		st.MTU = 1412
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Settings(ctx)
	if got.EndpointHost != "vpn.example.com" || got.MTU != 1412 || updated.MTU != 1412 {
		t.Fatalf("got %+v", got)
	}

	// Invalid settings aren't saved.
	if _, err := s.UpdateSettings(ctx, func(st *model.Settings) error { st.MTU = 100; return nil }); err == nil {
		t.Fatal("invalid MTU accepted")
	}
	if got, _ := s.Settings(ctx); got.MTU != 1412 {
		t.Fatalf("a rejected update changed the MTU to %d", got.MTU)
	}

	// An error from fn aborts the update.
	boom := errors.New("boom")
	if _, err := s.UpdateSettings(ctx, func(st *model.Settings) error { st.MTU = 1300; return boom }); !errors.Is(err, boom) {
		t.Fatalf("err %v, want boom", err)
	}
	if got, _ := s.Settings(ctx); got.MTU != 1412 {
		t.Fatal("an aborted update was saved")
	}
}

func TestSubnetsCantChangeWithClients(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	change := func(st *model.Settings) error {
		st.IPv4 = netip.MustParsePrefix("10.9.0.0/24")
		return nil
	}
	if _, err := s.UpdateSettings(ctx, change); err != nil {
		t.Fatalf("without clients: %v", err)
	}
	if _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSettings(ctx, func(st *model.Settings) error {
		st.IPv4 = netip.MustParsePrefix("10.10.0.0/24")
		return nil
	}); !errors.Is(err, ErrHasClients) {
		t.Fatalf("with a client: err %v, want ErrHasClients", err)
	}
}

func TestClientRefsAndRename(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	phone, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddClient(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Client(ctx, ByID(phone.ID)); err != nil || got.Name != "phone" {
		t.Fatalf("Client(ByID) = %+v, %v", got, err)
	}
	if _, err := s.Client(ctx, ByID("phone")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a name used as an ID: err %v, want ErrNotFound", err)
	}

	renamed, err := s.RenameClient(ctx, ByID(phone.ID), "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Pixel" || renamed.ID != phone.ID || renamed.IPv4 != phone.IPv4 {
		t.Fatalf("renamed %+v", renamed)
	}
	// Changing only the case of its own name is fine.
	if _, err := s.RenameClient(ctx, ByName("pixel"), "PIXEL"); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
	if _, err := s.RenameClient(ctx, ByName("pixel"), "Laptop"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename onto another client: err %v, want ErrNameTaken", err)
	}
	if _, err := s.RenameClient(ctx, ByName("pixel"), "bad\nname"); !model.IsInvalid(err) {
		t.Fatalf("invalid name: err %v, want an InvalidError", err)
	}
	if _, err := s.RenameClient(ctx, ByName("ghost"), "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown client: err %v", err)
	}
}

func TestTimestampsCompareAsText(t *testing.T) {
	a := formatTime(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC))
	b := formatTime(time.Date(2026, 9, 26, 12, 0, 0, 500_000_000, time.UTC))
	c := formatTime(time.Date(2026, 9, 26, 7, 0, 1, 0, time.FixedZone("EST", -5*3600)))
	if a >= b || b >= c || len(a) != len(b) {
		t.Fatalf("%s, %s, %s: want fixed-width UTC times that sort as text", a, b, c)
	}
	if _, err := time.Parse(time.RFC3339Nano, a); err != nil {
		t.Fatal(err)
	}
}

func TestAdminAllowedRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	if got, _ := s.Settings(ctx); len(got.AdminAllowed) != 0 {
		t.Fatalf("a new server has extra admin sources: %v", got.AdminAllowed)
	}
	want := []netip.Prefix{netip.MustParsePrefix("100.64.10.0/24")}
	if _, err := s.UpdateSettings(ctx, func(st *model.Settings) error { st.AdminAllowed = want; return nil }); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings(ctx); !slices.Equal(got.AdminAllowed, want) {
		t.Fatalf("got %v, want %v", got.AdminAllowed, want)
	}
	// A public range is refused and nothing is saved.
	bad := []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}
	if _, err := s.UpdateSettings(ctx, func(st *model.Settings) error { st.AdminAllowed = bad; return nil }); err == nil {
		t.Fatal("a public range was saved")
	}
	if got, _ := s.Settings(ctx); !slices.Equal(got.AdminAllowed, want) {
		t.Fatalf("a rejected update changed the sources to %v", got.AdminAllowed)
	}
	if _, err := s.UpdateSettings(ctx, func(st *model.Settings) error { st.AdminAllowed = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings(ctx); len(got.AdminAllowed) != 0 {
		t.Fatalf("clearing left %v", got.AdminAllowed)
	}
}

// A database from before the admin_allowed column exists keeps its server row when it's
// opened by a newer build, and starts with no extra sources.
func TestMigrationAddsAdminAllowedToAnExistingServer(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSettings(ctx, func(st *model.Settings) error { st.EndpointHost = "vpn.example.com"; return nil }); err != nil {
		t.Fatal(err)
	}
	rollBackTo(t, s, 2)
	_ = s.Close()

	again, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got, err := again.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.EndpointHost != "vpn.example.com" || len(got.AdminAllowed) != 0 {
		t.Fatalf("after the upgrade: endpoint %q, sources %v", got.EndpointHost, got.AdminAllowed)
	}
	if sessions, err := again.CurrentClientSessions(ctx); err != nil || len(sessions) != 0 {
		t.Fatalf("sessions %v, err %v, want none", sessions, err)
	}
}

// A client that exists before config tracking comes through the upgrade with no baseline: it
// isn't flagged, and the first config the admin hands out sets one.
func TestMigrationAddsConfigTrackingToExistingClients(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	added, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	rollBackTo(t, s, 8)
	_ = s.Close()

	again, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	c, err := again.Client(ctx, ByName("phone"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != added.ID || c.ConfigHash != "" || !c.ConfigDeliveredAt.IsZero() {
		t.Fatalf("after the upgrade: %+v", c)
	}
	if err := again.MarkConfigDelivered(ctx, c.ID, "abc"); err != nil {
		t.Fatal(err)
	}
	if c, _ = again.Client(ctx, ByID(c.ID)); c.ConfigHash != "abc" || c.ConfigDeliveredAt.IsZero() {
		t.Fatalf("after marking: %+v", c)
	}
}

func TestMarkConfigDelivered(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	c, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfigHash != "" || !c.ConfigDeliveredAt.IsZero() {
		t.Fatalf("a new client has a config handed out: %+v", c)
	}
	if err := s.MarkConfigDelivered(ctx, c.ID, "first"); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	s.now = func() time.Time { return later }
	if err := s.MarkConfigDelivered(ctx, c.ID, "second"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Client(ctx, ByID(c.ID))
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigHash != "second" || !got.ConfigDeliveredAt.Equal(later) {
		t.Fatalf("hash %q delivered %v, want the second at %v", got.ConfigHash, got.ConfigDeliveredAt, later)
	}
	if !got.UpdatedAt.Equal(c.UpdatedAt) {
		t.Error("handing out a config counted as changing the client")
	}
	if err := s.MarkConfigDelivered(ctx, "no-such-id", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown client: err %v, want ErrNotFound", err)
	}
}

func TestRotateClientKeys(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	before, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.AddClient(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkConfigDelivered(ctx, before.ID, "handed-out"); err != nil {
		t.Fatal(err)
	}

	after, err := s.RotateClientKeys(ctx, ByName("phone"))
	if err != nil {
		t.Fatal(err)
	}
	// What comes back is what was stored, including the secrets (sealed on the way in).
	stored, err := s.Client(ctx, ByID(before.ID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.PublicKey != after.PublicKey || *stored.PrivateKey != *after.PrivateKey || stored.PresharedKey != after.PresharedKey {
		t.Fatal("the rotated keys that came back aren't the ones stored")
	}
	if stored.PublicKey == before.PublicKey || *stored.PrivateKey == *before.PrivateKey || stored.PresharedKey == before.PresharedKey {
		t.Fatal("rotating left one of the old keys in place")
	}
	if got := stored.PrivateKey.PublicKey(); got != stored.PublicKey {
		t.Fatalf("the new public key %v doesn't belong to the new private key (%v)", stored.PublicKey, got)
	}
	// Everything else about the client stays, and rotating doesn't pretend the new config was
	// handed out.
	if stored.IPv4 != before.IPv4 || stored.IPv6 != before.IPv6 || stored.Name != "phone" || !stored.Enabled {
		t.Fatalf("rotating changed more than the keys: %+v", stored)
	}
	if stored.ConfigHash != "handed-out" {
		t.Errorf("config hash %q, want the old one kept so the config reads as outdated", stored.ConfigHash)
	}
	// Another client is untouched.
	if o, _ := s.Client(ctx, ByID(other.ID)); o.PublicKey != other.PublicKey || o.PresharedKey != other.PresharedKey {
		t.Fatal("rotating one client's keys changed another's")
	}
	if _, err := s.RotateClientKeys(ctx, ByName("nobody")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown client: err %v, want ErrNotFound", err)
	}
}

// A client the server doesn't keep a private key for can't be handed new keys: only the client
// could make a key pair of its own.
func TestRotateClientKeysNeedsThePrivateKey(t *testing.T) {
	ctx := context.Background()
	s := initialized(t)
	c, err := s.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE clients SET private_key_enc = NULL WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateClientKeys(ctx, ByID(c.ID)); !errors.Is(err, ErrNoClientKey) {
		t.Fatalf("err %v, want ErrNoClientKey", err)
	}
	if got, _ := s.Client(ctx, ByID(c.ID)); got.PublicKey != c.PublicKey {
		t.Fatal("a refused rotation changed the public key")
	}
}

// withUmask runs the rest of a test under a umask that doesn't hide anything, which is what a
// daemon started by hand might have, and puts the old one back after.
func withUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestOpenMakesAPrivateDatabase(t *testing.T) {
	// The units' UMask=0077 isn't what keeps the file private: under a umask of 0 it still is.
	withUmask(t, 0)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddClient(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
	// While the store is open SQLite keeps -wal and -shm beside the file; they take its mode.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if got := mode(t, p); got != 0o600 {
			t.Errorf("%s is %v, want -rw-------", filepath.Base(p), got)
		}
	}
}

func TestOpenTightensAWiderDatabase(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// A database an earlier build made under another umask: the file and its WAL files are open.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	again, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	_ = s.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err != nil {
			continue // SQLite removes them when the last connection closes.
		}
		if got := mode(t, p); got != 0o600 {
			t.Errorf("%s is %v after Open, want -rw-------", filepath.Base(p), got)
		}
	}
	if got := mode(t, path); got != 0o600 {
		t.Errorf("the database is %v after Open, want -rw-------", got)
	}
}

func TestOpenKeepsAnOwnerOnlyModeAndFailsOnAMissingDirectory(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	_ = s.Close()
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := makePrivate(path); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, path); got != 0o400 {
		t.Errorf("a file with no group or other bits was changed to %v", got)
	}
	_, err := Open(ctx, filepath.Join(t.TempDir(), "no", "such", "dir", "db"), testSealer(t, 1))
	if err == nil {
		t.Fatal("opened a database in a directory that doesn't exist")
	}
}
