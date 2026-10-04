package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238's default, and the only algorithm authenticator apps all support.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Time-based one-time passwords (RFC 6238, over RFC 4226), for the second step of a login
// (docs/PLAN.md §6.5). They are the parameters every authenticator app supports: HMAC-SHA1, six
// digits, and a 30-second step.
const (
	// TOTPSecretSize is the size of a secret in bytes: the 160 bits RFC 4226 recommends.
	TOTPSecretSize = 20
	// TOTPDigits is how many digits a code has.
	TOTPDigits = 6
	// TOTPPeriod is how long one code lasts.
	TOTPPeriod = 30 * time.Second
	// TOTPSkew is how many steps either side of the current one a code may come from. The host
	// may have no battery-backed clock, and a phone's can drift too (docs/PLAN.md §15), so a code
	// is accepted a step early or late.
	TOTPSkew = 1

	// RecoveryCodeCount is how many recovery codes an account gets.
	RecoveryCodeCount = 10
	// recoveryCodeLength is a code's characters: 5 bits each, so 75 bits. It is random, so the
	// SHA-256 hash that is stored can't be guessed backward.
	recoveryCodeLength = 15
)

// NewTOTPSecret returns a new random secret.
func NewTOTPSecret() ([]byte, error) {
	b := make([]byte, TOTPSecretSize)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// EncodeTOTPSecret is a secret in the form authenticator apps take for typing in: base32.
func EncodeTOTPSecret(secret []byte) string {
	return base32NoPadding.EncodeToString(secret)
}

// TOTPURI is the otpauth:// address an authenticator app reads from a QR code. issuer and account
// are what the app shows beside the code.
func TOTPURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", EncodeTOTPSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(TOTPDigits))
	q.Set("period", fmt.Sprint(int(TOTPPeriod/time.Second)))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPStep is the time step a moment falls in: the counter RFC 6238 feeds to HOTP.
func TOTPStep(t time.Time) int64 {
	return t.Unix() / int64(TOTPPeriod/time.Second)
}

// TOTPCode is the code for a time step.
func TOTPCode(secret []byte, step int64) string {
	mac := hmac.New(sha1.New, secret)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step)) //nolint:gosec // G115: a step is a positive time.
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	// Dynamic truncation (RFC 4226 §5.3).
	offset := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for range TOTPDigits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", TOTPDigits, bin%mod)
}

// NormalizeTOTPCode undoes what people do to a code when they type it from an app that shows it
// as "123 456". It reports whether what's left is a code at all: six digits.
func NormalizeTOTPCode(s string) (string, bool) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	if len(s) != TOTPDigits {
		return "", false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return s, true
}

// VerifyTOTP checks a code against the steps around now, and returns the step it matched. A step
// that isn't after `after` is refused, so a code can't be used twice, however long it stays valid:
// the caller keeps the last step it accepted (RFC 6238 §5.2). Every step is compared, so how long
// it takes doesn't say which one matched.
func VerifyTOTP(secret []byte, code string, now time.Time, after int64) (step int64, ok bool) {
	code, valid := NormalizeTOTPCode(code)
	if !valid {
		return 0, false
	}
	current := TOTPStep(now)
	for s := current - TOTPSkew; s <= current+TOTPSkew; s++ {
		if subtle.ConstantTimeCompare([]byte(TOTPCode(secret, s)), []byte(code)) == 1 && s > after && s > step {
			step, ok = s, true
		}
	}
	return step, ok
}

// NewRecoveryCodes returns RecoveryCodeCount codes, formatted for people (ABCDE-FGHJK-LMNPQ), and
// the hashes to store for them.
func NewRecoveryCodes() (codes, hashes []string, err error) {
	for range RecoveryCodeCount {
		raw, err := randomCode(recoveryCodeLength)
		if err != nil {
			return nil, nil, err
		}
		codes = append(codes, group(raw, "-"))
		hashes = append(hashes, HashRecoveryCode(raw))
	}
	return codes, hashes, nil
}

// HashRecoveryCode is what's stored for a recovery code, in any of the forms people copy it in.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(NormalizeSetupToken(code)))
	return hex.EncodeToString(sum[:])
}

// LooksLikeRecoveryCode reports whether s has the shape of a recovery code, so a login can tell
// one from a mistyped authenticator code.
func LooksLikeRecoveryCode(s string) bool {
	s = NormalizeSetupToken(s)
	if len(s) != recoveryCodeLength {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(alphabet, r) {
			return false
		}
	}
	return true
}
