package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/auth"
	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/store"
)

// totpEnv is a service with an admin who is logged in and has turned 2FA on.
type totpEnv struct {
	s        *Service
	clk      *clock
	ctx      context.Context
	login    Login
	password string
	secret   []byte
	recovery []string
	logs     *bytes.Buffer
}

func newTOTPEnv(t *testing.T) *totpEnv {
	t.Helper()
	s, clk, ctx, login, password := tokenEnv(t)
	e := &totpEnv{s: s, clk: clk, ctx: ctx, login: login, password: password, logs: &bytes.Buffer{}}
	s.Log = slog.New(slog.NewTextHandler(e.logs, nil))
	e.enable(t)
	return e
}

// enable turns 2FA on the way the web UI does, and moves on to the next step, so that the code
// that proved the app is behind the clock and the next code is a new one.
func (e *totpEnv) enable(t *testing.T) {
	t.Helper()
	if _, err := e.s.EnrollTOTP(e.ctx, e.user(t), e.password); err != nil {
		t.Fatal(err)
	}
	var err error
	if e.secret, err = e.s.Store.TOTPSecret(e.ctx, e.user(t).ID); err != nil {
		t.Fatal(err)
	}
	if e.recovery, err = e.s.EnableTOTP(e.ctx, e.user(t), e.login.Session, e.code()); err != nil {
		t.Fatal(err)
	}
	e.clk.advance(auth.TOTPPeriod)
}

func (e *totpEnv) user(t *testing.T) store.User {
	t.Helper()
	u, err := e.s.Store.UserByName(e.ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// code is the right code at the clock's time, or `steps` steps from it.
func (e *totpEnv) code(steps ...int64) string {
	step := auth.TOTPStep(e.clk.now())
	for _, n := range steps {
		step += n
	}
	return auth.TOTPCode(e.secret, step)
}

// wrongCode is six digits that aren't right at any time the window allows.
func (e *totpEnv) wrongCode() string {
	valid := []string{e.code(-1), e.code(), e.code(1)}
	for n := 0; ; n++ {
		c := strings.Repeat(string(rune('0'+n%10)), 6)
		if !slices.Contains(valid, c) {
			return c
		}
	}
}

func TestEnrollAndEnableTOTP(t *testing.T) {
	s, clk, ctx, login, password := tokenEnv(t)
	var logs bytes.Buffer
	s.Log = slog.New(slog.NewTextHandler(&logs, nil))
	other, err := s.Login(ctx, "admin", password, "phone")
	if err != nil {
		t.Fatal(err)
	}
	user := func() store.User { u, _ := s.Store.UserByName(ctx, "admin"); return u }

	if _, err := s.EnableTOTP(ctx, user(), login.Session, "123456"); !errors.Is(err, store.ErrNoEnrollment) {
		t.Fatalf("EnableTOTP before EnrollTOTP: %v", err)
	}
	en, err := s.EnrollTOTP(ctx, user(), password)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.Store.TOTPSecret(ctx, user().ID)
	if err != nil {
		t.Fatal(err)
	}
	// What the admin's app gets: the secret to type and the address a QR code holds.
	if en.Secret != auth.EncodeTOTPSecret(secret) || !strings.HasPrefix(en.URI, "otpauth://totp/Drawbridge:admin?") ||
		!strings.Contains(en.URI, "secret="+en.Secret) || !strings.Contains(en.URI, "issuer=Drawbridge") {
		t.Fatalf("enrollment = %+v", en)
	}
	// Waiting for the first code changes nothing: a login doesn't ask for one yet.
	if user().TOTPEnabled() {
		t.Fatal("2FA is on before the first code")
	}
	if _, err := s.Login(ctx, "admin", password, "laptop"); err != nil {
		t.Fatalf("a login while an enrollment waits: %v", err)
	}

	// A wrong code doesn't turn it on, and is a failure the log shows.
	wrong := auth.TOTPCode(secret, auth.TOTPStep(clk.now())+5)
	if _, err := s.EnableTOTP(ctx, user(), login.Session, wrong); !errors.Is(err, ErrBadCode) || !model.IsInvalid(err) {
		t.Fatalf("a wrong code: %v; want ErrBadCode as invalid input (a 401 would look like a lapsed session)", err)
	}
	if user().TOTPEnabled() {
		t.Fatal("a wrong code turned 2FA on")
	}

	codes, err := s.EnableTOTP(ctx, user(), login.Session, auth.TOTPCode(secret, auth.TOTPStep(clk.now())))
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != auth.RecoveryCodeCount || !user().TOTPEnabled() || user().RecoveryCodesLeft != len(codes) {
		t.Fatalf("codes %v; user %+v", codes, user())
	}
	for _, c := range codes {
		if !auth.LooksLikeRecoveryCode(c) {
			t.Errorf("recovery code %q", c)
		}
	}
	// The browser that turned it on stays logged in; every other one is out.
	if _, _, err := s.Authenticate(ctx, login.Token); err != nil {
		t.Errorf("the session that turned 2FA on ended: %v", err)
	}
	if _, _, err := s.Authenticate(ctx, other.Token); !errors.Is(err, store.ErrNoSession) {
		t.Errorf("another session after 2FA was turned on: %v", err)
	}

	// It's in the log, and neither the secret nor a code is.
	want := []string{"auth.totp_failed", "auth.totp_enabled"}
	got := kinds(t, s)
	if len(got) < 2 || !slices.Equal(got[len(got)-2:], want) {
		t.Errorf("events end %v, want %v", got[max(0, len(got)-2):], want)
	}
	all, _ := s.Events(ctx, store.EventFilter{})
	raw, _ := json.Marshal(all)
	for _, secretText := range append([]string{en.Secret, en.URI, string(secret)}, codes...) {
		if strings.Contains(string(raw), secretText) || strings.Contains(logs.String(), secretText) {
			t.Errorf("%q is in an event or the log", secretText)
		}
	}

	// It can't be turned on again over itself.
	if _, err := s.EnrollTOTP(ctx, user(), password); !errors.Is(err, store.ErrTOTPEnabled) {
		t.Errorf("EnrollTOTP while on: %v", err)
	}
	if _, err := s.EnableTOTP(ctx, user(), login.Session, wrong); !errors.Is(err, store.ErrTOTPEnabled) {
		t.Errorf("EnableTOTP while on: %v", err)
	}
}

// Whoever turns 2FA on picks the second factor, so a session alone isn't enough.
func TestEnrollTOTPNeedsThePassword(t *testing.T) {
	s, _, ctx, login, password := tokenEnv(t)
	if _, err := s.EnrollTOTP(ctx, login.User, "not the password"); !errors.Is(err, ErrWrongPassword) || !model.IsInvalid(err) {
		t.Fatalf("a wrong password: %v", err)
	}
	if _, err := s.Store.TOTPSecret(ctx, login.User.ID); !errors.Is(err, store.ErrNoEnrollment) {
		t.Errorf("a secret was made without the password: %v", err)
	}
	if got := kinds(t, s); got[len(got)-1] != "auth.totp_failed" {
		t.Errorf("the failed attempt isn't in the log: %v", got)
	}
	// The failures count against the login limits: five are free (one is spent), and after the
	// sixth the right password has to wait too.
	for range 5 {
		_, _ = s.EnrollTOTP(ctx, login.User, "wrong")
	}
	var limited *RateLimitedError
	if _, err := s.EnrollTOTP(ctx, login.User, password); !errors.As(err, &limited) {
		t.Errorf("after 6 failures: %v, want a RateLimitedError even for the right password", err)
	}
}

func TestLoginAsksForTheCode(t *testing.T) {
	e := newTOTPEnv(t)
	login := func(code string) (Login, error) {
		return e.s.LoginWithCode(e.ctx, "admin", e.password, code, "laptop")
	}

	// The password alone is the first step: it isn't a failure, and it doesn't log in. Plenty of
	// them don't lock the account, as five wrong passwords would.
	for range 12 {
		if l, err := login(""); !errors.Is(err, ErrTOTPRequired) || l.Token != "" {
			t.Fatalf("no code: %+v, %v", l, err)
		}
	}
	if l, err := e.s.Login(e.ctx, "admin", e.password, "laptop"); !errors.Is(err, ErrTOTPRequired) || l.Token != "" {
		t.Fatalf("Login of an account with 2FA: %+v, %v", l, err)
	}
	if _, err := login("   "); !errors.Is(err, ErrTOTPRequired) {
		t.Errorf("a blank code: %v", err)
	}
	if got := kinds(t, e.s); slices.Contains(got[len(got)-3:], "auth.login_failed") {
		t.Errorf("asking for the code is in the log as a failure: %v", got)
	}

	// A wrong code is a failure, and says so. It isn't the "wrong password" message, because the
	// password was right.
	if _, err := login(e.wrongCode()); !errors.Is(err, ErrBadCode) || errors.Is(err, ErrBadLogin) {
		t.Fatalf("a wrong code: %v", err)
	}
	events, _ := e.s.Events(e.ctx, store.EventFilter{Kind: "auth.login_failed"})
	if len(events) != 1 || events[0].Data["reason"] != "wrong code" || events[0].Actor != "admin" {
		t.Errorf("failed logins = %+v", events)
	}
	// Two steps away is too far (the window is one step either side) even though the code is real.
	if _, err := login(e.code(2)); !errors.Is(err, ErrBadCode) {
		t.Errorf("a code from two steps ahead: %v", err)
	}
	if _, err := login(e.code(-2)); !errors.Is(err, ErrBadCode) {
		t.Errorf("a code from two steps back: %v", err)
	}

	// The right code, typed the way an app displays it.
	c := e.code()
	l, err := login(c[:3] + " " + c[3:])
	if err != nil || l.Token == "" {
		t.Fatalf("the right code: %+v, %v", l, err)
	}
	if _, _, err := e.s.Authenticate(e.ctx, l.Token); err != nil {
		t.Errorf("the new session: %v", err)
	}

	// The password still has to be right, and a code doesn't stand in for it.
	if _, err := e.s.LoginWithCode(e.ctx, "admin", "wrong password", e.code(1), "laptop"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("a wrong password with a right code: %v", err)
	}
	if _, err := e.s.LoginWithCode(e.ctx, "nobody", e.password, e.code(1), "laptop"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("an account that doesn't exist: %v", err)
	}
}

// A code is good once, however long it stays inside the window.
func TestLoginRefusesACodeUsedAlready(t *testing.T) {
	e := newTOTPEnv(t)
	login := func(code string) error {
		_, err := e.s.LoginWithCode(e.ctx, "admin", e.password, code, "laptop")
		return err
	}
	code := e.code()
	if err := login(code); err != nil {
		t.Fatal(err)
	}
	if err := login(code); !errors.Is(err, ErrBadCode) {
		t.Errorf("the same code twice: %v", err)
	}
	// The step before it is still inside the window, and still no good: a code can't be used
	// after a later one.
	if err := login(e.code(-1)); !errors.Is(err, ErrBadCode) {
		t.Errorf("an earlier step after a later one: %v", err)
	}
	// The next step's code is new. (A phone whose clock runs fast gets here a step early.)
	if err := login(e.code(1)); err != nil {
		t.Errorf("the next step: %v", err)
	}
	if err := login(e.code(1)); !errors.Is(err, ErrBadCode) {
		t.Errorf("the next step twice: %v", err)
	}
	// The code that proved the app when 2FA was turned on is spent too.
	e.clk.advance(-auth.TOTPPeriod)
	if err := login(e.code()); !errors.Is(err, ErrBadCode) {
		t.Errorf("the code that turned 2FA on: %v", err)
	}
}

// A wrong code is a failed login, counted against the source and the account like a wrong
// password. The right password mustn't clear the count between guesses, or the code could be
// guessed without limit by someone who has the password.
func TestWrongCodesAreLimitedWhateverThePassword(t *testing.T) {
	e := newTOTPEnv(t)
	for i := range 6 {
		if _, err := e.s.LoginWithCode(e.ctx, "admin", e.password, e.wrongCode(), "laptop"); !errors.Is(err, ErrBadCode) {
			t.Fatalf("guess %d: %v", i+1, err)
		}
	}
	_, err := e.s.LoginWithCode(e.ctx, "admin", e.password, e.code(), "laptop")
	var limited *RateLimitedError
	if !errors.As(err, &limited) || limited.Wait <= 0 {
		t.Fatalf("after 6 wrong codes: %v, want a RateLimitedError, even for the right password and code", err)
	}
	e.clk.advance(limited.Wait + time.Second)
	if _, err := e.s.LoginWithCode(e.ctx, "admin", e.password, e.code(), "laptop"); err != nil {
		t.Errorf("after the wait: %v", err)
	}
}

func TestLoginWithARecoveryCode(t *testing.T) {
	e := newTOTPEnv(t)
	login := func(code string) error {
		_, err := e.s.LoginWithCode(e.ctx, "admin", e.password, code, "laptop")
		return err
	}
	// However it is typed.
	if err := login(strings.ToLower(strings.ReplaceAll(e.recovery[0], "-", ""))); err != nil {
		t.Fatal(err)
	}
	if got := e.user(t).RecoveryCodesLeft; got != auth.RecoveryCodeCount-1 {
		t.Errorf("%d codes left, want %d", got, auth.RecoveryCodeCount-1)
	}
	if err := login(e.recovery[0]); !errors.Is(err, ErrBadCode) {
		t.Errorf("a recovery code twice: %v", err)
	}
	if err := login(e.recovery[1] + "X"); !errors.Is(err, ErrBadCode) {
		t.Errorf("a recovery code with a character added: %v", err)
	}
	if err := login("ABCDE-FGHJK-LMNPQ"); !errors.Is(err, ErrBadCode) {
		t.Errorf("a recovery code that was never made: %v", err)
	}

	events, _ := e.s.Events(e.ctx, store.EventFilter{Kind: "auth.recovery_code_used"})
	if len(events) != 1 || events[0].Data["left"] != "9" {
		t.Errorf("events = %+v, want one, with 9 left", events)
	}
	// The log says one was used, and never which.
	all, _ := e.s.Events(e.ctx, store.EventFilter{})
	raw, _ := json.Marshal(all)
	for _, c := range e.recovery {
		if strings.Contains(string(raw), c) || strings.Contains(e.logs.String(), c) {
			t.Errorf("recovery code %q is in an event or the log", c)
		}
	}
}

func TestDisableTOTP(t *testing.T) {
	e := newTOTPEnv(t)
	other, err := e.s.LoginWithCode(e.ctx, "admin", e.password, e.code(), "phone")
	if err != nil {
		t.Fatal(err)
	}
	e.clk.advance(auth.TOTPPeriod)

	// It takes both: a right code with a wrong password, and a right password with a wrong code, fail.
	if err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, "not the password", e.code()); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("a wrong password: %v", err)
	}
	if err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, e.password, e.wrongCode()); !errors.Is(err, ErrBadCode) ||
		!model.IsInvalid(err) {
		t.Errorf("a wrong code: %v", err)
	}
	if err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, e.password, ""); !errors.Is(err, ErrBadCode) {
		t.Errorf("no code: %v", err)
	}
	if !e.user(t).TOTPEnabled() {
		t.Fatal("2FA went off without the password and a code")
	}

	if err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, e.password, e.code()); err != nil {
		t.Fatal(err)
	}
	if u := e.user(t); u.TOTPEnabled() || u.RecoveryCodesLeft != 0 {
		t.Errorf("after turning it off: %+v", u)
	}
	if _, err := e.s.Store.TOTPSecret(e.ctx, e.user(t).ID); !errors.Is(err, store.ErrNoEnrollment) {
		t.Errorf("the secret stayed: %v", err)
	}
	if _, _, err := e.s.Authenticate(e.ctx, e.login.Token); err != nil {
		t.Errorf("the session that turned it off ended: %v", err)
	}
	if _, _, err := e.s.Authenticate(e.ctx, other.Token); !errors.Is(err, store.ErrNoSession) {
		t.Errorf("another session after 2FA was turned off: %v", err)
	}
	if _, err := e.s.Login(e.ctx, "admin", e.password, "laptop"); err != nil {
		t.Errorf("a login without a code, after: %v", err)
	}
	got := kinds(t, e.s)
	if got[len(got)-2] != "auth.totp_disabled" {
		t.Errorf("events end %v", got[len(got)-3:])
	}
	if err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, e.password, e.code()); !errors.Is(err, store.ErrTOTPOff) {
		t.Errorf("turning it off twice: %v", err)
	}
}

func TestDisableTOTPWithARecoveryCode(t *testing.T) {
	e := newTOTPEnv(t)
	if err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, e.password, e.recovery[3]); err != nil {
		t.Fatal(err)
	}
	if e.user(t).TOTPEnabled() {
		t.Error("2FA is still on")
	}
	events, _ := e.s.Events(e.ctx, store.EventFilter{Kind: "auth.recovery_code_used"})
	if len(events) != 1 || events[0].Data["left"] != "9" {
		t.Errorf("events = %+v", events)
	}
}

// Turning 2FA off has the same property as logging in: a session that has the password can't
// guess the code without limit.
func TestDisableTOTPLimitsWrongCodes(t *testing.T) {
	e := newTOTPEnv(t)
	for i := range 6 {
		if err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, e.password, e.wrongCode()); !errors.Is(err, ErrBadCode) {
			t.Fatalf("guess %d: %v", i+1, err)
		}
	}
	err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, e.password, e.code())
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("after 6 wrong codes: %v, want a RateLimitedError", err)
	}
	if !e.user(t).TOTPEnabled() {
		t.Error("2FA went off while limited")
	}
}

func TestNewRecoveryCodes(t *testing.T) {
	e := newTOTPEnv(t)
	if _, err := e.s.NewRecoveryCodes(e.ctx, e.user(t), "not the password", e.code()); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("a wrong password: %v", err)
	}
	if _, err := e.s.NewRecoveryCodes(e.ctx, e.user(t), e.password, e.wrongCode()); !errors.Is(err, ErrBadCode) {
		t.Errorf("a wrong code: %v", err)
	}
	if got := e.user(t).RecoveryCodesLeft; got != auth.RecoveryCodeCount {
		t.Fatalf("%d codes after refusals", got)
	}

	// Use two, then get a new set: it is a full set, and the old ones, used or not, are void.
	for _, c := range e.recovery[:2] {
		if _, err := e.s.LoginWithCode(e.ctx, "admin", e.password, c, "laptop"); err != nil {
			t.Fatal(err)
		}
	}
	codes, err := e.s.NewRecoveryCodes(e.ctx, e.user(t), e.password, e.code())
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != auth.RecoveryCodeCount || e.user(t).RecoveryCodesLeft != auth.RecoveryCodeCount {
		t.Fatalf("codes = %v; user %+v", codes, e.user(t))
	}
	if _, err := e.s.LoginWithCode(e.ctx, "admin", e.password, e.recovery[5], "laptop"); !errors.Is(err, ErrBadCode) {
		t.Errorf("an old recovery code after new ones: %v", err)
	}
	if _, err := e.s.LoginWithCode(e.ctx, "admin", e.password, codes[0], "laptop"); err != nil {
		t.Errorf("a new recovery code: %v", err)
	}
	if got := kinds(t, e.s); !slices.Contains(got, "auth.recovery_codes_renewed") {
		t.Errorf("events = %v", got)
	}

	if err := e.s.DisableTOTP(e.ctx, e.user(t), e.login.Session, e.password, codes[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.NewRecoveryCodes(e.ctx, e.user(t), e.password, e.code()); !errors.Is(err, store.ErrTOTPOff) {
		t.Errorf("new codes with 2FA off: %v", err)
	}
}

// The recovery for an admin who lost the app and the codes: from the command line.
func TestDisableTOTPFor(t *testing.T) {
	e := newTOTPEnv(t)
	cli := WithActor(context.Background(), Actor{Name: "root", Via: ViaCLI})
	userID := e.user(t).ID

	for range 6 {
		_, _ = e.s.LoginWithCode(e.ctx, "admin", e.password, e.wrongCode(), "laptop")
	}
	if e.s.Limiter.Wait(auth.AccountKey(userID)) <= 0 {
		t.Fatal("the account isn't locked out")
	}

	if _, _, err := e.s.DisableTOTPFor(cli, "nobody"); !errors.Is(err, store.ErrNoUser) {
		t.Errorf("an account that doesn't exist: %v", err)
	}
	u, wasOn, err := e.s.DisableTOTPFor(cli, "")
	if err != nil || !wasOn || u.Username != "admin" {
		t.Fatalf("DisableTOTPFor = %+v, %v, %v", u, wasOn, err)
	}
	if e.user(t).TOTPEnabled() {
		t.Error("2FA is still on")
	}
	if e.s.Limiter.Wait(auth.AccountKey(userID)) != 0 {
		t.Error("the lockout stayed")
	}
	if _, _, err := e.s.Authenticate(e.ctx, e.login.Token); !errors.Is(err, store.ErrNoSession) {
		t.Errorf("a session survived: %v", err)
	}
	events, _ := e.s.Events(e.ctx, store.EventFilter{Kind: "auth.totp_disabled"})
	if len(events) != 1 || events[0].Via != ViaCLI || events[0].Actor != "root" || events[0].Data["username"] != "admin" {
		t.Errorf("events = %+v", events)
	}

	// Again is harmless, and says nothing happened.
	if _, wasOn, err := e.s.DisableTOTPFor(cli, "admin"); err != nil || wasOn {
		t.Errorf("a second time: %v, %v", wasOn, err)
	}
	if events, _ := e.s.Events(e.ctx, store.EventFilter{Kind: "auth.totp_disabled"}); len(events) != 1 {
		t.Errorf("%d events after a second time", len(events))
	}
}

// Forgetting the password and losing the app are separate problems with separate recoveries.
func TestResetPasswordKeepsTOTP(t *testing.T) {
	e := newTOTPEnv(t)
	_, password, err := e.s.ResetPassword(WithActor(context.Background(), Actor{Name: "root", Via: ViaCLI}), "")
	if err != nil {
		t.Fatal(err)
	}
	if !e.user(t).TOTPEnabled() {
		t.Fatal("a password reset turned 2FA off")
	}
	if _, err := e.s.Login(e.ctx, "admin", password, "laptop"); !errors.Is(err, ErrTOTPRequired) {
		t.Errorf("the new password alone: %v", err)
	}
	if _, err := e.s.LoginWithCode(e.ctx, "admin", password, e.code(), "laptop"); err != nil {
		t.Errorf("the new password and a code: %v", err)
	}
}

// An account with 2FA off ignores a code sent with the password, and a pending enrollment doesn't
// change that.
func TestLoginWithoutTOTPIgnoresACode(t *testing.T) {
	s, _, ctx, login, password := tokenEnv(t)
	if _, err := s.LoginWithCode(ctx, "admin", password, "123456", "laptop"); err != nil {
		t.Errorf("a code from an account without 2FA: %v", err)
	}
	if _, err := s.EnrollTOTP(ctx, login.User, password); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginWithCode(ctx, "admin", password, "", "laptop"); err != nil {
		t.Errorf("a login while an enrollment waits: %v", err)
	}
}
