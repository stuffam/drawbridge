package service

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/reconcile"
	"github.com/stuffam/drawbridge/internal/store"
)

// Safe apply (commit-confirm, docs/PLAN.md §4.3): a settings change that could cut the admin
// off is applied at once and undone after SafeApplyWindow unless the admin keeps it. The web UI
// asks for it on every change; the CLI applies at once unless it's told to (--safe).
//
// The change and the settings it replaced are saved together (store.UpdateSettingsWith), so a
// restart or a reboot inside the window still undoes it: the daemon looks again when it starts.

// DefaultSafeApplyWindow is how long the admin has to keep a change on probation.
const DefaultSafeApplyWindow = time.Minute

// ErrChangeExpired means the admin tried to keep a change after its time was up, so it was
// undone instead.
var ErrChangeExpired = errors.New("the change was undone because it wasn't kept in time")

// PendingChange is a settings change on probation: applied, and undone at Deadline unless it's
// kept first.
type PendingChange struct {
	Deadline time.Time
	// Changes is what changed, as {"setting": "old → new"}.
	Changes map[string]string
	// Actor, Via, and SourceIP are who made the change.
	Actor, Via, SourceIP string
}

func pendingFrom(p store.Probation) *PendingChange {
	return &PendingChange{Deadline: p.Deadline, Changes: maps.Clone(p.Changes), Actor: p.Actor, Via: p.Via, SourceIP: p.SourceIP}
}

func (s *Service) safeApplyWindow() time.Duration {
	if s.SafeApplyWindow > 0 {
		return s.SafeApplyWindow
	}
	return DefaultSafeApplyWindow
}

// needsConfirmation reports whether a settings change can cut the admin off, and so waits to be
// kept when it's made with SafeApply. It's what the plan lists that exists today:
//
//   - The listen port. Clients, the admin's own VPN included, keep sending to the old one until
//     they import a config that names the new one, and the router forwards only the old one.
//   - Removing a source from the admin UI's allowlist, which can be the one the admin is on.
//
// Everything else either takes effect for new configs only (the endpoint, DNS, keepalive), or
// can't lock anyone out (the MTU, client isolation, adding a source). Rotating the server's key
// and changing the subnets will be on this list when they exist.
func needsConfirmation(before, after model.Settings) bool {
	if before.ListenPort != after.ListenPort {
		return true
	}
	for _, p := range before.AdminAllowed {
		if !slices.Contains(after.AdminAllowed, p) {
			return true
		}
	}
	return false
}

// safeApply is the service's state for the change on probation. The database is the truth; this
// is what the daemon's loop checks every second without reading it.
type safeApply struct {
	// mu serializes ending a probation: keeping, undoing, and letting it run out.
	mu sync.Mutex
	// cacheMu guards pending.
	cacheMu sync.Mutex
	pending *PendingChange
}

func (s *Service) cachedPending() *PendingChange {
	s.safe.cacheMu.Lock()
	defer s.safe.cacheMu.Unlock()
	if s.safe.pending == nil {
		return nil
	}
	p := *s.safe.pending
	return &p
}

func (s *Service) setCachedPending(p *PendingChange) {
	s.safe.cacheMu.Lock()
	s.safe.pending = p
	s.safe.cacheMu.Unlock()
}

// PendingChange returns the settings change waiting to be kept, or nil when there is none.
func (s *Service) PendingChange(ctx context.Context) (*PendingChange, error) {
	p, err := s.Store.PendingApply(ctx)
	if err != nil || p == nil {
		return nil, err
	}
	return pendingFrom(p.Probation), nil
}

// ConfirmChange keeps the change on probation. If its time is up, it was undone instead, and
// the answer is ErrChangeExpired: a click that arrives after the deadline doesn't get to keep it.
func (s *Service) ConfirmChange(ctx context.Context) (PendingChange, error) {
	s.safe.mu.Lock()
	defer s.safe.mu.Unlock()
	p, err := s.Store.PendingApply(ctx)
	if err != nil {
		return PendingChange{}, err
	}
	if p == nil {
		return PendingChange{}, store.ErrNoPending
	}
	if !s.now().Before(p.Deadline) {
		if _, _, err := s.undoLocked(ctx, true); err != nil {
			return PendingChange{}, err
		}
		return PendingChange{}, ErrChangeExpired
	}
	res, err := s.Store.ResolvePending(ctx, true)
	if err != nil {
		return PendingChange{}, err
	}
	s.setCachedPending(nil)
	s.record(ctx, Event{Kind: "server.settings_kept", Data: maps.Clone(res.Changes)})
	return *pendingFrom(res.Probation), nil
}

// RevertChange undoes the change on probation now, without waiting for its time to run out.
func (s *Service) RevertChange(ctx context.Context) (PendingChange, Applied, error) {
	s.safe.mu.Lock()
	defer s.safe.mu.Unlock()
	return s.undoLocked(ctx, false)
}

// undoLocked restores the settings the change replaced, and applies them. timedOut says the
// admin didn't keep it in time, which is the daemon's doing and not theirs.
func (s *Service) undoLocked(ctx context.Context, timedOut bool) (PendingChange, Applied, error) {
	res, err := s.Store.ResolvePending(ctx, false)
	if err != nil {
		return PendingChange{}, Applied{}, err
	}
	s.setCachedPending(nil)
	data := maps.Clone(res.Changes)
	e := Event{Kind: "server.settings_undone", Data: data}
	if timedOut {
		// The daemon did this, and not the admin whose click came late, if one did.
		ctx = WithActor(ctx, Actor{Name: "drawbridge", Via: ViaSystem})
		e.Kind, e.Category = "server.settings_expired", CategorySystem
		data["reason"] = "not kept within " + s.safeApplyWindow().String()
	}
	s.record(ctx, e)
	return *pendingFrom(res.Probation), s.apply(ctx, "the undone settings"), nil
}

// RunSafeApply undoes a change that isn't kept in time. It looks for one already waiting when
// the daemon starts (a restart inside the window, or a reboot), then checks every second until
// ctx ends.
func (s *Service) RunSafeApply(ctx context.Context) {
	s.loadPending(ctx)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		s.expirePending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// loadPending reads the change on probation from the database into the cache the loop checks.
func (s *Service) loadPending(ctx context.Context) {
	if p, err := s.PendingChange(ctx); err != nil {
		s.Log.Warn("can't look for a settings change waiting to be kept", "err", err)
	} else {
		s.setCachedPending(p)
	}
}

// expirePending undoes the change on probation if its time is up.
func (s *Service) expirePending(ctx context.Context) {
	if p := s.cachedPending(); p == nil || s.now().Before(p.Deadline) {
		return
	}
	s.safe.mu.Lock()
	defer s.safe.mu.Unlock()
	p, err := s.Store.PendingApply(ctx)
	switch {
	case err != nil:
		s.Log.Warn("can't read the settings change waiting to be kept", "err", err)
		return
	case p == nil:
		s.setCachedPending(nil) // kept or undone meanwhile
		return
	case s.now().Before(p.Deadline):
		return
	}
	if _, applied, err := s.undoLocked(ctx, true); err != nil {
		s.Log.Error("can't undo a settings change that wasn't kept", "err", err)
	} else if w := applied.Warning(); w != "" {
		s.Log.Warn("undid a settings change that wasn't kept", "warning", w)
	}
}

// Apply reconciles once and returns what changed (`drawbridge apply`). With dryRun it changes
// nothing and returns what a real run would. A run that changes something is an event: it
// corrected what the settings and the kernel disagreed on.
func (s *Service) Apply(ctx context.Context, dryRun bool) (reconcile.Result, error) {
	if dryRun {
		return s.Rec.Plan(ctx)
	}
	res, err := s.Rec.Sync(ctx)
	if err == nil && len(res.Changes) > 0 {
		s.record(ctx, Event{Kind: "tunnel.applied", Data: map[string]string{"changes": strings.Join(res.Changes, "; ")}})
	}
	return res, err
}
