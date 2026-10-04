// Package reconcile applies the database's desired state to the kernel: the WireGuard
// interface, its peers and addresses, and the nftables table (docs/PLAN.md §4.3,
// ADR 0004). It's idempotent: running it again changes nothing unless something drifted.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"sync"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/stuffam/drawbridge/internal/firewall"
	"github.com/stuffam/drawbridge/internal/lan"
	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/wg"
)

// State is the desired state's source: the store.
type State interface {
	Settings(ctx context.Context) (model.Settings, error)
	Clients(ctx context.Context) ([]model.Client, error)
}

// Firewall applies and inspects the nftables table.
type Firewall interface {
	Apply(ctx context.Context, rs firewall.Ruleset) error
	Remove(ctx context.Context) error
	Revision(ctx context.Context) (string, bool, error)
}

// Locker serializes reconciles across processes: the tunnel unit and the daemon.
type Locker interface {
	Lock() (unlock func(), err error)
}

// Reconciler applies desired state to the kernel.
type Reconciler struct {
	State    State
	WG       wg.Backend
	Firewall Firewall
	// Lock, when set, is held around every run, in addition to an in-process mutex.
	Lock Locker
	Log  *slog.Logger
	// AdminPort, when set, adds the firewall rules that keep the web UI's port to the
	// allowlist: loopback, link-local, the VPN, and LAN's subnets, which LAN returns
	// (nil for none).
	AdminPort uint16
	LAN       func() []netip.Prefix

	mu sync.Mutex
}

// Result describes one run.
type Result struct {
	// TunnelDown means the interface doesn't exist and the run was a Sync, so nothing
	// was applied: the tunnel's lifecycle belongs to drawbridge-tunnel.service (ADR 0008).
	TunnelDown bool
	// Changes lists what the run changed, for the log. It's empty when nothing drifted.
	Changes []string
}

// Up brings the tunnel up: it creates the interface if it's missing, then applies
// everything. drawbridge-tunnel.service runs it at boot.
func (r *Reconciler) Up(ctx context.Context) (Result, error) {
	return r.run(ctx, true)
}

// Sync applies everything to an existing interface. If the interface is missing, it
// does nothing and reports TunnelDown, so the daemon never brings up a tunnel the admin
// stopped. The daemon runs it after every change and every 30 seconds.
func (r *Reconciler) Sync(ctx context.Context) (Result, error) {
	return r.run(ctx, false)
}

// Down deletes the interface and the nftables table.
func (r *Reconciler) Down(ctx context.Context) error {
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s, err := r.State.Settings(ctx)
	if err != nil {
		return err
	}
	return errors.Join(r.WG.Delete(s.Interface), r.Firewall.Remove(ctx))
}

func (r *Reconciler) lock() (func(), error) {
	r.mu.Lock()
	if r.Lock == nil {
		return r.mu.Unlock, nil
	}
	unlock, err := r.Lock.Lock()
	if err != nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("taking the reconcile lock: %w", err)
	}
	return func() { unlock(); r.mu.Unlock() }, nil
}

// target is what a run changes: the WireGuard interface and the firewall. A dry run
// (Plan) swaps in versions that change nothing, so it takes the same steps and reports what
// each would have done.
type target struct {
	wg wg.Backend
	fw Firewall
}

func (r *Reconciler) run(ctx context.Context, create bool) (Result, error) {
	return r.runOn(ctx, create, target{wg: r.WG, fw: r.Firewall})
}

// Plan reports what Sync would change, and changes nothing: the changes are the lines Sync
// would log, and an interface that doesn't exist reports TunnelDown, as Sync does.
func (r *Reconciler) Plan(ctx context.Context) (Result, error) {
	return r.runOn(ctx, false, target{wg: dryWG{r.WG}, fw: dryFirewall{r.Firewall}})
}

// dryWG reads the real interface and changes nothing.
type dryWG struct{ wg.Backend }

func (dryWG) Create(string) error                    { return nil }
func (dryWG) Delete(string) error                    { return nil }
func (dryWG) Configure(string, wgtypes.Config) error { return nil }
func (dryWG) SetMTU(string, int) error               { return nil }
func (dryWG) AddAddr(string, netip.Prefix) error     { return nil }
func (dryWG) DelAddr(string, netip.Prefix) error     { return nil }
func (dryWG) SetUp(string) error                     { return nil }

// dryFirewall reads the real table's revision and applies nothing.
type dryFirewall struct{ Firewall }

func (dryFirewall) Apply(context.Context, firewall.Ruleset) error { return nil }
func (dryFirewall) Remove(context.Context) error                  { return nil }

func (r *Reconciler) runOn(ctx context.Context, create bool, t target) (Result, error) {
	unlock, err := r.lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	s, err := r.State.Settings(ctx)
	if err != nil {
		return Result{}, err
	}
	clients, err := r.State.Clients(ctx)
	if err != nil {
		return Result{}, err
	}

	var res Result
	dev, err := t.wg.Device(s.Interface)
	if errors.Is(err, wg.ErrNoDevice) {
		if !create {
			return Result{TunnelDown: true}, nil
		}
		if err := t.wg.Create(s.Interface); err != nil {
			return res, err
		}
		res.Changes = append(res.Changes, "created "+s.Interface)
		dev, err = t.wg.Device(s.Interface)
	}
	if err != nil {
		return res, err
	}

	// The firewall goes first, so its rules exist before the interface comes up. A
	// firewall failure doesn't stop the tunnel from being configured.
	fwErr := r.syncFirewall(ctx, t, s, &res)
	wgErr := r.syncDevice(t, s, clients, dev, &res)
	return res, errors.Join(fwErr, wgErr)
}

func (r *Reconciler) syncFirewall(ctx context.Context, t target, s model.Settings, res *Result) error {
	rules := firewall.Rules{
		Interface:       s.Interface,
		IPv4:            s.IPv4,
		IPv6:            s.IPv6,
		ClientIsolation: s.ClientIsolation,
		AdminPort:       r.AdminPort,
	}
	if r.AdminPort != 0 {
		var lanPrefixes []netip.Prefix
		if r.LAN != nil {
			lanPrefixes = r.LAN()
		}
		rules.AdminAllowed = lan.Allowlist(s.AdminSources(), lanPrefixes)
	}
	rs := firewall.Render(rules)
	current, exists, err := t.fw.Revision(ctx)
	if err != nil {
		return err
	}
	if exists && current == rs.Revision {
		return nil
	}
	if err := t.fw.Apply(ctx, rs); err != nil {
		return err
	}
	res.Changes = append(res.Changes, "applied nftables revision "+rs.Revision)
	return nil
}

func (r *Reconciler) syncDevice(t target, s model.Settings, clients []model.Client, dev wg.Device, res *Result) error {
	name := s.Interface
	cfg := wgtypes.Config{}
	changed := false

	if dev.PrivateKey != s.PrivateKey {
		cfg.PrivateKey = &s.PrivateKey
		changed = true
		res.Changes = append(res.Changes, "set the private key")
	}
	if dev.ListenPort != int(s.ListenPort) {
		port := int(s.ListenPort)
		cfg.ListenPort = &port
		changed = true
		res.Changes = append(res.Changes, fmt.Sprintf("set the listen port to %d", port))
	}

	peerChanges, peerLog := diffPeers(clients, dev.Peers)
	if len(peerChanges) > 0 {
		cfg.Peers = peerChanges
		changed = true
		res.Changes = append(res.Changes, peerLog...)
	}
	if changed {
		if err := t.wg.Configure(name, cfg); err != nil {
			return fmt.Errorf("configuring %s: %w", name, err)
		}
	}

	if dev.MTU != s.MTU {
		if err := t.wg.SetMTU(name, s.MTU); err != nil {
			return fmt.Errorf("setting the MTU of %s: %w", name, err)
		}
		res.Changes = append(res.Changes, fmt.Sprintf("set the MTU to %d", s.MTU))
	}

	if err := r.syncAddrs(t, s, dev, res); err != nil {
		return err
	}

	if !dev.Up {
		if err := t.wg.SetUp(name); err != nil {
			return fmt.Errorf("bringing %s up: %w", name, err)
		}
		res.Changes = append(res.Changes, "brought "+name+" up")
	}
	return nil
}

func (r *Reconciler) syncAddrs(t target, s model.Settings, dev wg.Device, res *Result) error {
	srv, err := s.ServerAddrs()
	if err != nil {
		return err
	}
	want := []netip.Prefix{netip.PrefixFrom(srv.IPv4, s.IPv4.Bits())}
	if srv.IPv6.IsValid() {
		want = append(want, netip.PrefixFrom(srv.IPv6, s.IPv6.Bits()))
	}
	for _, a := range dev.Addrs {
		if !slices.Contains(want, a) {
			if err := t.wg.DelAddr(s.Interface, a); err != nil {
				return fmt.Errorf("removing address %s: %w", a, err)
			}
			res.Changes = append(res.Changes, "removed address "+a.String())
		}
	}
	for _, a := range want {
		if !slices.Contains(dev.Addrs, a) {
			if err := t.wg.AddAddr(s.Interface, a); err != nil {
				return fmt.Errorf("adding address %s: %w", a, err)
			}
			res.Changes = append(res.Changes, "added address "+a.String())
		}
	}
	return nil
}

// diffPeers returns the peer changes that turn current into the enabled clients, and a
// log line for each. Paused clients have no peer at all, so they can't complete a
// handshake (docs/PLAN.md §6.1). Unchanged peers aren't touched, so they keep their
// sessions.
func diffPeers(clients []model.Client, current []wg.Peer) ([]wgtypes.PeerConfig, []string) {
	want := map[wgtypes.Key]model.Client{}
	for _, c := range clients {
		if c.Enabled {
			want[c.PublicKey] = c
		}
	}
	have := map[wgtypes.Key]wg.Peer{}
	for _, p := range current {
		have[p.PublicKey] = p
	}

	var changes []wgtypes.PeerConfig
	var log []string
	for _, p := range current {
		if _, ok := want[p.PublicKey]; !ok {
			changes = append(changes, wgtypes.PeerConfig{PublicKey: p.PublicKey, Remove: true})
			log = append(log, "removed peer "+p.PublicKey.String())
		}
	}
	for _, c := range clients {
		if !c.Enabled {
			continue
		}
		p, exists := have[c.PublicKey]
		allowed := c.AllowedIPs()
		if exists && p.PresharedKey == c.PresharedKey && samePrefixes(p.AllowedIPs, allowed) {
			continue
		}
		psk := c.PresharedKey
		pc := wgtypes.PeerConfig{
			PublicKey:         c.PublicKey,
			PresharedKey:      &psk,
			ReplaceAllowedIPs: true,
		}
		for _, a := range allowed {
			pc.AllowedIPs = append(pc.AllowedIPs, wg.IPNet(a))
		}
		changes = append(changes, pc)
		if exists {
			log = append(log, "updated peer "+c.PublicKey.String())
		} else {
			log = append(log, "added peer "+c.PublicKey.String())
		}
	}
	return changes, log
}

func samePrefixes(a, b []netip.Prefix) bool {
	if len(a) != len(b) {
		return false
	}
	for _, p := range a {
		if !slices.Contains(b, p) {
			return false
		}
	}
	return true
}
