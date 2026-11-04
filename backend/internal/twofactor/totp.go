// Package twofactor holds the second sign-in factors: an authenticator app
// (TOTP), one-time recovery codes and passkeys (WebAuthn). It has no HTTP.
package twofactor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 default algorithm, what authenticator apps implement
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	totpPeriod = 30 // seconds
	totpDigits = 6
	secretLen  = 20 // bytes, 160 bits as RFC 4226 recommends
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a fresh 20-byte TOTP secret.
func NewSecret() ([]byte, error) {
	b := make([]byte, secretLen)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// EncodeSecret is the base32 text form users type into an app (no padding).
func EncodeSecret(secret []byte) string { return b32.EncodeToString(secret) }

// Code returns the 6-digit code for a 30-second step (RFC 4226 truncation).
func Code(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := (uint32(sum[off]&0x7f) << 24) | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	n %= 1_000_000
	s := strconv.FormatUint(uint64(n), 10)
	return strings.Repeat("0", totpDigits-len(s)) + s
}

// StepAt is the 30-second step number containing t.
func StepAt(t time.Time) int64 { return t.Unix() / totpPeriod }

// Check validates code against the steps one before to one after now. A step
// is accepted only when it is greater than lastStep, so a code cannot be used
// twice. It compares every step in constant time and returns the matched step.
func Check(secret []byte, now time.Time, code string, lastStep int64) (step int64, ok bool) {
	code = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := StepAt(now)
	match := 0
	for s := cur - 1; s <= cur+1; s++ {
		eq := subtle.ConstantTimeCompare([]byte(Code(secret, s)), []byte(code))
		fresh := subtle.ConstantTimeEq(int32(boolInt(s > lastStep)), 1)
		// Later steps win, so a code valid in two windows burns the later one.
		if eq&fresh == 1 {
			match = 1
			step = s
		}
	}
	return step, match == 1
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ProvisioningURI is the otpauth:// URI an authenticator app scans.
func ProvisioningURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", EncodeSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", strconv.Itoa(totpDigits))
	q.Set("period", strconv.Itoa(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
