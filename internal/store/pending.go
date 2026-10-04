package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/stuffam/drawbridge/internal/model"
)

var (
	// ErrChangePending means a settings change is on probation, and no other settings change
	// is accepted until it's kept or undone: undoing it restores the settings as they were, so
	// a change made meanwhile would be lost.
	ErrChangePending = errors.New("a settings change is waiting to be kept or undone; keep it or undo it first")
	// ErrNoPending means no settings change is on probation.
	ErrNoPending = errors.New("no settings change is waiting to be kept or undone")
)

// Probation describes a settings change that's applied but not yet kept (docs/PLAN.md §4.3).
type Probation struct {
	CreatedAt time.Time
	// Deadline is when the change is undone if it hasn't been kept.
	Deadline time.Time
	// Actor, Via, and SourceIP are who made the change, as in the event log.
	Actor, Via, SourceIP string
	// Changes is what changed, as {"setting": "old → new"}.
	Changes map[string]string
}

// PendingApply is a change on probation, with the settings it replaced.
type PendingApply struct {
	Probation
	// Previous is every setting as it was before the change, the server's private key included.
	Previous model.Settings
}

// settingsSnapshot is model.Settings as JSON, for the settings a pending change would restore.
// It lists the fields one by one, so the server's private key can't end up in it (the key is
// sealed apart), and TestSnapshotCarriesEveryField fails when a field is added to
// model.Settings and not here.
type settingsSnapshot struct {
	Interface        string         `json:"interface"`
	ListenPort       uint16         `json:"listen_port"`
	EndpointHost     string         `json:"endpoint_host"`
	EndpointPort     uint16         `json:"endpoint_port"`
	MTU              int            `json:"mtu"`
	IPv4             netip.Prefix   `json:"ipv4"`
	IPv6             netip.Prefix   `json:"ipv6"`
	DNS              []netip.Addr   `json:"dns"`
	Keepalive        int            `json:"keepalive"`
	ClientIsolation  bool           `json:"client_isolation"`
	ClientAllowedIPs []netip.Prefix `json:"client_allowed_ips"`
	AdminAllowed     []netip.Prefix `json:"admin_allowed"`
}

func snapshotOf(s model.Settings) settingsSnapshot {
	return settingsSnapshot{
		Interface: s.Interface, ListenPort: s.ListenPort, EndpointHost: s.EndpointHost,
		EndpointPort: s.EndpointPort, MTU: s.MTU, IPv4: s.IPv4, IPv6: s.IPv6, DNS: s.DNS,
		Keepalive: s.Keepalive, ClientIsolation: s.ClientIsolation,
		ClientAllowedIPs: s.ClientAllowedIPs, AdminAllowed: s.AdminAllowed,
	}
}

func (n settingsSnapshot) settings() model.Settings {
	return model.Settings{
		Interface: n.Interface, ListenPort: n.ListenPort, EndpointHost: n.EndpointHost,
		EndpointPort: n.EndpointPort, MTU: n.MTU, IPv4: n.IPv4, IPv6: n.IPv6, DNS: n.DNS,
		Keepalive: n.Keepalive, ClientIsolation: n.ClientIsolation,
		ClientAllowedIPs: n.ClientAllowedIPs, AdminAllowed: n.AdminAllowed,
	}
}

// UpdateSettingsWith is UpdateSettings that can put the change on probation. After fn and
// validation, decide sees the settings before and after; if it returns a Probation, the change
// is saved together with the settings it replaces, in one transaction, so there is never a
// change on probation without a way back, or the other way round. decide may be nil. While a
// change is on probation, no settings change is accepted (ErrChangePending).
func (s *Store) UpdateSettingsWith(ctx context.Context, fn func(*model.Settings) error,
	decide func(before, after model.Settings) *Probation) (model.Settings, *Probation, error) {
	var updated model.Settings
	var probation *Probation
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if has, err := s.hasPending(ctx, tx); err != nil {
			return err
		} else if has {
			return ErrChangePending
		}
		current, err := s.readSettings(ctx, tx)
		if err != nil {
			return err
		}
		next := current
		next.DNS = append([]netip.Addr(nil), current.DNS...)
		next.ClientAllowedIPs = append([]netip.Prefix(nil), current.ClientAllowedIPs...)
		next.AdminAllowed = append([]netip.Prefix(nil), current.AdminAllowed...)
		if err := fn(&next); err != nil {
			return err
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if next.IPv4 != current.IPv4 || next.IPv6 != current.IPv6 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients`).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return ErrHasClients
			}
		}
		if decide != nil {
			if probation = decide(current, next); probation != nil {
				if err := s.insertPending(ctx, tx, *probation, current); err != nil {
					return err
				}
			}
		}
		updated = next
		return s.writeSettings(ctx, tx, next, false)
	})
	if err != nil {
		return model.Settings{}, nil, err
	}
	return updated, probation, nil
}

func (s *Store) hasPending(ctx context.Context, q queryer) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM pending_apply`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) insertPending(ctx context.Context, tx *sql.Tx, p Probation, previous model.Settings) error {
	changes, err := json.Marshal(p.Changes)
	if err != nil {
		return err
	}
	snap, err := json.Marshal(snapshotOf(previous))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pending_apply (id, created_at, deadline, actor, via,
		source_ip, changes, previous, previous_key_enc) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(p.CreatedAt), formatTime(p.Deadline), p.Actor, p.Via, p.SourceIP,
		string(changes), string(snap), s.sealer.SealKey(previous.PrivateKey, serverKeyPurpose))
	return err
}

// PendingApply returns the change on probation, or nil when there is none.
func (s *Store) PendingApply(ctx context.Context) (*PendingApply, error) {
	return s.readPending(ctx, s.db)
}

func (s *Store) readPending(ctx context.Context, q queryer) (*PendingApply, error) {
	var (
		p                                    PendingApply
		createdAt, deadline, changes, before string
		keyEnc                               []byte
	)
	err := q.QueryRowContext(ctx, `SELECT created_at, deadline, actor, via, source_ip, changes,
		previous, previous_key_enc FROM pending_apply WHERE id = 1`).Scan(
		&createdAt, &deadline, &p.Actor, &p.Via, &p.SourceIP, &changes, &before, &keyEnc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return nil, fmt.Errorf("pending change: %w", err)
	}
	if p.Deadline, err = time.Parse(time.RFC3339Nano, deadline); err != nil {
		return nil, fmt.Errorf("pending change: %w", err)
	}
	if err := json.Unmarshal([]byte(changes), &p.Changes); err != nil {
		return nil, fmt.Errorf("pending change: %w", err)
	}
	var snap settingsSnapshot
	if err := json.Unmarshal([]byte(before), &snap); err != nil {
		return nil, fmt.Errorf("pending change's previous settings: %w", err)
	}
	p.Previous = snap.settings()
	if p.Previous.PrivateKey, err = s.sealer.OpenKey(keyEnc, serverKeyPurpose); err != nil {
		return nil, fmt.Errorf("pending change's previous key: %w", err)
	}
	return &p, nil
}

// ResolvePending ends the probation. Keeping the change leaves the settings as they are, and
// undoing it writes the settings it replaced back, in the same transaction that deletes the
// pending row. It returns the change it resolved, and ErrNoPending when there was none (it was
// already kept, or undone).
func (s *Store) ResolvePending(ctx context.Context, keep bool) (PendingApply, error) {
	var resolved PendingApply
	err := s.tx(ctx, func(tx *sql.Tx) error {
		p, err := s.readPending(ctx, tx)
		if err != nil {
			return err
		}
		if p == nil {
			return ErrNoPending
		}
		if !keep {
			if err := p.Previous.Validate(); err != nil {
				return fmt.Errorf("the settings to restore: %w", err)
			}
			if err := s.writeSettings(ctx, tx, p.Previous, false); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM pending_apply`); err != nil {
			return err
		}
		resolved = *p
		return nil
	})
	return resolved, err
}
