package storetest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/stuffam/drawbridge/internal/auth"
	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/store"
)

// feature is what one migration added: its rows, and the check that reads them back. since is
// the schema version that introduced it, so a seed may use only the tables and columns that
// version has. A check runs on a database at any later schema too, and so must leave alone what
// later features change (the listen port is the pending change's business, not the server's).
type feature struct {
	name    string
	since   int
	profile Profile // empty for both
	seed    func(*seeder)
	check   func(*checker)
}

func (f feature) applies(p Profile) bool { return f.profile == "" || f.profile == p }

// What the fixtures hold. Keys are fixed bytes, so Build and Verify agree without sharing
// state: any 32 bytes are a WireGuard key.
func fixedKey(b byte) wgtypes.Key {
	var k wgtypes.Key
	for i := range k {
		k[i] = b
	}
	return k
}

var (
	serverKey  = fixedKey(0x11)
	phoneKey   = fixedKey(0x21)
	phonePSK   = fixedKey(0x31)
	laptopKey  = fixedKey(0x22)
	routerKey  = fixedKey(0x23) // the router's private key never reached the server
	totpSecret = []byte("12345678901234567890")
)

// Client and account IDs are 32 hex characters, as the store makes them.
const (
	phoneID  = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	laptopID = "b2c3d4e5f60718293a4b5c6d7e8f90a1"
	routerID = "c3d4e5f60718293a4b5c6d7e8f90a1b2"
	adminID  = "d4e5f60718293a4b5c6d7e8f90a1b2c3"
	// goneID is a client that was deleted.
	goneID = "f60718293a4b5c6d7e8f90a1b2c3d4e5"
)

// Times come in the two formats the store has written: M1's shorter RFC 3339, and the fixed
// width one it settled on (store.timeFormat). Rows from the first releases carry the first.
const (
	early = "2026-09-26T10:15:30Z"
	later = "2026-10-01T08:30:00.000000Z"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

// The account's password is "correct horse battery staple". The hash is a literal, made once with
// parameters that aren't auth.DefaultParams (and cheap, so the matrix stays fast): whatever
// parameters a build hashes with, a Verify has to read them from the string, so a later change to
// the defaults must still open this.
const (
	adminPassword = "correct horse battery staple"
	adminHash     = "$argon2id$v=19$m=1024,t=1,p=1$ZHJhd2JyaWRnZS1maXhlZA$RP0K5sTWytWiUHt0eD4sxL0tM3s22nfV9CE2nOnkzDY"
)

var (
	sessionToken = sha256.Sum256([]byte("a session token"))
	apiToken     = sha256.Sum256([]byte("an API token"))
)

// The purposes secrets are sealed under are part of what's on disk: a change to one makes
// every old secret unreadable, so they're written out here and not borrowed from the store.
const (
	serverKeyPurpose = "server/private-key"
	dnsPurpose       = "dns-integration/password"
	setupPurpose     = "setup-token"
)

func clientKeyPurpose(id string) string { return "client/" + id + "/private-key" }
func clientPSKPurpose(id string) string { return "client/" + id + "/preshared-key" }
func totpPurpose(id string) string      { return "user/" + id + "/totp" }

type seeder struct {
	t       testing.TB
	db      *sql.DB
	sealer  *keys.Sealer
	feature string
}

func (s *seeder) exec(query string, args ...any) {
	s.t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		s.t.Fatalf("seeding %s: %v", s.feature, err)
	}
}

func (s *seeder) seal(plain []byte, purpose string) []byte { return s.sealer.Seal(plain, purpose) }
func (s *seeder) sealKey(k wgtypes.Key, purpose string) []byte {
	return s.sealer.SealKey(k, purpose)
}

type checker struct {
	t       testing.TB
	ctx     context.Context
	s       *store.Store
	feature string
	opts    options
}

// must stops the test when a read fails: nothing after it means anything.
func (c *checker) must(err error, what string) {
	c.t.Helper()
	if err != nil {
		c.t.Fatalf("%s: reading %s through this build: %v", c.feature, what, err)
	}
}

func (c *checker) eq(what string, got, want any) {
	c.t.Helper()
	if !reflect.DeepEqual(got, want) {
		c.t.Errorf("%s: %s is %+v, want %+v", c.feature, what, got, want)
	}
}

func (c *checker) when(what string, got time.Time, want string) {
	c.t.Helper()
	if !got.Equal(at(want)) {
		c.t.Errorf("%s: %s is %v, want %s", c.feature, what, got, want)
	}
}

func (c *checker) addrs(what string, got []netip.Addr, want ...string) {
	c.t.Helper()
	var w []netip.Addr
	for _, s := range want {
		w = append(w, netip.MustParseAddr(s))
	}
	c.eq(what, got, w)
}

func (c *checker) prefixes(what string, got []netip.Prefix, want ...string) {
	c.t.Helper()
	var w []netip.Prefix
	for _, s := range want {
		w = append(w, netip.MustParsePrefix(s))
	}
	c.eq(what, got, w)
}

var features = []feature{
	{
		name: "the server's settings", since: 1,
		seed: func(s *seeder) {
			s.exec(`INSERT INTO server (id, iface, listen_port, endpoint_host, endpoint_port,
				private_key_enc, mtu, ipv4_cidr, ipv6_cidr, dns, keepalive, client_isolation,
				client_allowed_ips, updated_at)
				VALUES (1, 'wg0', 51820, 'vpn.example.com', 51820, ?, 1420, '10.8.0.0/24',
				'fd12:3456:789a:1::/64', '["10.8.0.1","fd12:3456:789a:1::1"]', 25, 1,
				'["0.0.0.0/0","::/0"]', ?)`, s.sealKey(serverKey, serverKeyPurpose), early)
		},
		check: func(c *checker) {
			st, err := c.s.Settings(c.ctx)
			c.must(err, "the settings")
			c.eq("the interface", st.Interface, "wg0")
			c.eq("the endpoint", st.EndpointHost, "vpn.example.com")
			c.eq("the endpoint port", st.EndpointPort, uint16(51820))
			c.eq("the private key", st.PrivateKey, serverKey)
			c.eq("the MTU", st.MTU, 1420)
			c.eq("the IPv4 subnet", st.IPv4, netip.MustParsePrefix("10.8.0.0/24"))
			c.eq("the IPv6 subnet", st.IPv6, netip.MustParsePrefix("fd12:3456:789a:1::/64"))
			c.addrs("the DNS servers", st.DNS, "10.8.0.1", "fd12:3456:789a:1::1")
			c.eq("the keepalive", st.Keepalive, 25)
			c.eq("client isolation", st.ClientIsolation, true)
			c.prefixes("the clients' AllowedIPs", st.ClientAllowedIPs, "0.0.0.0/0", "::/0")
			// The listen port and the admin's extra sources are what later features change,
			// and they check their own.
		},
	},
	{
		name: "the clients", since: 1, profile: Used,
		seed: func(s *seeder) {
			// A client whose keys the server made, with a preshared key; a paused one without
			// a preshared key; and one that brought its own public key, so the server holds no
			// private key for it, on a VPN that had no IPv6 yet.
			s.exec(`INSERT INTO clients (id, name, enabled, ipv4, ipv6, public_key, private_key_enc,
				psk_enc, created_at, updated_at) VALUES (?, 'phone', 1, '10.8.0.2', 'fd12:3456:789a:1::2',
				?, ?, ?, ?, ?)`, phoneID, phoneKey.PublicKey().String(),
				s.sealKey(phoneKey, clientKeyPurpose(phoneID)), s.sealKey(phonePSK, clientPSKPurpose(phoneID)),
				early, early)
			s.exec(`INSERT INTO clients (id, name, enabled, ipv4, ipv6, public_key, private_key_enc,
				psk_enc, created_at, updated_at) VALUES (?, 'laptop', 0, '10.8.0.3', 'fd12:3456:789a:1::3',
				?, ?, NULL, ?, ?)`, laptopID, laptopKey.PublicKey().String(),
				s.sealKey(laptopKey, clientKeyPurpose(laptopID)), early, later)
			s.exec(`INSERT INTO clients (id, name, enabled, ipv4, ipv6, public_key, private_key_enc,
				psk_enc, created_at, updated_at) VALUES (?, 'router', 1, '10.8.0.4', NULL, ?, NULL,
				NULL, ?, ?)`, routerID, routerKey.PublicKey().String(), early, early)
		},
		check: func(c *checker) {
			clients, err := c.s.Clients(c.ctx)
			c.must(err, "the clients")
			if len(clients) != 3 {
				c.t.Fatalf("%s: %d clients, want 3", c.feature, len(clients))
			}
			phone, laptop, router := clients[0], clients[1], clients[2]
			c.eq("the phone", [4]any{phone.ID, phone.Name, phone.Enabled, phone.IPv4},
				[4]any{phoneID, "phone", true, netip.MustParseAddr("10.8.0.2")})
			c.eq("the phone's IPv6", phone.IPv6, netip.MustParseAddr("fd12:3456:789a:1::2"))
			c.eq("the phone's public key", phone.PublicKey, phoneKey.PublicKey())
			if phone.PrivateKey == nil {
				c.t.Errorf("%s: the phone's private key didn't open", c.feature)
			} else {
				c.eq("the phone's private key", *phone.PrivateKey, phoneKey)
			}
			c.eq("the phone's preshared key", phone.PresharedKey, phonePSK)
			c.when("the phone's creation", phone.CreatedAt, early)

			c.eq("the laptop", [4]any{laptop.ID, laptop.Name, laptop.Enabled, laptop.IPv4},
				[4]any{laptopID, "laptop", false, netip.MustParseAddr("10.8.0.3")})
			if laptop.PrivateKey == nil || *laptop.PrivateKey != laptopKey {
				c.t.Errorf("%s: the laptop's private key is %v", c.feature, laptop.PrivateKey)
			}
			c.eq("the laptop's preshared key", laptop.PresharedKey, wgtypes.Key{})
			c.when("the laptop's last change", laptop.UpdatedAt, later)

			c.eq("the router", [4]any{router.ID, router.Name, router.Enabled, router.IPv4},
				[4]any{routerID, "router", true, netip.MustParseAddr("10.8.0.4")})
			c.eq("the router's IPv6", router.IPv6, netip.Addr{})
			c.eq("the router's public key", router.PublicKey, routerKey.PublicKey())
			c.eq("the router's private key", router.PrivateKey, (*wgtypes.Key)(nil))
		},
	},
	{
		name: "the admin account", since: 2, profile: Used,
		seed: func(s *seeder) {
			s.exec(`INSERT INTO users (id, username, password_hash, created_at, password_changed_at,
				last_login_at) VALUES (?, 'admin', ?, ?, ?, ?)`, adminID, adminHash, early, early, later)
		},
		check: func(c *checker) {
			u, err := c.s.UserByName(c.ctx, "ADMIN")
			c.must(err, "the account")
			c.eq("the account's ID", u.ID, adminID)
			c.eq("the username", u.Username, "admin")
			c.when("the account's creation", u.CreatedAt, early)
			c.when("the last login", u.LastLoginAt, later)
			ok, err := auth.NewHasher(auth.DefaultParams).Verify(c.ctx, u.PasswordHash, adminPassword)
			c.must(err, "the password hash")
			if !ok {
				c.t.Errorf("%s: the admin's old password no longer matches its hash", c.feature)
			}
		},
	},
	{
		name: "the admin's logins", since: 2, profile: Used,
		seed: func(s *seeder) {
			s.exec(`INSERT INTO auth_sessions (id, token_hash, user_id, created_at, last_seen_at,
				expires_at, ip, user_agent) VALUES ('sess-1', ?, ?, ?, ?, '2026-10-08T08:30:00.000000Z',
				'192.168.4.20', 'Mozilla/5.0 (X11; Linux x86_64)')`, sessionToken[:], adminID, later, later)
		},
		check: func(c *checker) {
			sess, owner, err := c.s.SessionByToken(c.ctx, sessionToken[:])
			if c.opts.endedLogins {
				// A restore logs everybody out: the browsers that were logged in to the old host
				// aren't logged in to this one.
				if !errors.Is(err, store.ErrNoSession) {
					c.t.Errorf("%s: the old login survived, as %+v (%v)", c.feature, sess, err)
				}
				return
			}
			c.must(err, "the login session")
			c.eq("the session's account", owner.ID, adminID)
			c.eq("the session's address", sess.IP, "192.168.4.20")
			c.eq("the session's browser", sess.UserAgent, "Mozilla/5.0 (X11; Linux x86_64)")
			c.when("the session's expiry", sess.ExpiresAt, "2026-10-08T08:30:00.000000Z")
		},
	},
	{
		name: "the setup token", since: 2, profile: Unused,
		seed: func(s *seeder) {
			s.exec(`INSERT INTO setup_token (id, token_enc, created_at) VALUES (1, ?, ?)`,
				s.seal([]byte("the-first-run-token"), setupPurpose), early)
		},
		check: func(c *checker) {
			// Asking for a token when one is waiting returns it; it writes nothing.
			got, err := c.s.EnsureSetupToken(c.ctx, "another-token")
			c.must(err, "the setup token")
			c.eq("the setup token", got, "the-first-run-token")
		},
	},
	{
		name: "the event log", since: 2, profile: Used,
		seed: func(s *seeder) {
			s.exec(`INSERT INTO events (id, ts, kind, category, actor, via, source_ip, client_id,
				client_name, data) VALUES (1, ?, 'server.initialized', 'system', 'drawbridge', 'system',
				'', NULL, '', '{}')`, early)
			s.exec(`INSERT INTO events (id, ts, kind, category, actor, via, source_ip, client_id,
				client_name, data) VALUES (2, ?, 'client.added', 'admin', 'admin', 'web',
				'192.168.4.20', ?, 'phone', '{"ipv4":"10.8.0.2"}')`, "2026-09-26T10:16:02.5Z", phoneID)
		},
		check: func(c *checker) {
			// Events the host has logged since (an upgrade, a restore) come after these two, so
			// they're found by their IDs, not counted.
			events, err := c.s.Events(c.ctx, store.EventFilter{Limit: 1000})
			c.must(err, "the events")
			byID := map[int64]store.Event{}
			for _, e := range events {
				byID[e.ID] = e
			}
			added, initialized := byID[2], byID[1]
			c.eq("the first event", initialized.Kind, "server.initialized")
			c.eq("the first event's client", initialized.ClientID, "")
			c.when("the first event's time", initialized.Time, early)
			c.eq("the second event", [5]string{added.Kind, added.Category, added.Actor, added.Via, added.SourceIP},
				[5]string{"client.added", "admin", "admin", "web", "192.168.4.20"})
			c.eq("the second event's client", [2]string{added.ClientID, added.ClientName}, [2]string{phoneID, "phone"})
			c.eq("the second event's data", added.Data, map[string]string{"ipv4": "10.8.0.2"})
			c.when("the second event's time", added.Time, "2026-09-26T10:16:02.5Z")
		},
	},
	{
		name: "the admin's extra sources", since: 3,
		seed: func(s *seeder) {
			s.exec(`UPDATE server SET admin_allowed = '["100.64.10.0/24","fd12:3456:789a:77::/64"]'`)
		},
		check: func(c *checker) {
			st, err := c.s.Settings(c.ctx)
			c.must(err, "the settings")
			c.prefixes("the admin's extra sources", st.AdminAllowed, "100.64.10.0/24", "fd12:3456:789a:77::/64")
		},
	},
	{
		name: "the clients' connection history", since: 4, profile: Used,
		seed: func(s *seeder) {
			s.exec(`INSERT INTO client_sessions (id, client_id, started_at, ended_at, endpoint,
				baseline_rx, baseline_tx, rx_bytes, tx_bytes) VALUES ('conn-1', ?, '2026-09-30T09:00:00.000000Z',
				'2026-09-30T11:30:00.000000Z', '198.51.100.7:40001', 1000, 2000, 5000000, 1200000)`, phoneID)
			s.exec(`INSERT INTO client_sessions (id, client_id, started_at, ended_at, endpoint,
				baseline_rx, baseline_tx, rx_bytes, tx_bytes) VALUES ('conn-2', ?, '2026-10-01T07:00:00.000000Z',
				NULL, '[2001:db8::7]:40002', 6000000, 3200000, 1234, 5678)`, phoneID)
		},
		check: func(c *checker) {
			history, err := c.s.ClientSessions(c.ctx, phoneID, time.Time{}, 10)
			c.must(err, "the phone's connections")
			if len(history) != 2 {
				c.t.Fatalf("%s: %d connections, want 2", c.feature, len(history))
			}
			open, closed := history[0], history[1] // newest first
			c.eq("the open connection", [3]any{open.ID, open.EndedAt == nil, open.Endpoint}, [3]any{"conn-2", true, "[2001:db8::7]:40002"})
			c.eq("the open connection's bytes", [2]int64{open.RxBytes, open.TxBytes}, [2]int64{1234, 5678})
			c.eq("the closed connection", [3]any{closed.ID, closed.EndedAt != nil, closed.Endpoint}, [3]any{"conn-1", true, "198.51.100.7:40001"})
			c.eq("the closed connection's bytes", [4]int64{closed.BaselineRx, closed.BaselineTx, closed.RxBytes, closed.TxBytes},
				[4]int64{1000, 2000, 5000000, 1200000})
			now, err := c.s.CurrentClientSessions(c.ctx)
			c.must(err, "the open connections")
			if got, ok := now[phoneID]; !ok || got.ID != "conn-2" {
				c.t.Errorf("%s: the phone's open connection is %+v, want conn-2", c.feature, got)
			}
		},
	},
	{
		name: "the traffic history", since: 5, profile: Used,
		seed: func(s *seeder) {
			for _, r := range []struct {
				client, resolution, bucket string
				rx, tx                     int64
			}{
				{phoneID, "raw", "2026-10-01T07:00:00.000000Z", 100, 200},
				{phoneID, "raw", "2026-10-01T07:01:00.000000Z", 300, 400},
				{phoneID, "hourly", "2026-10-01T06:00:00.000000Z", 9000, 8000},
				{laptopID, "raw", "2026-10-01T07:00:00.000000Z", 5, 6},
			} {
				s.exec(`INSERT INTO traffic (client_id, resolution, bucket_start, rx_bytes, tx_bytes)
					VALUES (?, ?, ?, ?, ?)`, r.client, r.resolution, r.bucket, r.rx, r.tx)
			}
		},
		check: func(c *checker) {
			since := at("2026-10-01T00:00:00Z")
			phone, err := c.s.ClientTraffic(c.ctx, phoneID, store.ResolutionRaw, since)
			c.must(err, "the phone's raw traffic")
			c.eq("the phone's raw buckets", bytesOf(phone), [][3]any{
				{"2026-10-01T07:00:00Z", int64(100), int64(200)}, {"2026-10-01T07:01:00Z", int64(300), int64(400)}})
			hourly, err := c.s.ClientTraffic(c.ctx, phoneID, store.ResolutionHourly, since)
			c.must(err, "the phone's hourly traffic")
			// The 06:00 hour is stored; the 07:00 hour is the raw buckets not yet rolled up.
			c.eq("the phone's hourly buckets", bytesOf(hourly), [][3]any{
				{"2026-10-01T06:00:00Z", int64(9000), int64(8000)}, {"2026-10-01T07:00:00Z", int64(400), int64(600)}})
			total, err := c.s.TotalTraffic(c.ctx, store.ResolutionRaw, since)
			c.must(err, "the total traffic")
			c.eq("the total of every client", bytesOf(total), [][3]any{
				{"2026-10-01T07:00:00Z", int64(105), int64(206)}, {"2026-10-01T07:01:00Z", int64(300), int64(400)}})
		},
	},
	{
		name: "the AdGuard Home connection", since: 6, profile: Used,
		seed: func(s *seeder) {
			s.exec(`INSERT INTO dns_integration (id, kind, base_url, username, password_enc, updated_at)
				VALUES (1, 'adguard', 'http://127.0.0.1:3000/control', 'drawbridge', ?, ?)`,
				s.seal([]byte("adguard-password"), dnsPurpose), later)
		},
		check: func(c *checker) {
			in, ok, err := c.s.DNSIntegration(c.ctx)
			c.must(err, "the AdGuard Home connection")
			if !ok {
				c.t.Fatalf("%s: the saved connection is gone", c.feature)
			}
			c.eq("the resolver", [3]string{in.Kind, in.BaseURL, in.Username},
				[3]string{store.KindAdGuard, "http://127.0.0.1:3000/control", "drawbridge"})
			c.eq("the password", in.Password, "adguard-password")
		},
	},
	{
		name: "name sync", since: 7, profile: Used,
		seed: func(s *seeder) {
			// Turned on, with the names left alone: neither is the column's default.
			s.exec(`UPDATE dns_integration SET enabled = 1, sync_names = 0`)
			s.exec(`INSERT INTO dns_integration_clients (client_id, name, ids)
				VALUES (?, 'phone', '["10.8.0.2","fd12:3456:789a:1::2"]')`, phoneID)
			// A client deleted since: its row has no client to point at, on purpose.
			s.exec(`INSERT INTO dns_integration_clients (client_id, name, ids)
				VALUES (?, 'old-tablet', '["10.8.0.9"]')`, goneID)
		},
		check: func(c *checker) {
			in, _, err := c.s.DNSIntegration(c.ctx)
			c.must(err, "the AdGuard Home connection")
			c.eq("sync's switches", [2]bool{in.Enabled, in.SyncNames}, [2]bool{true, false})
			synced, err := c.s.SyncedClients(c.ctx)
			c.must(err, "what sync wrote")
			c.eq("the clients sync named", synced, map[string]store.SyncedClient{
				phoneID: {ClientID: phoneID, Name: "phone", IDs: []string{"10.8.0.2", "fd12:3456:789a:1::2"}},
				goneID:  {ClientID: goneID, Name: "old-tablet", IDs: []string{"10.8.0.9"}},
			})
		},
	},
	{
		name: "an API token", since: 8, profile: Used,
		seed: func(s *seeder) {
			s.exec(`INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scope, created_at,
				last_used_at) VALUES ('tok-1', ?, 'homepage', 'dbt_a1b2', ?, 'read', ?, ?)`,
				adminID, apiToken[:], early, later)
		},
		check: func(c *checker) {
			tok, err := c.s.APITokenByHash(c.ctx, apiToken[:])
			c.must(err, "the API token")
			c.eq("the token", [5]string{tok.ID, tok.UserID, tok.Name, tok.Prefix, tok.Scope},
				[5]string{"tok-1", adminID, "homepage", "dbt_a1b2", "read"})
			c.when("the token's creation", tok.CreatedAt, early)
			c.when("the token's last use", tok.LastUsedAt, later)
		},
	},
	{
		name: "which config each client was handed", since: 9, profile: Used,
		seed: func(s *seeder) {
			// Only the phone had its config handed out; the others have no baseline yet.
			s.exec(`UPDATE clients SET delivered_hash = 'ab12cd34', delivered_at = ? WHERE id = ?`, later, phoneID)
		},
		check: func(c *checker) {
			phone, err := c.s.Client(c.ctx, store.ByID(phoneID))
			c.must(err, "the phone")
			c.eq("the phone's config fingerprint", phone.ConfigHash, "ab12cd34")
			c.when("when the phone's config was handed out", phone.ConfigDeliveredAt, later)
			laptop, err := c.s.Client(c.ctx, store.ByName("laptop"))
			c.must(err, "the laptop")
			c.eq("the laptop's config fingerprint", laptop.ConfigHash, "")
			c.eq("the laptop's config time", laptop.ConfigDeliveredAt.IsZero(), true)
		},
	},
	{
		name: "a change on probation", since: 10, profile: Used,
		seed: func(s *seeder) {
			// A listen-port change applied from the web UI and not yet kept: the live row has
			// the new port, and the change holds the settings it replaced.
			s.exec(`UPDATE server SET listen_port = 51830`)
			s.exec(`INSERT INTO pending_apply (id, created_at, deadline, actor, via, source_ip, changes,
				previous, previous_key_enc) VALUES (1, ?, '2026-10-01T08:31:00.000000Z', 'admin', 'web',
				'192.168.4.20', '{"listen_port":"51820 → 51830"}', ?, ?)`, later,
				`{"interface":"wg0","listen_port":51820,"endpoint_host":"vpn.example.com","endpoint_port":51820,`+
					`"mtu":1420,"ipv4":"10.8.0.0/24","ipv6":"fd12:3456:789a:1::/64",`+
					`"dns":["10.8.0.1","fd12:3456:789a:1::1"],"keepalive":25,"client_isolation":true,`+
					`"client_allowed_ips":["0.0.0.0/0","::/0"],"admin_allowed":[]}`,
				s.sealKey(serverKey, serverKeyPurpose))
		},
		check: func(c *checker) {
			st, err := c.s.Settings(c.ctx)
			c.must(err, "the settings")
			c.eq("the live listen port", st.ListenPort, uint16(51830))
			p, err := c.s.PendingApply(c.ctx)
			c.must(err, "the change on probation")
			if p == nil {
				c.t.Fatalf("%s: the change on probation is gone", c.feature)
			}
			c.eq("who made it", [3]string{p.Actor, p.Via, p.SourceIP}, [3]string{"admin", "web", "192.168.4.20"})
			c.eq("what it changed", p.Changes, map[string]string{"listen_port": "51820 → 51830"})
			c.when("its deadline", p.Deadline, "2026-10-01T08:31:00.000000Z")
			c.eq("the port it replaced", p.Previous.ListenPort, uint16(51820))
			c.eq("the key it replaced", p.Previous.PrivateKey, serverKey)
		},
	},
	{
		name: "two-factor authentication", since: 11, profile: Used,
		seed: func(s *seeder) {
			s.exec(`UPDATE users SET totp_secret_enc = ?, totp_enabled_at = ?, totp_last_step = 58000123,
				recovery_codes_hash = '["0a1b2c","3d4e5f","6a7b8c"]' WHERE id = ?`,
				s.seal(totpSecret, totpPurpose(adminID)), later, adminID)
		},
		check: func(c *checker) {
			u, err := c.s.UserByName(c.ctx, "admin")
			c.must(err, "the account")
			c.when("when 2FA was turned on", u.TOTPEnabledAt, later)
			c.eq("the last code's time step", u.TOTPLastStep, int64(58000123))
			c.eq("the recovery codes left", u.RecoveryCodesLeft, 3)
			secret, err := c.s.TOTPSecret(c.ctx, adminID)
			c.must(err, "the 2FA secret")
			c.eq("the 2FA secret", secret, totpSecret)
		},
	},
}

// bytesOf turns traffic buckets into the three values a check compares.
func bytesOf(samples []store.TrafficSample) [][3]any {
	var out [][3]any
	for _, s := range samples {
		out = append(out, [3]any{s.BucketStart.UTC().Format(time.RFC3339), s.RxBytes, s.TxBytes})
	}
	return out
}
