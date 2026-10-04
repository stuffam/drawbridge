package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func totpUser(t *testing.T) (*Store, string, User) {
	t.Helper()
	s, path := openTest(t)
	return s, path, tokenUser(t, s)
}

func session(t *testing.T, s *Store, u User, id string) {
	t.Helper()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	err := s.CreateSession(context.Background(), Session{
		ID: id, TokenHash: []byte("hash-" + id), UserID: u.ID, CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(time.Hour), IP: "192.168.4.20", UserAgent: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func sessionIDs(t *testing.T, s *Store, u User) []string {
	t.Helper()
	list, err := s.Sessions(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, sess := range list {
		ids = append(ids, sess.ID)
	}
	return ids
}

func TestTOTPEnrollment(t *testing.T) {
	ctx := context.Background()
	s, _, u := totpUser(t)
	if u.TOTPEnabled() || u.RecoveryCodesLeft != 0 || u.TOTPLastStep != 0 {
		t.Fatalf("a new account has 2FA: %+v", u)
	}
	if _, err := s.TOTPSecret(ctx, u.ID); !errors.Is(err, ErrNoEnrollment) {
		t.Fatalf("TOTPSecret with none: %v", err)
	}
	if err := s.EnableTOTP(ctx, u.ID, 5, []string{"a"}, ""); !errors.Is(err, ErrNoEnrollment) {
		t.Fatalf("EnableTOTP with no enrollment: %v", err)
	}

	// An enrollment that waits for its first code changes nothing about logging in.
	secret := []byte("the first secret, 20")
	if err := s.SetPendingTOTP(ctx, u.ID, secret); err != nil {
		t.Fatal(err)
	}
	if got, err := s.TOTPSecret(ctx, u.ID); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("TOTPSecret = %q, %v", got, err)
	}
	if got, _ := s.UserByName(ctx, "admin"); got.TOTPEnabled() {
		t.Error("an enrollment waiting for its first code turned 2FA on")
	}
	// Starting again replaces the secret.
	again := []byte("the second secret!!!")
	if err := s.SetPendingTOTP(ctx, u.ID, again); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TOTPSecret(ctx, u.ID); !bytes.Equal(got, again) {
		t.Errorf("TOTPSecret = %q after enrolling again", got)
	}

	session(t, s, u, "mine")
	session(t, s, u, "other")
	if err := s.EnableTOTP(ctx, u.ID, 41, []string{"h1", "h2", "h3"}, "mine"); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserByName(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !got.TOTPEnabled() || got.TOTPLastStep != 41 || got.RecoveryCodesLeft != 3 {
		t.Errorf("after EnableTOTP: %+v", got)
	}
	// Turning it on ends the other sessions, as a password change does.
	if ids := sessionIDs(t, s, u); len(ids) != 1 || ids[0] != "mine" {
		t.Errorf("sessions = %v, want only the one that turned it on", ids)
	}

	if err := s.EnableTOTP(ctx, u.ID, 42, nil, ""); !errors.Is(err, ErrTOTPEnabled) {
		t.Errorf("EnableTOTP twice: %v", err)
	}
	if err := s.SetPendingTOTP(ctx, u.ID, secret); !errors.Is(err, ErrTOTPEnabled) {
		t.Errorf("a new secret while 2FA is on: %v", err)
	}
	if got, _ := s.TOTPSecret(ctx, u.ID); !bytes.Equal(got, again) {
		t.Error("the secret changed under an account with 2FA on")
	}
	if err := s.SetPendingTOTP(ctx, "nobody", secret); !errors.Is(err, ErrNoUser) {
		t.Errorf("a secret for no one: %v", err)
	}
}

func TestTOTPSecretIsSealed(t *testing.T) {
	ctx := context.Background()
	s, path, u := totpUser(t)
	secret := []byte("0123456789abcdefghij")
	if err := s.SetPendingTOTP(ctx, u.ID, secret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, secret) {
		t.Error("the secret is in the database file")
	}

	// Sealed for the account: moved to another row, it can't be opened.
	var sealed []byte
	if err := s.db.QueryRowContext(ctx, `SELECT totp_secret_enc FROM users`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.sealer.Open(sealed, totpPurpose("another-account")); err == nil {
		t.Error("the secret opened for another account")
	}

	// And a different secret key can't read it.
	_ = s.Close()
	other, err := Open(ctx, path, testSealer(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.TOTPSecret(ctx, u.ID); err == nil {
		t.Error("the secret opened with another key")
	}
}

func TestDisableTOTP(t *testing.T) {
	ctx := context.Background()
	s, _, u := totpUser(t)
	session(t, s, u, "mine")
	session(t, s, u, "other")

	// With 2FA off, turning it off is a no-op that ends nothing.
	if on, err := s.DisableTOTP(ctx, u.ID, "mine"); err != nil || on {
		t.Fatalf("DisableTOTP while off: %v, %v", on, err)
	}
	// So is dropping an enrollment that never got its first code.
	if err := s.SetPendingTOTP(ctx, u.ID, []byte("0123456789abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if on, err := s.DisableTOTP(ctx, u.ID, "mine"); err != nil || on {
		t.Fatalf("DisableTOTP of an enrollment: %v, %v", on, err)
	}
	if _, err := s.TOTPSecret(ctx, u.ID); !errors.Is(err, ErrNoEnrollment) {
		t.Errorf("the pending secret stayed: %v", err)
	}
	if ids := sessionIDs(t, s, u); len(ids) != 2 {
		t.Fatalf("sessions = %v, want both: nothing was on", ids)
	}

	if err := s.SetPendingTOTP(ctx, u.ID, []byte("0123456789abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, u.ID, 9, []string{"h1", "h2"}, "mine"); err != nil {
		t.Fatal(err)
	}
	session(t, s, u, "later")
	if on, err := s.DisableTOTP(ctx, u.ID, "mine"); err != nil || !on {
		t.Fatalf("DisableTOTP: %v, %v", on, err)
	}
	got, _ := s.UserByName(ctx, "admin")
	if got.TOTPEnabled() || got.TOTPLastStep != 0 || got.RecoveryCodesLeft != 0 {
		t.Errorf("after DisableTOTP: %+v", got)
	}
	if _, err := s.TOTPSecret(ctx, u.ID); !errors.Is(err, ErrNoEnrollment) {
		t.Errorf("the secret stayed: %v", err)
	}
	if ids := sessionIDs(t, s, u); len(ids) != 1 || ids[0] != "mine" {
		t.Errorf("sessions = %v, want only the one that turned it off", ids)
	}

	// From the command line there is no session to keep.
	if err := s.SetPendingTOTP(ctx, u.ID, []byte("0123456789abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, u.ID, 9, []string{"h1"}, "mine"); err != nil {
		t.Fatal(err)
	}
	if on, err := s.DisableTOTP(ctx, u.ID, ""); err != nil || !on {
		t.Fatalf("DisableTOTP: %v, %v", on, err)
	}
	if ids := sessionIDs(t, s, u); len(ids) != 0 {
		t.Errorf("sessions = %v, want none", ids)
	}
	if _, err := s.DisableTOTP(ctx, "nobody", ""); !errors.Is(err, ErrNoUser) {
		t.Errorf("DisableTOTP of no one: %v", err)
	}
}

func TestUseTOTPStepOnlyAdvances(t *testing.T) {
	ctx := context.Background()
	s, _, u := totpUser(t)
	use := func(step int64) bool {
		t.Helper()
		ok, err := s.UseTOTPStep(ctx, u.ID, step)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	// While 2FA is off, or only being set up, there is nothing to use a code for.
	if use(5) {
		t.Error("a step was used on an account with 2FA off")
	}
	if err := s.SetPendingTOTP(ctx, u.ID, []byte("0123456789abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if use(5) {
		t.Error("a step was used on an enrollment")
	}
	if err := s.EnableTOTP(ctx, u.ID, 10, []string{"h"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		step int64
		want bool
	}{{10, false}, {9, false}, {11, true}, {11, false}, {10, false}, {13, true}, {12, false}} {
		if got := use(c.step); got != c.want {
			t.Errorf("step %d: %v, want %v", c.step, got, c.want)
		}
	}
	if got, _ := s.UserByName(ctx, "admin"); got.TOTPLastStep != 13 {
		t.Errorf("last step = %d", got.TOTPLastStep)
	}
}

// Two logins that present the same code at the same moment can't both pass.
func TestUseTOTPStepIsAtomic(t *testing.T) {
	ctx := context.Background()
	s, _, u := totpUser(t)
	if err := s.SetPendingTOTP(ctx, u.ID, []byte("0123456789abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, u.ID, 1, []string{"h"}, ""); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if ok, err := s.UseTOTPStep(ctx, u.ID, 2); err == nil && ok {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("%d logins used the same step", wins.Load())
	}
}

func TestUseRecoveryCode(t *testing.T) {
	ctx := context.Background()
	s, _, u := totpUser(t)
	if err := s.SetPendingTOTP(ctx, u.ID, []byte("0123456789abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, u.ID, 1, []string{"h1", "h2", "h3"}, ""); err != nil {
		t.Fatal(err)
	}

	if ok, left, err := s.UseRecoveryCode(ctx, u.ID, "nope"); err != nil || ok || left != 3 {
		t.Errorf("a code that isn't one: %v, %d, %v", ok, left, err)
	}
	if ok, left, err := s.UseRecoveryCode(ctx, u.ID, "h2"); err != nil || !ok || left != 2 {
		t.Errorf("first use: %v, %d, %v", ok, left, err)
	}
	if ok, left, err := s.UseRecoveryCode(ctx, u.ID, "h2"); err != nil || ok || left != 2 {
		t.Errorf("second use: %v, %d, %v", ok, left, err)
	}
	if got, _ := s.UserByName(ctx, "admin"); got.RecoveryCodesLeft != 2 {
		t.Errorf("RecoveryCodesLeft = %d", got.RecoveryCodesLeft)
	}
	// The others still work, and the last one leaves none.
	for _, h := range []string{"h1", "h3"} {
		if ok, _, err := s.UseRecoveryCode(ctx, u.ID, h); err != nil || !ok {
			t.Errorf("%s: %v, %v", h, ok, err)
		}
	}
	if got, _ := s.UserByName(ctx, "admin"); got.RecoveryCodesLeft != 0 || !got.TOTPEnabled() {
		t.Errorf("after the last code: %+v", got)
	}

	var wg sync.WaitGroup
	var wins atomic.Int32
	if err := s.ReplaceRecoveryCodes(ctx, u.ID, []string{"n1"}); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		wg.Go(func() {
			if ok, _, err := s.UseRecoveryCode(ctx, u.ID, "n1"); err == nil && ok {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("a recovery code was used %d times at once", wins.Load())
	}
}

func TestReplaceRecoveryCodes(t *testing.T) {
	ctx := context.Background()
	s, _, u := totpUser(t)
	if err := s.ReplaceRecoveryCodes(ctx, u.ID, []string{"x"}); !errors.Is(err, ErrTOTPOff) {
		t.Errorf("with 2FA off: %v", err)
	}
	if err := s.SetPendingTOTP(ctx, u.ID, []byte("0123456789abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, u.ID, 1, []string{"old1", "old2"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRecoveryCodes(ctx, u.ID, []string{"new1", "new2", "new3"}); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.UseRecoveryCode(ctx, u.ID, "old1"); ok {
		t.Error("an old code still works")
	}
	if ok, left, _ := s.UseRecoveryCode(ctx, u.ID, "new3"); !ok || left != 2 {
		t.Errorf("a new code: %v, %d", ok, left)
	}
}

// A database from before 2FA keeps its account when a newer build opens it, with 2FA off.
func TestMigrationAddsTOTPToAnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	u := tokenUser(t, s)
	rollBackTo(t, s, 10)
	_ = s.Close()

	again, err := Open(ctx, path, testSealer(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got, err := again.UserByName(ctx, "admin")
	if err != nil || got.ID != u.ID || got.TOTPEnabled() || got.RecoveryCodesLeft != 0 || got.TOTPLastStep != 0 {
		t.Fatalf("the account after the upgrade: %+v, %v", got, err)
	}
	if err := again.SetPendingTOTP(ctx, u.ID, []byte("0123456789abcdefghij")); err != nil {
		t.Errorf("can't enroll after the upgrade: %v", err)
	}
}
