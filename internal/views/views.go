// Package views has the JSON shapes that the control socket (the CLI) and the web API
// share, and the conversions to them. Keys never appear in a view.
package views

import (
	"encoding/json"
	"math"
	"net/netip"
	"strings"
	"time"

	"github.com/stuffam/drawbridge/internal/diag"
	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/reconcile"
	"github.com/stuffam/drawbridge/internal/service"
	"github.com/stuffam/drawbridge/internal/store"
)

// SettingsView is the server's settings, without the private key.
type SettingsView struct {
	Interface        string         `json:"interface"`
	ListenPort       uint16         `json:"listen_port"`
	EndpointHost     string         `json:"endpoint_host"`
	EndpointPort     uint16         `json:"endpoint_port"`
	Endpoint         string         `json:"endpoint"`
	PublicKey        string         `json:"public_key"`
	MTU              int            `json:"mtu"`
	IPv4Subnet       netip.Prefix   `json:"ipv4_subnet"`
	IPv4Address      netip.Addr     `json:"ipv4_address"`
	IPv6Subnet       netip.Prefix   `json:"ipv6_subnet"`
	IPv6Address      netip.Addr     `json:"ipv6_address"`
	DNS              []netip.Addr   `json:"dns"`
	Keepalive        int            `json:"keepalive"`
	ClientIsolation  bool           `json:"client_isolation"`
	ClientAllowedIPs []netip.Prefix `json:"client_allowed_ips"`
	AdminAllowed     []netip.Prefix `json:"admin_allowed"`
}

// Settings converts settings to their view.
func Settings(s model.Settings) SettingsView {
	v := SettingsView{
		Interface:        s.Interface,
		ListenPort:       s.ListenPort,
		EndpointHost:     s.EndpointHost,
		EndpointPort:     s.EndpointPort,
		PublicKey:        s.PublicKey().String(),
		MTU:              s.MTU,
		IPv4Subnet:       s.IPv4,
		IPv6Subnet:       s.IPv6,
		DNS:              s.DNS,
		Keepalive:        s.Keepalive,
		ClientIsolation:  s.ClientIsolation,
		ClientAllowedIPs: s.ClientAllowedIPs,
		AdminAllowed:     nonNil(s.AdminAllowed),
	}
	if ep, err := s.Endpoint(); err == nil {
		v.Endpoint = ep
	}
	if srv, err := s.ServerAddrs(); err == nil {
		v.IPv4Address, v.IPv6Address = srv.IPv4, srv.IPv6
	}
	return v
}

// DNSProbeResult is one address's DNS check.
type DNSProbeResult struct {
	Address  netip.Addr `json:"address"`
	Answered bool       `json:"answered"`
	Detail   string     `json:"detail"`
}

// DNSCheck is what asking the server's VPN addresses for DNS found. Usable lists the
// addresses that answered: the client DNS that "this server" means, and empty when no
// resolver answers on the host.
type DNSCheck struct {
	Results []DNSProbeResult `json:"results"`
	Usable  []netip.Addr     `json:"usable"`
}

// NewDNSCheck converts the service's probe results.
func NewDNSCheck(probes []service.DNSProbe) DNSCheck {
	c := DNSCheck{Results: []DNSProbeResult{}, Usable: []netip.Addr{}}
	for _, p := range probes {
		c.Results = append(c.Results, DNSProbeResult{Address: p.Address, Answered: p.Answered, Detail: p.Detail})
		if p.Answered {
			c.Usable = append(c.Usable, p.Address)
		}
	}
	return c
}

// BackupRequest asks for a backup. The passphrase encrypts it, and it's the only thing that
// does: a backup holds the secret key beside the database it unlocks.
type BackupRequest struct {
	Passphrase string `json:"passphrase"`
}

// BackupDownloadRequest asks for a backup from the web UI. Password is the account's own, asked
// for again because the file holds every secret the server has; Passphrase encrypts the file.
type BackupDownloadRequest struct {
	Password   string `json:"password"`
	Passphrase string `json:"passphrase"`
}

// SnapshotView is one snapshot of the database on the host (GET /api/system/snapshots). Kind is
// "nightly" or "pre-migration"; Schema is the database version a pre-migration one holds.
type SnapshotView struct {
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	MadeAt time.Time `json:"made_at"`
	Schema int       `json:"schema,omitempty"`
	Size   int64     `json:"size"`
}

// SnapshotListView is the snapshots on the host, newest first. Dir is empty when the daemon
// keeps none, and Nightly says whether it makes one a day.
type SnapshotListView struct {
	Dir       string         `json:"dir"`
	Nightly   bool           `json:"nightly"`
	Snapshots []SnapshotView `json:"snapshots"`
}

// NewSnapshotList converts the service's list.
func NewSnapshotList(l service.SnapshotList) SnapshotListView {
	v := SnapshotListView{Dir: l.Dir, Nightly: l.Nightly, Snapshots: make([]SnapshotView, 0, len(l.Items))}
	for _, i := range l.Items {
		v.Snapshots = append(v.Snapshots, SnapshotView{Name: i.Name, Kind: i.Kind, MadeAt: i.MadeAt, Schema: i.Schema, Size: i.Size})
	}
	return v
}

// Certificate is the TLS certificate the web UI serves (GET /api/system/certificate). Source
// is "self-signed" (made by the daemon) or "uploaded" (the admin's); Notes are warnings about an
// uploaded one that aren't reasons to refuse it. It never carries the key.
type Certificate struct {
	Source      string    `json:"source"`
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	Names       []string  `json:"names"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"fingerprint"`
	Chain       int       `json:"chain"`
	SelfIssued  bool      `json:"self_issued"`
	Notes       []string  `json:"notes"`
}

// NewCertificate converts the service's description of the certificate in use.
func NewCertificate(c service.CertificateInfo) Certificate {
	v := Certificate{
		Source: c.Source, Subject: c.Subject, Issuer: c.Issuer, Names: c.Names, NotBefore: c.NotBefore,
		NotAfter: c.NotAfter, Fingerprint: c.Fingerprint, Chain: c.Chain, SelfIssued: c.SelfIssued, Notes: c.Notes,
	}
	if v.Names == nil {
		v.Names = []string{}
	}
	if v.Notes == nil {
		v.Notes = []string{}
	}
	return v
}

// CertificateInstallRequest installs a certificate: the chain (the server's own certificate
// first) and its private key, as PEM text. Password is the account's own, asked for again by the
// web API and not by the control socket, which root's account is enough for.
type CertificateInstallRequest struct {
	Password    string `json:"password,omitempty"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}

// DiagnosticCheck is one host diagnostic's result (`drawbridge doctor`). Status is "pass",
// "warn", "fail", or "skip"; Hint says how to fix a warning or a failure.
type DiagnosticCheck struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

// Diagnostics is the list of checks, in the order the admin should read them.
type Diagnostics struct {
	Checks []DiagnosticCheck `json:"checks"`
}

// NewDiagnostics converts the diagnostics' results.
func NewDiagnostics(checks []diag.Check) Diagnostics {
	d := Diagnostics{Checks: []DiagnosticCheck{}}
	for _, c := range checks {
		d.Checks = append(d.Checks, DiagnosticCheck{ID: c.ID, Name: c.Name, Status: string(c.Status), Detail: c.Detail, Hint: c.Hint})
	}
	return d
}

// SettingsPatch changes some settings. Omitted fields stay as they are. DNSDefault resets
// the DNS servers to the server's VPN addresses (D12).
type SettingsPatch struct {
	EndpointHost    *string         `json:"endpoint_host,omitempty"`
	EndpointPort    *uint16         `json:"endpoint_port,omitempty"`
	ListenPort      *uint16         `json:"listen_port,omitempty"`
	MTU             *int            `json:"mtu,omitempty"`
	DNS             *[]netip.Addr   `json:"dns,omitempty"`
	DNSDefault      bool            `json:"dns_default,omitempty"`
	Keepalive       *int            `json:"keepalive,omitempty"`
	ClientIsolation *bool           `json:"client_isolation,omitempty"`
	AdminAllowed    *[]netip.Prefix `json:"admin_allowed,omitempty"`
}

// Service converts the patch for the service layer.
func (p SettingsPatch) Service() service.SettingsPatch {
	return service.SettingsPatch{
		EndpointHost: p.EndpointHost, EndpointPort: p.EndpointPort, ListenPort: p.ListenPort,
		MTU: p.MTU, DNS: p.DNS, DNSDefault: p.DNSDefault, Keepalive: p.Keepalive,
		ClientIsolation: p.ClientIsolation, AdminAllowed: p.AdminAllowed,
	}
}

// nonNil returns ps, or an empty list for nil, so JSON shows [] rather than null.
func nonNil(ps []netip.Prefix) []netip.Prefix {
	if ps == nil {
		return []netip.Prefix{}
	}
	return ps
}

// PeerView is a client's live status in the tunnel.
type PeerView struct {
	Endpoint      string    `json:"endpoint,omitempty"`
	LastHandshake time.Time `json:"last_handshake,omitzero"`
	// ReceiveBytes and SendBytes are the peer's all-time totals: they reset only when
	// its peer is recreated (a pause and resume, or the tunnel restarting).
	ReceiveBytes int64 `json:"receive_bytes"`
	SendBytes    int64 `json:"send_bytes"`
	// SessionStartedAt and the bytes below describe the client's current connection
	// (docs/PLAN.md §6.4); they're zero when it has none.
	SessionStartedAt    time.Time `json:"session_started_at,omitzero"`
	SessionReceiveBytes int64     `json:"session_receive_bytes,omitempty"`
	SessionSendBytes    int64     `json:"session_send_bytes,omitempty"`
}

// ClientView is a client, without its keys.
type ClientView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	IPv4      netip.Addr `json:"ipv4"`
	IPv6      netip.Addr `json:"ipv6"`
	PublicKey string     `json:"public_key"`
	CreatedAt time.Time  `json:"created_at"`
	// ConfigDeliveredAt is when the admin last downloaded or showed the client's config; it's
	// absent when they never have.
	ConfigDeliveredAt time.Time `json:"config_delivered_at,omitzero"`
	// ConfigOutdated means the server's settings or the client's keys changed since then, so the
	// config the client holds no longer matches and it needs to import it again. Only the
	// responses that carry live status set it (Status); a change's own response (ClientResult)
	// leaves it out.
	ConfigOutdated bool `json:"config_outdated,omitempty"`
	// Peer is nil when the client isn't in the tunnel (paused, or the tunnel is down).
	Peer *PeerView `json:"peer,omitempty"`
}

// Client converts a client to its view, without live status.
func Client(c model.Client) ClientView {
	return ClientView{
		ID:                c.ID,
		Name:              c.Name,
		Enabled:           c.Enabled,
		IPv4:              c.IPv4,
		IPv6:              c.IPv6,
		PublicKey:         c.PublicKey.String(),
		CreatedAt:         c.CreatedAt,
		ConfigDeliveredAt: c.ConfigDeliveredAt,
	}
}

// Status converts a client and its live status to a view.
func Status(cs service.ClientStatus) ClientView {
	v := Client(cs.Client)
	v.ConfigOutdated = cs.ConfigOutdated
	if p := cs.Peer; p != nil {
		v.Peer = &PeerView{
			LastHandshake: p.LastHandshake,
			ReceiveBytes:  p.ReceiveBytes,
			SendBytes:     p.SendBytes,
		}
		if p.Endpoint.IsValid() {
			v.Peer.Endpoint = p.Endpoint.String()
		}
		if cs.Session != nil {
			v.Peer.SessionStartedAt = cs.Session.StartedAt
			v.Peer.SessionReceiveBytes = cs.Session.RxBytes
			v.Peer.SessionSendBytes = cs.Session.TxBytes
		}
	}
	return v
}

// Statuses converts a list of clients.
func Statuses(cs []service.ClientStatus) []ClientView {
	out := make([]ClientView, len(cs))
	for i, c := range cs {
		out[i] = Status(c)
	}
	return out
}

// ClientResult is the response to a change to a client.
type ClientResult struct {
	Client  ClientView `json:"client"`
	Warning string     `json:"warning,omitempty"`
	// ApplyFailed means the change is saved but applying it to the tunnel failed.
	ApplyFailed bool `json:"apply_failed,omitempty"`
}

// NewClientResult is the response to a change that was applied.
func NewClientResult(c model.Client, a service.Applied) ClientResult {
	return ClientResult{Client: Client(c), Warning: a.Warning(), ApplyFailed: a.Err != nil}
}

// SettingsResult is the response to a settings change.
type SettingsResult struct {
	Settings SettingsView `json:"settings"`
	Warning  string       `json:"warning,omitempty"`
	// ApplyFailed means the change is saved but applying it to the tunnel failed.
	ApplyFailed bool `json:"apply_failed,omitempty"`
	// PendingChange is set when the change is on probation: it's applied, and undone unless it's
	// kept in time (docs/PLAN.md §4.3).
	PendingChange *PendingChangeView `json:"pending_change,omitempty"`
}

// NewSettingsResult is the response to a settings change that was applied.
func NewSettingsResult(s model.Settings, a service.Applied) SettingsResult {
	return SettingsResult{Settings: Settings(s), Warning: a.Warning(), ApplyFailed: a.Err != nil,
		PendingChange: NewPendingChange(a.Pending)}
}

// PendingChangeView is a settings change on probation. ExpiresIn is the seconds left when the
// response was made, so a browser whose clock disagrees with the server's can still count down;
// ExpiresAt is the same moment as a time.
type PendingChangeView struct {
	// Changes is what changed, as {"setting": "old → new"}.
	Changes   map[string]string `json:"changes"`
	ExpiresAt time.Time         `json:"expires_at"`
	ExpiresIn int               `json:"expires_in"`
	// Actor and Via say who made the change: "admin" via "web", or "root" via "cli".
	Actor string `json:"actor"`
	Via   string `json:"via"`
}

// NewPendingChange converts a change on probation; nil for none.
func NewPendingChange(p *service.PendingChange) *PendingChangeView {
	if p == nil {
		return nil
	}
	left := int(math.Ceil(time.Until(p.Deadline).Seconds()))
	return &PendingChangeView{Changes: p.Changes, ExpiresAt: p.Deadline.UTC(), ExpiresIn: max(left, 0), Actor: p.Actor, Via: p.Via}
}

// ApplyState is whether a settings change is waiting to be kept (GET /api/server/apply).
type ApplyState struct {
	PendingChange *PendingChangeView `json:"pending_change,omitempty"`
}

// ApplyResult is what `drawbridge apply` found: the changes the kernel needed to match the
// settings, and whether they were made. TunnelDown means the interface doesn't exist, so
// nothing could be compared.
type ApplyResult struct {
	DryRun     bool     `json:"dry_run"`
	TunnelDown bool     `json:"tunnel_down"`
	Changes    []string `json:"changes"`
}

// NewApplyResult converts a reconcile result; Changes is never nil.
func NewApplyResult(r reconcile.Result, dryRun bool) ApplyResult {
	changes := r.Changes
	if changes == nil {
		changes = []string{}
	}
	return ApplyResult{DryRun: dryRun, TunnelDown: r.TunnelDown, Changes: changes}
}

// NewClientRequest creates a client.
type NewClientRequest struct {
	Name string `json:"name"`
}

// ClientPatch changes a client. Omitted fields stay as they are.
type ClientPatch struct {
	Name *string `json:"name,omitempty"`
}

// Error is the body of every error response.
type Error struct {
	Error string `json:"error"`
	// Code is set when a client has to tell the error from others of its status; "totp_required"
	// for a login whose password was right and needs a code too.
	Code string `json:"code,omitempty"`
}

// EventView is one entry in the event log.
type EventView struct {
	ID         int64             `json:"id"`
	Time       time.Time         `json:"time"`
	Kind       string            `json:"kind"`
	Category   string            `json:"category"`
	Actor      string            `json:"actor"`
	Via        string            `json:"via"`
	SourceIP   string            `json:"source_ip,omitempty"`
	ClientID   string            `json:"client_id,omitempty"`
	ClientName string            `json:"client_name,omitempty"`
	Data       map[string]string `json:"data,omitempty"`
}

// Events converts events to their views.
func Events(es []store.Event) []EventView {
	out := make([]EventView, len(es))
	for i, e := range es {
		out[i] = EventView{
			ID: e.ID, Time: e.Time, Kind: e.Kind, Category: e.Category, Actor: e.Actor,
			Via: e.Via, SourceIP: e.SourceIP, ClientID: e.ClientID, ClientName: e.ClientName,
			Data: e.Data,
		}
	}
	return out
}

// EventsCSVHeader is the first row of the event log's CSV export.
var EventsCSVHeader = []string{"time", "event", "category", "actor", "via", "source_ip", "client_id",
	"client_name", "details"}

// EventCSVRow is an event as a row of the CSV export, in the order of EventsCSVHeader. The
// time is RFC 3339 in UTC, and the details are the event's data as a JSON object (empty when
// there is none).
//
// A spreadsheet runs a cell that starts with =, +, -, or @ as a formula, and some of these
// cells are chosen by strangers: a failed login's actor is whatever name was typed. So such
// a cell starts with an apostrophe, which a spreadsheet reads as "this is text".
func EventCSVRow(e store.Event) []string {
	var details string
	if len(e.Data) > 0 {
		// Marshaling a map of strings can't fail, and sorts its keys.
		b, _ := json.Marshal(e.Data)
		details = string(b)
	}
	row := []string{e.Time.UTC().Format(time.RFC3339), e.Kind, e.Category, e.Actor, e.Via, e.SourceIP,
		e.ClientID, e.ClientName, details}
	for i, cell := range row {
		if cell != "" && strings.IndexByte("=+-@\t\r", cell[0]) >= 0 {
			row[i] = "'" + cell
		}
	}
	return row
}

// SetupStatus says whether first-run setup is needed.
type SetupStatus struct {
	Needed bool `json:"needed"`
}

// SetupRequest creates the admin account with the setup token.
type SetupRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginRequest logs in.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Code is the six digits from the authenticator app, or a recovery code. It is needed only by
	// an account with 2FA on, and a login without it gets a 401 with the code "totp_required".
	Code string `json:"code,omitempty"`
}

// TOTPEnrollRequest starts turning 2FA on. It takes the password again, even in a logged-in
// session.
type TOTPEnrollRequest struct {
	Password string `json:"password"`
}

// TOTPEnrollment is the secret to put in an authenticator app, shown once, with the address its QR
// code holds.
type TOTPEnrollment struct {
	// Secret is base32, for an app that can't scan.
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

// TOTPVerifyRequest finishes turning 2FA on, with the first code from the app.
type TOTPVerifyRequest struct {
	Code string `json:"code"`
}

// SecondFactorRequest is a change to 2FA that takes the password again and a code: the six digits
// from the authenticator app, or a recovery code.
type SecondFactorRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// RecoveryCodes is a new set of recovery codes, shown once.
type RecoveryCodes struct {
	Codes []string `json:"codes"`
}

// PasswordChange changes the logged-in account's password.
type PasswordChange struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// UserView is the admin account.
type UserView struct {
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	// LastLoginAt is when the account last logged in. In the response to logging in,
	// it's the login before this one, so people can spot one they didn't make; zero if
	// there was none.
	LastLoginAt time.Time `json:"last_login_at,omitzero"`
	// TOTPEnabled is whether a login needs a code from the admin's authenticator app.
	TOTPEnabled bool `json:"totp_enabled"`
	// RecoveryCodesLeft is how many recovery codes haven't been used; 0 when 2FA is off.
	RecoveryCodesLeft int `json:"recovery_codes_left"`
}

// User converts an account to its view.
func User(u store.User) UserView {
	return UserView{Username: u.Username, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
		TOTPEnabled: u.TOTPEnabled(), RecoveryCodesLeft: u.RecoveryCodesLeft}
}

// SessionView is a logged-in browser. Its token is never shown.
type SessionView struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	// Current is the session making the request.
	Current bool `json:"current"`
}

// Session converts a session to its view.
func Session(s store.Session, current string) SessionView {
	return SessionView{ID: s.ID, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt,
		ExpiresAt: s.ExpiresAt, IP: s.IP, UserAgent: s.UserAgent, Current: s.ID == current}
}

// Me is the logged-in account and its session.
type Me struct {
	User    UserView    `json:"user"`
	Session SessionView `json:"session"`
}

// ConfigFileName turns a client's name into a file name for its config. The WireGuard
// apps name the tunnel after the file, and Linux limits interface names to 15
// characters, so it keeps letters, digits, and - _ = + . and at most 15 of them.
func ConfigFileName(name string) string {
	var b []rune
	for _, r := range name {
		switch {
		case len(b) == 15:
		case r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_=+.", r)):
			b = append(b, r)
		case r == ' ' || r == '\'':
			b = append(b, '-')
		}
	}
	s := strings.Trim(string(b), "-.")
	if s == "" {
		s = "wireguard"
	}
	return s + ".conf"
}

// ServerStatus is the tunnel's state at a glance.
type ServerStatus struct {
	TunnelUp bool `json:"tunnel_up"`
	Clients  int  `json:"clients"`
	Paused   int  `json:"paused"`
	// Online counts clients with a handshake in the last three minutes.
	Online int `json:"online"`
	// Outdated counts clients whose config changed since the admin last handed it out.
	Outdated int `json:"outdated"`
	// ReceiveBytes and SendBytes add up the counters of the peers in the tunnel now, counted at
	// the server (service.Status).
	ReceiveBytes int64 `json:"receive_bytes"`
	SendBytes    int64 `json:"send_bytes"`
	// AdGuardWarning is set when the AdGuard Home name sync needs the admin: it can't reach
	// AdGuard Home, was refused, or couldn't name some clients.
	AdGuardWarning string `json:"adguard_warning,omitempty"`
}

// NewServerStatus converts the service's status.
func NewServerStatus(s service.Status) ServerStatus {
	return ServerStatus{TunnelUp: s.TunnelUp, Clients: s.Clients, Paused: s.Paused, Online: s.Online,
		Outdated: s.Outdated, ReceiveBytes: s.ReceiveBytes, SendBytes: s.SendBytes, AdGuardWarning: s.AdGuardWarning}
}

// StreamStatus is the stream's "status" message: what the dashboard, the client list, and a
// client's page would each ask for every few seconds, in one.
type StreamStatus struct {
	Server  ServerStatus `json:"server"`
	Clients []ClientView `json:"clients"`
	// PendingChange is a settings change waiting to be kept. It's here and not in Server because
	// a read-only API token can read the server's status, and a setting being changed is the
	// admin's business.
	PendingChange *PendingChangeView `json:"pending_change,omitempty"`
}

// NewStreamStatus converts the service's snapshot.
func NewStreamStatus(st service.Status, clients []service.ClientStatus) StreamStatus {
	return StreamStatus{Server: NewServerStatus(st), Clients: Statuses(clients), PendingChange: NewPendingChange(st.Pending)}
}

// TrafficSampleView is one bucket of a client's, or every client's, traffic history
// (docs/PLAN.md §6.4).
type TrafficSampleView struct {
	BucketStart  time.Time `json:"bucket_start"`
	ReceiveBytes int64     `json:"receive_bytes"`
	SendBytes    int64     `json:"send_bytes"`
}

// TrafficSamples converts traffic samples to their views. The store already returns
// them oldest first.
func TrafficSamples(ss []store.TrafficSample) []TrafficSampleView {
	out := make([]TrafficSampleView, len(ss))
	for i, s := range ss {
		out[i] = TrafficSampleView{BucketStart: s.BucketStart, ReceiveBytes: s.RxBytes, SendBytes: s.TxBytes}
	}
	return out
}

// TrafficSamplesView is one series of traffic history, with the window it covers
// (GET /api/traffic and GET /api/clients/{id}/traffic, docs/PLAN.md §6.4, §8).
type TrafficSamplesView struct {
	// StepSeconds is how long one sample covers; a sample's bytes over it is a rate.
	StepSeconds float64 `json:"step_seconds"`
	// Until is where the history ends: every sample is complete and starts before it.
	Until   time.Time           `json:"until"`
	Samples []TrafficSampleView `json:"samples"`
}

// NewTrafficSamples converts the service's samples.
func NewTrafficSamples(t service.TrafficSamples) TrafficSamplesView {
	return TrafficSamplesView{StepSeconds: t.Step.Seconds(), Until: t.Until, Samples: TrafficSamples(t.Samples)}
}

// TrafficTotalView is every client's traffic over a range, added up (GET /api/traffic/total).
type TrafficTotalView struct {
	// Range is the range asked for, as written: "24h" when none was.
	Range string `json:"range"`
	// Since and Until bound what was added: the samples that start in [Since, Until).
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	// ReceiveBytes and SendBytes are counted at the server.
	ReceiveBytes int64 `json:"receive_bytes"`
	SendBytes    int64 `json:"send_bytes"`
}

// NewTrafficTotal converts the service's total, for the range called name.
func NewTrafficTotal(name string, t service.TrafficTotal) TrafficTotalView {
	return TrafficTotalView{Range: name, Since: t.Since, Until: t.Until, ReceiveBytes: t.RxBytes, SendBytes: t.TxBytes}
}

// ClientTrafficView is one client's samples in a TrafficHistoryView.
type ClientTrafficView struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Samples []TrafficSampleView `json:"samples"`
}

// TrafficHistoryView is every client's traffic over one chart range
// (GET /api/traffic/clients, docs/PLAN.md §6.4, §8).
type TrafficHistoryView struct {
	// StepSeconds is how long one sample covers; a sample's bytes over it is a rate.
	StepSeconds float64 `json:"step_seconds"`
	// Until is where the history ends: every sample is complete and ends at or before it.
	Until   time.Time           `json:"until"`
	Clients []ClientTrafficView `json:"clients"`
}

// NewTrafficHistory converts the service's history. Every client is in it, by name.
func NewTrafficHistory(h service.TrafficHistory) TrafficHistoryView {
	v := TrafficHistoryView{
		StepSeconds: h.Step.Seconds(),
		Until:       h.Until,
		Clients:     make([]ClientTrafficView, len(h.Series)),
	}
	for i, s := range h.Series {
		v.Clients[i] = ClientTrafficView{ID: s.Client.ID, Name: s.Client.Name, Samples: TrafficSamples(s.Samples)}
	}
	return v
}

// ClientSessionView is one of a client's past or current connections.
type ClientSessionView struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	// EndedAt is absent while the session is still open.
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	Endpoint     string     `json:"endpoint"`
	ReceiveBytes int64      `json:"receive_bytes"`
	SendBytes    int64      `json:"send_bytes"`
}

// ClientSessions converts sessions to their views. The store already returns them
// newest first.
func ClientSessions(cs []store.ClientSession) []ClientSessionView {
	out := make([]ClientSessionView, len(cs))
	for i, c := range cs {
		out[i] = ClientSessionView{ID: c.ID, StartedAt: c.StartedAt, EndedAt: c.EndedAt,
			Endpoint: c.Endpoint, ReceiveBytes: c.RxBytes, SendBytes: c.TxBytes}
	}
	return out
}

// AdGuardConnection is the saved connection to AdGuard Home (docs/PLAN.md §6.3), and how the name
// sync is doing. The password isn't in it: it can be set, and never read back.
type AdGuardConnection struct {
	// Configured is whether a connection is saved. When it isn't, BaseURL is the usual address
	// of a local AdGuard Home, to start from.
	Configured  bool   `json:"configured"`
	BaseURL     string `json:"base_url"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	// Enabled is the admin's switch for using the connection at all, and SyncNames whether
	// Drawbridge writes its clients' names into AdGuard Home.
	Enabled   bool        `json:"enabled"`
	SyncNames bool        `json:"sync_names"`
	Sync      AdGuardSync `json:"sync"`
}

// NewAdGuardConnection converts the service's connection and sync status.
func NewAdGuardConnection(c service.AdGuardConnection, sync service.AdGuardSyncStatus) AdGuardConnection {
	return AdGuardConnection{
		Configured: c.Configured, BaseURL: c.BaseURL, Username: c.Username, HasPassword: c.HasPassword,
		Enabled: c.Enabled, SyncNames: c.SyncNames, Sync: NewAdGuardSync(sync),
	}
}

// AdGuardSync is how the name sync is doing: off, pending, ok, error (retrying), or stopped
// (AdGuard Home refused the account).
type AdGuardSync struct {
	State string `json:"state"`
	// LastSync is when a pass last finished without failing; null before the first.
	LastSync *time.Time `json:"last_sync"`
	Error    string     `json:"error,omitempty"`
	// Synced is how many clients have their name in AdGuard Home.
	Synced    int               `json:"synced"`
	Conflicts []AdGuardConflict `json:"conflicts"`
}

// AdGuardConflict is a client the sync couldn't name in AdGuard Home, and why.
type AdGuardConflict struct {
	ClientID string `json:"client_id"`
	Client   string `json:"client"`
	Reason   string `json:"reason"`
}

// NewAdGuardSync converts the service's sync status.
func NewAdGuardSync(st service.AdGuardSyncStatus) AdGuardSync {
	out := AdGuardSync{State: st.State, Error: st.Error, Synced: st.Synced, Conflicts: []AdGuardConflict{}}
	if !st.LastSync.IsZero() {
		t := st.LastSync.UTC()
		out.LastSync = &t
	}
	for _, c := range st.Conflicts {
		out.Conflicts = append(out.Conflicts, AdGuardConflict(c))
	}
	return out
}

// AdGuardRequest saves or tests a connection. Omitted fields stay as they are. A password
// replaces the saved one, and "" removes it. Changing the address or the username takes a
// password too, because the saved one is only sent to the address it was saved with.
type AdGuardRequest struct {
	BaseURL  *string `json:"base_url"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	// Enabled and SyncNames are the switches; they move no password, so they take none.
	Enabled   *bool `json:"enabled"`
	SyncNames *bool `json:"sync_names"`
}

// Service returns the request as the service takes it.
func (r AdGuardRequest) Service() service.AdGuardPatch {
	return service.AdGuardPatch{BaseURL: r.BaseURL, Username: r.Username, Password: r.Password,
		Enabled: r.Enabled, SyncNames: r.SyncNames}
}

// AdGuardQueryLog is how AdGuard Home's query log is set.
type AdGuardQueryLog struct {
	Enabled           bool `json:"enabled"`
	AnonymizeClientIP bool `json:"anonymize_client_ip"`
}

// AdGuardTest is what a test of the connection found.
type AdGuardTest struct {
	OK                bool             `json:"ok"`
	Error             string           `json:"error,omitempty"`
	Refused           bool             `json:"refused"`
	Version           string           `json:"version,omitempty"`
	Running           bool             `json:"running"`
	ProtectionEnabled bool             `json:"protection_enabled"`
	QueryLog          *AdGuardQueryLog `json:"query_log"`
	DNS               []DNSProbeResult `json:"dns"`
	Warnings          []string         `json:"warnings"`
}

// NewAdGuardTest converts the service's result.
func NewAdGuardTest(t service.AdGuardTest) AdGuardTest {
	out := AdGuardTest{
		OK: t.OK, Error: t.Error, Refused: t.Refused, Version: t.Version, Running: t.Running,
		ProtectionEnabled: t.ProtectionEnabled, DNS: []DNSProbeResult{}, Warnings: []string{},
	}
	if t.QueryLog != nil {
		out.QueryLog = &AdGuardQueryLog{Enabled: t.QueryLog.Enabled, AnonymizeClientIP: t.QueryLog.AnonymizeClientIP}
	}
	for _, p := range t.DNS {
		out.DNS = append(out.DNS, DNSProbeResult{Address: p.Address, Answered: p.Answered, Detail: p.Detail})
	}
	out.Warnings = append(out.Warnings, t.Warnings...)
	return out
}

// DNSQuery is one DNS query a client made, from AdGuard Home's query log.
type DNSQuery struct {
	Time time.Time `json:"time"`
	// Address is which of the client's addresses it came from.
	Address netip.Addr `json:"address"`
	Domain  string     `json:"domain"`
	// Type is the record type asked for: A, AAAA, HTTPS, and so on.
	Type string `json:"type"`
	// Status is the DNS answer's code: NOERROR, NXDOMAIN, SERVFAIL, and so on.
	Status string `json:"status"`
	// Blocked means AdGuard Home's filters answered, and Rule is the rule that did.
	Blocked   bool     `json:"blocked"`
	Rule      string   `json:"rule,omitempty"`
	Cached    bool     `json:"cached"`
	Answers   []string `json:"answers"`
	ElapsedMs float64  `json:"elapsed_ms"`
}

// DNSLog is a client's recent DNS queries (docs/PLAN.md §6.3). State is "off" (the AdGuard Home
// integration isn't turned on), "ok", or "error" (it couldn't be read, and Error says why).
type DNSLog struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// AdGuardURL is the address of AdGuard Home's API, for a link to its own query log.
	AdGuardURL string `json:"adguard_url,omitempty"`
	// Addresses are the client's, which the queries are from.
	Addresses []netip.Addr `json:"addresses"`
	Queries   []DNSQuery   `json:"queries"`
	// Warnings say why an empty log is empty, when AdGuard Home's settings are the reason.
	Warnings []string `json:"warnings"`
}

// NewDNSLog converts the service's DNS log.
func NewDNSLog(l service.DNSLog) DNSLog {
	out := DNSLog{
		State: l.State, Error: l.Error, AdGuardURL: l.BaseURL,
		Addresses: l.Addresses, Queries: []DNSQuery{}, Warnings: []string{},
	}
	if out.Addresses == nil {
		out.Addresses = []netip.Addr{}
	}
	for _, q := range l.Queries {
		answers := q.Answers
		if answers == nil {
			answers = []string{}
		}
		out.Queries = append(out.Queries, DNSQuery{
			Time: q.Time.UTC(), Address: q.Client, Domain: q.Domain, Type: q.Type, Status: q.Status,
			Blocked: q.Blocked, Rule: q.Rule, Cached: q.Cached, Answers: answers,
			ElapsedMs: float64(q.Elapsed) / float64(time.Millisecond),
		})
	}
	out.Warnings = append(out.Warnings, l.Warnings...)
	return out
}

// APIToken is a read-only API token, without its secret (docs/PLAN.md §6.5). The secret is shown
// once, when the token is made, and kept nowhere.
type APIToken struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Prefix is the start of the secret, to tell one token from another.
	Prefix    string    `json:"prefix"`
	Scope     string    `json:"scope"`
	CreatedAt time.Time `json:"created_at"`
	// LastUsedAt is null until the token is first used, and then is accurate to the hour.
	LastUsedAt *time.Time `json:"last_used_at"`
}

// NewAPIToken converts a stored token.
func NewAPIToken(t store.APIToken) APIToken {
	out := APIToken{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scope: t.Scope, CreatedAt: t.CreatedAt.UTC()}
	if !t.LastUsedAt.IsZero() {
		used := t.LastUsedAt.UTC()
		out.LastUsedAt = &used
	}
	return out
}

// APITokens converts a list of stored tokens.
func APITokens(list []store.APIToken) []APIToken {
	out := make([]APIToken, len(list))
	for i, t := range list {
		out[i] = NewAPIToken(t)
	}
	return out
}

// NewAPITokenRequest makes a token. It takes the password again, even in a logged-in session,
// because a token outlives the session.
type NewAPITokenRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// NewAPITokenResult is a new token and its secret, the only time the secret is shown.
type NewAPITokenResult struct {
	Token  APIToken `json:"token"`
	Secret string   `json:"secret"`
}
