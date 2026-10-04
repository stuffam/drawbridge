package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// TOTP two-factor authentication for the admin account (docs/PLAN.md §6.5). The service decides
// what is allowed; the checks that must be atomic with the writes (a code is used once, a
// recovery code is used once, an enrollment is turned on once) are here.

var (
	// ErrTOTPEnabled means 2FA is already on for the account.
	ErrTOTPEnabled = errors.New("two-factor authentication is already on")
	// ErrTOTPOff means 2FA isn't on for the account.
	ErrTOTPOff = errors.New("two-factor authentication isn't on")
	// ErrNoEnrollment means no enrollment is waiting for its first code.
	ErrNoEnrollment = errors.New("two-factor authentication isn't being set up; start again")
)

// totpPurpose ties a sealed secret to its account, so it can't be swapped for another's.
func totpPurpose(userID string) string { return "user/" + userID + "/totp" }

func parseRecoveryHashes(ns sql.NullString) ([]string, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	var hashes []string
	if err := json.Unmarshal([]byte(ns.String), &hashes); err != nil {
		return nil, fmt.Errorf("the recovery codes in the database are damaged: %w", err)
	}
	return hashes, nil
}

func encodeRecoveryHashes(hashes []string) (any, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(hashes)
	return string(b), err
}

// SetPendingTOTP stores a new secret for an account that has 2FA off, and starts an enrollment
// that waits for its first code (EnableTOTP). It replaces the secret of an enrollment that was
// started before. It returns ErrTOTPEnabled if 2FA is already on.
func (s *Store) SetPendingTOTP(ctx context.Context, userID string, secret []byte) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var enabled sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT totp_enabled_at FROM users WHERE id = ?`, userID).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoUser
		}
		if err != nil {
			return err
		}
		if enabled.Valid {
			return ErrTOTPEnabled
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET totp_secret_enc = ?, totp_last_step = 0 WHERE id = ?`,
			s.sealer.Seal(secret, totpPurpose(userID)), userID)
		return err
	})
}

// TOTPSecret returns an account's secret, whether 2FA is on or an enrollment is waiting. It
// returns ErrNoEnrollment if there is none.
func (s *Store) TOTPSecret(ctx context.Context, userID string) ([]byte, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, `SELECT totp_secret_enc FROM users WHERE id = ?`, userID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoUser
	}
	if err != nil {
		return nil, err
	}
	if sealed == nil {
		return nil, ErrNoEnrollment
	}
	return s.sealer.Open(sealed, totpPurpose(userID))
}

// EnableTOTP turns 2FA on for an account whose enrollment has had its first code, which was for
// time step `step`. recoveryHashes are the hashes of its new recovery codes. Every session except
// keep ends, as when the password changes, so one that was open before can't carry on without the
// second factor. It returns ErrNoEnrollment if there is no waiting enrollment, and ErrTOTPEnabled
// if 2FA is on already.
func (s *Store) EnableTOTP(ctx context.Context, userID string, step int64, recoveryHashes []string, keep string) error {
	codes, err := encodeRecoveryHashes(recoveryHashes)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		var (
			sealed  []byte
			enabled sql.NullString
		)
		err := tx.QueryRowContext(ctx, `SELECT totp_secret_enc, totp_enabled_at FROM users WHERE id = ?`, userID).
			Scan(&sealed, &enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoUser
		}
		if err != nil {
			return err
		}
		if enabled.Valid {
			return ErrTOTPEnabled
		}
		if sealed == nil {
			return ErrNoEnrollment
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET totp_enabled_at = ?, totp_last_step = ?,
			recovery_codes_hash = ? WHERE id = ?`, s.timestamp(), step, codes, userID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id = ? AND id != ?`, userID, keep)
		return err
	})
}

// DisableTOTP turns 2FA off for an account, and forgets its secret, any enrollment that was
// waiting, and its recovery codes. If it was on, every session except keep ("" for none) ends.
// It reports whether it was on.
func (s *Store) DisableTOTP(ctx context.Context, userID, keep string) (wasOn bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var enabled sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT totp_enabled_at FROM users WHERE id = ?`, userID).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoUser
		}
		if err != nil {
			return err
		}
		wasOn = enabled.Valid
		if _, err := tx.ExecContext(ctx, `UPDATE users SET totp_secret_enc = NULL, totp_enabled_at = NULL,
			totp_last_step = 0, recovery_codes_hash = NULL WHERE id = ?`, userID); err != nil {
			return err
		}
		if !wasOn {
			return nil
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id = ? AND id != ?`, userID, keep)
		return err
	})
	return wasOn, err
}

// UseTOTPStep records that the code for a time step was accepted, if the step is later than the
// last one that was. It reports whether it was: false means the code was used already, or an
// earlier code was used since. Two logins that present the same code at once can't both pass,
// because only one update changes the row.
func (s *Store) UseTOTPStep(ctx context.Context, userID string, step int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET totp_last_step = ?
		WHERE id = ? AND totp_enabled_at IS NOT NULL AND totp_last_step < ?`, step, userID, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UseRecoveryCode spends the recovery code with the given hash, if the account has it. It reports
// whether it did, and how many codes are left.
func (s *Store) UseRecoveryCode(ctx context.Context, userID, hash string) (ok bool, left int, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var stored sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT recovery_codes_hash FROM users WHERE id = ?`, userID).Scan(&stored)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoUser
		}
		if err != nil {
			return err
		}
		hashes, err := parseRecoveryHashes(stored)
		if err != nil {
			return err
		}
		// Every hash is compared, so the time doesn't say where in the list a code was.
		found := -1
		for i, h := range hashes {
			if subtle.ConstantTimeCompare([]byte(h), []byte(hash)) == 1 {
				found = i
			}
		}
		left = len(hashes)
		if found < 0 {
			return nil
		}
		rest := append(hashes[:found:found], hashes[found+1:]...)
		codes, err := encodeRecoveryHashes(rest)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET recovery_codes_hash = ? WHERE id = ?`, codes, userID); err != nil {
			return err
		}
		ok, left = true, len(rest)
		return nil
	})
	return ok, left, err
}

// ReplaceRecoveryCodes replaces an account's unused recovery codes with new ones. It returns
// ErrTOTPOff if the account doesn't have 2FA on.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes []string) error {
	codes, err := encodeRecoveryHashes(hashes)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET recovery_codes_hash = ?
		WHERE id = ? AND totp_enabled_at IS NOT NULL`, codes, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrTOTPOff
	}
	return nil
}
