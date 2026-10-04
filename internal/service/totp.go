package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/stuffam/drawbridge/internal/auth"
	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/store"
)

// TOTP two-factor authentication (docs/PLAN.md §6.5). Logging in with it is in auth.go
// (LoginWithCode). Here: turning it on and off, and the recovery codes.

// totpIssuer is the name an authenticator app shows beside the account.
const totpIssuer = "Drawbridge"

var (
	// ErrTOTPRequired means the password was right, and the account needs a code as well.
	ErrTOTPRequired = errors.New("enter the code from your authenticator app")
	// ErrBadCode means a code was wrong, or had been used already. It is the same for both, so it
	// doesn't tell anyone which codes have been seen.
	ErrBadCode = errors.New("that code is wrong, or has been used already")
)

// TOTPEnrollment is a new secret waiting for the admin's authenticator app to prove it has it.
type TOTPEnrollment struct {
	// Secret is for typing into an app that can't scan a QR code: base32.
	Secret string
	// URI is the otpauth:// address a QR code holds.
	URI string
}

// EnrollTOTP starts turning 2FA on: it makes a secret and keeps it, waiting for the first code
// from the admin's app (EnableTOTP). It takes the password again, even in a logged-in session,
// because whoever turns 2FA on decides what the second factor is: a hijacked session that could
// do it would lock the admin out, and keep the way in. Nothing changes at login until the first
// code is proved, so a secret that never is does no harm. Starting again replaces it.
func (s *Service) EnrollTOTP(ctx context.Context, u store.User, password string) (TOTPEnrollment, error) {
	if u.TOTPEnabled() {
		return TOTPEnrollment{}, store.ErrTOTPEnabled
	}
	if err := s.confirmPassword(ctx, u, password, "auth.totp_failed"); err != nil {
		return TOTPEnrollment{}, err
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if err := s.Store.SetPendingTOTP(ctx, u.ID, secret); err != nil {
		return TOTPEnrollment{}, err
	}
	return TOTPEnrollment{
		Secret: auth.EncodeTOTPSecret(secret),
		URI:    auth.TOTPURI(totpIssuer, u.Username, secret),
	}, nil
}

// EnableTOTP finishes turning 2FA on, with the first code from the admin's app, and returns the
// recovery codes: the only time they are shown. The account's other sessions end, as when its
// password changes. A wrong code counts against the same limits as a wrong password.
func (s *Service) EnableTOTP(ctx context.Context, u store.User, current store.Session, code string) ([]string, error) {
	if u.TOTPEnabled() {
		return nil, store.ErrTOTPEnabled
	}
	keys := []string{sourceKey(ctx), auth.AccountKey(u.ID)}
	if wait := s.Limiter.Wait(keys...); wait > 0 {
		return nil, &RateLimitedError{Wait: wait}
	}
	secret, err := s.Store.TOTPSecret(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	step, ok := auth.VerifyTOTP(secret, code, s.now(), 0)
	if !ok {
		s.Limiter.Fail(keys...)
		s.record(ctx, Event{Kind: "auth.totp_failed", Data: map[string]string{"reason": "wrong code"}})
		return nil, &model.InvalidError{Err: ErrBadCode}
	}
	codes, hashes, err := auth.NewRecoveryCodes()
	if err != nil {
		return nil, err
	}
	// The step is recorded as used, so the code that proved the app can't also log in.
	if err := s.Store.EnableTOTP(ctx, u.ID, step, hashes, current.ID); err != nil {
		return nil, err
	}
	s.Limiter.Succeed(keys...)
	s.record(ctx, Event{Kind: "auth.totp_enabled"})
	return codes, nil
}

// DisableTOTP turns 2FA off. It takes the password again and a code (an authenticator code, or a
// recovery code), so a hijacked session can't take the second factor away. The account's other
// sessions end.
func (s *Service) DisableTOTP(ctx context.Context, u store.User, current store.Session, password, code string) error {
	if !u.TOTPEnabled() {
		return store.ErrTOTPOff
	}
	if err := s.confirmFactors(ctx, u, password, code); err != nil {
		return err
	}
	if _, err := s.Store.DisableTOTP(ctx, u.ID, current.ID); err != nil {
		return err
	}
	s.record(ctx, Event{Kind: "auth.totp_disabled"})
	return nil
}

// NewRecoveryCodes replaces the account's recovery codes with a new set and returns them: the
// only time they are shown. The old ones stop working. It takes the password again and a code,
// like turning 2FA off.
func (s *Service) NewRecoveryCodes(ctx context.Context, u store.User, password, code string) ([]string, error) {
	if !u.TOTPEnabled() {
		return nil, store.ErrTOTPOff
	}
	if err := s.confirmFactors(ctx, u, password, code); err != nil {
		return nil, err
	}
	codes, hashes, err := auth.NewRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.Store.ReplaceRecoveryCodes(ctx, u.ID, hashes); err != nil {
		return nil, err
	}
	s.record(ctx, Event{Kind: "auth.recovery_codes_renewed"})
	return codes, nil
}

// DisableTOTPFor turns 2FA off for an account (the named one, or the only one if username is
// empty) without its codes, and lifts any lockout: the recovery for an admin who lost their app
// and their recovery codes, from the command line. Whoever can reach the control socket can
// already reset the password, so this takes no more than that. It ends the account's sessions if
// 2FA was on, and reports whether it was.
func (s *Service) DisableTOTPFor(ctx context.Context, username string) (u store.User, wasOn bool, err error) {
	if username == "" {
		u, err = s.Store.OnlyUser(ctx)
	} else {
		u, err = s.Store.UserByName(ctx, username)
	}
	if err != nil {
		return store.User{}, false, err
	}
	if wasOn, err = s.Store.DisableTOTP(ctx, u.ID, ""); err != nil {
		return store.User{}, false, err
	}
	s.Limiter.Succeed(auth.AccountKey(u.ID))
	if wasOn {
		s.record(ctx, Event{Kind: "auth.totp_disabled", Data: map[string]string{"username": u.Username}})
	}
	return u, wasOn, nil
}

// confirmFactors checks the password and a code of a logged-in account, before a change to its
// second factor. Both count against the login limits when wrong, and the failures are forgiven
// only when both are right: forgiving them after the password alone would let a session that has
// the password try codes without limit.
func (s *Service) confirmFactors(ctx context.Context, u store.User, password, code string) error {
	keys := []string{sourceKey(ctx), auth.AccountKey(u.ID)}
	if wait := s.Limiter.Wait(keys...); wait > 0 {
		return &RateLimitedError{Wait: wait}
	}
	if err := s.checkPassword(ctx, u, password, "auth.totp_failed", keys); err != nil {
		return err
	}
	recovery, left, err := s.useSecondFactor(ctx, u, code)
	if errors.Is(err, ErrBadCode) {
		s.Limiter.Fail(keys...)
		s.record(ctx, Event{Kind: "auth.totp_failed", Data: map[string]string{"reason": "wrong code"}})
		return &model.InvalidError{Err: ErrBadCode}
	}
	if err != nil {
		return err
	}
	s.Limiter.Succeed(keys...)
	if recovery {
		s.record(ctx, Event{Kind: "auth.recovery_code_used", Data: map[string]string{"left": strconv.Itoa(left)}})
	}
	return nil
}

// useSecondFactor checks a code against an account that has 2FA on, and uses it up: the six
// digits from the authenticator app, or a recovery code. It returns ErrBadCode if it is neither,
// or has been used. It leaves the limiter to the caller. For a recovery code it also returns how
// many are left.
func (s *Service) useSecondFactor(ctx context.Context, u store.User, code string) (recovery bool, left int, err error) {
	if digits, ok := auth.NormalizeTOTPCode(code); ok {
		secret, err := s.Store.TOTPSecret(ctx, u.ID)
		if err != nil {
			return false, 0, err
		}
		step, ok := auth.VerifyTOTP(secret, digits, s.now(), u.TOTPLastStep)
		if !ok {
			return false, 0, ErrBadCode
		}
		// The step is spent in the database, in one statement that only one of two logins with the
		// same code can win.
		if used, err := s.Store.UseTOTPStep(ctx, u.ID, step); err != nil {
			return false, 0, err
		} else if !used {
			return false, 0, ErrBadCode
		}
		return false, 0, nil
	}
	if auth.LooksLikeRecoveryCode(code) {
		used, left, err := s.Store.UseRecoveryCode(ctx, u.ID, auth.HashRecoveryCode(code))
		if err != nil {
			return false, 0, err
		}
		if !used {
			return false, 0, ErrBadCode
		}
		return true, left, nil
	}
	return false, 0, ErrBadCode
}
