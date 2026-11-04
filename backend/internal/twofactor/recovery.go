package twofactor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"strings"

	"hatamoto.dev/core/internal/secretbox"
)

const (
	// RecoveryCodeCount is how many codes a user gets at a time.
	RecoveryCodeCount = 10
	recoveryCodeChars = 12
)

// NewRecoveryCodes returns n codes of 12 base32 characters shown as xxxx-xxxx-xxxx.
func NewRecoveryCodes(n int) ([]string, error) {
	out := make([]string, 0, n)
	for range n {
		b := make([]byte, 8) // 64 bits of entropy, 13 characters; the first 12 are used
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		s := strings.ToLower(b32.EncodeToString(b))[:recoveryCodeChars]
		out = append(out, s[0:4]+"-"+s[4:8]+"-"+s[8:12])
	}
	return out, nil
}

// NormalizeCode strips spaces and dashes and upper-cases, so a code typed in
// any common form hashes the same.
func NormalizeCode(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, s)
	return strings.ToUpper(s)
}

// RecoveryKey derives the HMAC key for recovery codes from the server secret.
func RecoveryKey(serverSecret string) []byte {
	return secretbox.DeriveKey(serverSecret, "two-factor-recovery")
}

// HashRecoveryCode is the stored form of a recovery code: HMAC-SHA256 under
// the server-derived key. The code itself is never stored.
func HashRecoveryCode(key []byte, code string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(NormalizeCode(code)))
	return m.Sum(nil)
}
