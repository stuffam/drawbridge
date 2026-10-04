package auth

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The secret both RFCs use for their test vectors.
var rfcSecret = []byte("12345678901234567890")

// RFC 4226 Appendix D: the codes for counters 0 to 9. They are the check that the truncation is
// right, and that nothing here is merely consistent with itself.
func TestTOTPCodeMatchesRFC4226(t *testing.T) {
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for counter, code := range want {
		if got := TOTPCode(rfcSecret, int64(counter)); got != code {
			t.Errorf("counter %d: %s, want %s", counter, got, code)
		}
	}
}

// RFC 6238 Appendix B (SHA-1), which gives eight digits; six are their last six.
func TestTOTPCodeMatchesRFC6238(t *testing.T) {
	for _, c := range []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		if got := TOTPCode(rfcSecret, TOTPStep(time.Unix(c.unix, 0))); got != c.want {
			t.Errorf("at %d: %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestVerifyTOTPWindow(t *testing.T) {
	now := time.Unix(1111111111, 0)
	step := TOTPStep(now)
	for _, c := range []struct {
		name string
		step int64
		ok   bool
	}{
		{"two steps early", step - 2, false},
		{"a step early", step - 1, true},
		{"this step", step, true},
		{"a step late", step + 1, true},
		{"two steps late", step + 2, false},
	} {
		got, ok := VerifyTOTP(rfcSecret, TOTPCode(rfcSecret, c.step), now, 0)
		if ok != c.ok || (ok && got != c.step) {
			t.Errorf("%s: step %d, ok %v; want ok %v at %d", c.name, got, ok, c.ok, c.step)
		}
	}
	if _, ok := VerifyTOTP(rfcSecret, TOTPCode([]byte("another secret, 20 b"), step), now, 0); ok {
		t.Error("a code from another secret was accepted")
	}
}

// A code is good once: the last step accepted is the floor, even for a code that is still inside
// the window.
func TestVerifyTOTPRefusesAStepAlreadyUsed(t *testing.T) {
	now := time.Unix(1111111111, 0)
	step := TOTPStep(now)
	code := TOTPCode(rfcSecret, step)
	got, ok := VerifyTOTP(rfcSecret, code, now, 0)
	if !ok || got != step {
		t.Fatalf("the first use: %d, %v", got, ok)
	}
	if _, ok := VerifyTOTP(rfcSecret, code, now, got); ok {
		t.Error("the same code was accepted twice")
	}
	if _, ok := VerifyTOTP(rfcSecret, TOTPCode(rfcSecret, step-1), now, got); ok {
		t.Error("an earlier step was accepted after a later one")
	}
	if got, ok := VerifyTOTP(rfcSecret, TOTPCode(rfcSecret, step+1), now, got); !ok || got != step+1 {
		t.Errorf("the next step: %d, %v", got, ok)
	}
}

func TestNormalizeTOTPCode(t *testing.T) {
	for in, want := range map[string]string{"123456": "123456", "123 456": "123456", " 123456\n": "123456", "007 008": "007008"} {
		if got, ok := NormalizeTOTPCode(in); !ok || got != want {
			t.Errorf("%q: %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "12345", "1234567", "12345a", "١٢٣٤٥٦", "123-456", "ABCDE-FGHJK-LMNPQ"} {
		if got, ok := NormalizeTOTPCode(in); ok {
			t.Errorf("%q was taken for the code %q", in, got)
		}
	}
	// A code that isn't six digits never matches, whatever the secret makes of it.
	if _, ok := VerifyTOTP(rfcSecret, "28708", time.Unix(59, 0), 0); ok {
		t.Error("a short code was accepted")
	}
}

func TestTOTPURI(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri := TOTPURI("Drawbridge", "ad min", secret)
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Errorf("uri = %s", uri)
	}
	// The label is the issuer and the account, which an app shows; the space is escaped.
	if got, _ := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/")); got != "Drawbridge:ad min" {
		t.Errorf("label = %q in %s", got, uri)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"secret": "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "issuer": "Drawbridge", "algorithm": "SHA1", "digits": "6", "period": "30",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q (in %s)", k, q.Get(k), want, uri)
		}
	}
}

func TestNewTOTPSecret(t *testing.T) {
	a, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewTOTPSecret()
	if len(a) != TOTPSecretSize || string(a) == string(b) {
		t.Errorf("secrets %x and %x", a, b)
	}
	// Typed into an app by hand, it has no padding to get wrong: 20 bytes are exactly 32 characters.
	if got := EncodeTOTPSecret(a); len(got) != 32 || strings.Contains(got, "=") {
		t.Errorf("encoded as %q", got)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("%d codes and %d hashes", len(codes), len(hashes))
	}
	seen := map[string]bool{}
	for i, c := range codes {
		if len(c) != 17 || c[5] != '-' || c[11] != '-' || !LooksLikeRecoveryCode(c) {
			t.Errorf("code %q isn't ABCDE-FGHJK-LMNPQ", c)
		}
		if seen[c] {
			t.Errorf("code %q twice", c)
		}
		seen[c] = true
		// Found again by its hash, however it is typed.
		for _, typed := range []string{c, strings.ToLower(c), strings.ReplaceAll(c, "-", ""), " " + c + "\n"} {
			if HashRecoveryCode(typed) != hashes[i] {
				t.Errorf("%q doesn't hash like %q", typed, c)
			}
		}
	}
	if HashRecoveryCode(codes[0]) == HashRecoveryCode(codes[1]) {
		t.Error("two codes hash alike")
	}
	for _, in := range []string{"", "123456", "ABCDE-FGHJK", "ABCDE-FGHJK-LMNP0", "ABCDE-FGHJK-LMNPQR"} {
		if LooksLikeRecoveryCode(in) {
			t.Errorf("%q looks like a recovery code", in)
		}
	}
}
