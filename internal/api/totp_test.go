package api

import (
	"encoding/base32"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/auth"
	"github.com/stuffam/drawbridge/internal/views"
)

// Two-factor authentication over HTTP: what the web app sees at each step.
func TestTwoFactorFlow(t *testing.T) {
	svc := newService(t)
	// The service's clock is frozen, starting at the real time. It can't start at a fixed date:
	// the login's cookie expires 12 hours after the service's now, and the browser's cookie jar
	// judges that by the real clock, so a date more than 12 hours back means the cookie is
	// dropped and every request after the login is a 401. This test did that from midnight UTC
	// on 2026-10-05, 12 hours after the date it used to start at.
	now := time.Now().UTC().Truncate(time.Second)
	svc.Now = func() time.Time { return now }
	srv := newServer(t, svc)
	b, password := loggedIn(t, svc, srv)

	var me views.Me
	b.expect(http.StatusOK, "GET", "/api/auth/me", nil).decode(t, &me)
	if me.User.TOTPEnabled || me.User.RecoveryCodesLeft != 0 {
		t.Fatalf("2FA is on for a new account: %+v", me.User)
	}

	// A wrong password is a 400, like every refused password: a 401 would look to the web app like
	// a session that lapsed, and send the admin to the login.
	r := b.expect(http.StatusBadRequest, "POST", "/api/auth/totp/enroll", views.TOTPEnrollRequest{Password: "not it"})
	if !strings.Contains(r.errorText(), "password is wrong") {
		t.Errorf("enroll with a wrong password: %s", r.body)
	}
	b.expect(http.StatusOK, "GET", "/api/auth/me", nil)

	var en views.TOTPEnrollment
	b.expect(http.StatusOK, "POST", "/api/auth/totp/enroll", views.TOTPEnrollRequest{Password: password}).decode(t, &en)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(en.Secret)
	if err != nil || len(secret) != auth.TOTPSecretSize || !strings.Contains(en.URI, "secret="+en.Secret) {
		t.Fatalf("enrollment = %+v (%v)", en, err)
	}
	code := func(steps int64) string { return auth.TOTPCode(secret, auth.TOTPStep(now)+steps) }

	// Waiting for the first code, nothing is asked at login.
	newBrowser(t, srv).expect(http.StatusOK, "POST", "/api/auth/login", views.LoginRequest{Username: "admin", Password: password})

	r = b.expect(http.StatusBadRequest, "POST", "/api/auth/totp/verify", views.TOTPVerifyRequest{Code: code(4)})
	if !strings.Contains(r.errorText(), "wrong") {
		t.Errorf("verify with a wrong code: %s", r.body)
	}
	b.expect(http.StatusOK, "GET", "/api/auth/me", nil)

	var codes views.RecoveryCodes
	b.expect(http.StatusOK, "POST", "/api/auth/totp/verify", views.TOTPVerifyRequest{Code: code(0)}).decode(t, &codes)
	if len(codes.Codes) != auth.RecoveryCodeCount {
		t.Fatalf("recovery codes = %v", codes.Codes)
	}
	b.expect(http.StatusOK, "GET", "/api/auth/me", nil).decode(t, &me)
	if !me.User.TOTPEnabled || me.User.RecoveryCodesLeft != auth.RecoveryCodeCount {
		t.Errorf("after verify: %+v", me.User)
	}
	b.expect(http.StatusConflict, "POST", "/api/auth/totp/enroll", views.TOTPEnrollRequest{Password: password})
	b.expect(http.StatusConflict, "POST", "/api/auth/totp/verify", views.TOTPVerifyRequest{Code: code(1)})

	// The second browser logs in in two steps.
	b2 := newBrowser(t, srv)
	login := views.LoginRequest{Username: "admin", Password: password}
	r = b2.expect(http.StatusUnauthorized, "POST", "/api/auth/login", login)
	var e views.Error
	r.decode(t, &e)
	if e.Code != "totp_required" || r.header.Get("Set-Cookie") != "" {
		t.Errorf("a login without a code: %s, cookie %q", r.body, r.header.Get("Set-Cookie"))
	}
	login.Code = "000000"
	r = b2.expect(http.StatusUnauthorized, "POST", "/api/auth/login", login)
	e = views.Error{}
	r.decode(t, &e)
	if e.Code != "" || !strings.Contains(e.Error, "wrong") || r.header.Get("Set-Cookie") != "" {
		t.Errorf("a login with a wrong code: %s, cookie %q", r.body, r.header.Get("Set-Cookie"))
	}
	// The code that proved the app is spent; the next step's is new.
	login.Code = code(0)
	b2.expect(http.StatusUnauthorized, "POST", "/api/auth/login", login)
	login.Code = code(1)
	b2.expect(http.StatusOK, "POST", "/api/auth/login", login).decode(t, &me)
	if !me.User.TOTPEnabled {
		t.Errorf("login response: %+v", me.User)
	}
	b2.expect(http.StatusOK, "GET", "/api/auth/me", nil)

	// A recovery code logs in too, once.
	b3 := newBrowser(t, srv)
	login.Code = codes.Codes[0]
	b3.expect(http.StatusOK, "POST", "/api/auth/login", login)
	b4 := newBrowser(t, srv)
	b4.expect(http.StatusUnauthorized, "POST", "/api/auth/login", login)

	// Nothing the account shows has a secret in it: not the account, not the log.
	for _, path := range []string{"/api/auth/me", "/api/auth/sessions", "/api/events"} {
		body := string(b.expect(http.StatusOK, "GET", path, nil).body)
		for _, secretText := range append([]string{en.Secret, en.URI}, codes.Codes...) {
			if strings.Contains(body, secretText) {
				t.Errorf("GET %s has %q", path, secretText)
			}
		}
	}

	// New recovery codes, and turning it off, take the password and a code. The password alone,
	// and a code alone, are both refused with a 400.
	now = now.Add(2 * auth.TOTPPeriod)
	renew := views.SecondFactorRequest{Password: password, Code: code(0)}
	b.expect(http.StatusBadRequest, "POST", "/api/auth/totp/recovery-codes", views.SecondFactorRequest{Password: password})
	b.expect(http.StatusBadRequest, "POST", "/api/auth/totp/recovery-codes", views.SecondFactorRequest{Password: "no", Code: code(0)})
	var fresh views.RecoveryCodes
	b.expect(http.StatusOK, "POST", "/api/auth/totp/recovery-codes", renew).decode(t, &fresh)
	if len(fresh.Codes) != auth.RecoveryCodeCount || fresh.Codes[0] == codes.Codes[0] {
		t.Errorf("new codes = %v", fresh.Codes)
	}
	login.Code = codes.Codes[1]
	newBrowser(t, srv).expect(http.StatusUnauthorized, "POST", "/api/auth/login", login)

	b.expect(http.StatusBadRequest, "POST", "/api/auth/totp/disable", views.SecondFactorRequest{Password: password, Code: "000000"})
	b.expect(http.StatusNoContent, "POST", "/api/auth/totp/disable", views.SecondFactorRequest{Password: password, Code: fresh.Codes[0]})
	b.expect(http.StatusConflict, "POST", "/api/auth/totp/disable", views.SecondFactorRequest{Password: password, Code: fresh.Codes[1]})
	b.expect(http.StatusConflict, "POST", "/api/auth/totp/recovery-codes", views.SecondFactorRequest{Password: password, Code: fresh.Codes[1]})
	b.expect(http.StatusOK, "GET", "/api/auth/me", nil).decode(t, &me)
	if me.User.TOTPEnabled {
		t.Error("2FA is on after turning it off")
	}
	// Turning it off ends the other sessions.
	b2.expect(http.StatusUnauthorized, "GET", "/api/auth/me", nil)
	newBrowser(t, srv).expect(http.StatusOK, "POST", "/api/auth/login", views.LoginRequest{Username: "admin", Password: password})
}

// Every route that changes the second factor needs a session, the CSRF header, and JSON.
func TestTwoFactorRoutesAreGuarded(t *testing.T) {
	svc := newService(t)
	srv := newServer(t, svc)
	b, password := loggedIn(t, svc, srv)
	anon := newBrowser(t, srv)
	body := views.SecondFactorRequest{Password: password, Code: "123456"}
	for _, path := range []string{
		"/api/auth/totp/enroll", "/api/auth/totp/verify", "/api/auth/totp/disable", "/api/auth/totp/recovery-codes",
	} {
		anon.expect(http.StatusUnauthorized, "POST", path, body)
		b.expect(http.StatusForbidden, "POST", path, body, csrfHeader, "")
		// A field the request doesn't take is refused, not ignored.
		b.expect(http.StatusBadRequest, "POST", path, map[string]string{"password": password, "code": "1", "admin": "true"})
	}
}
