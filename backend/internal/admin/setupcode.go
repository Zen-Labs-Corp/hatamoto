package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"strings"

	"hatamoto.dev/core/internal/config"
)

// SetupCode gates the creation of the first owner: whoever reaches a fresh
// install first must still prove access to the server (the code is in its log,
// or in HATAMOTO_SETUP_CODE). Only a hash is kept in memory.
type SetupCode struct{ hash [32]byte }

// codeAlphabet leaves out look-alikes (0/O, 1/I/L).
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func normalizeCode(s string) string { return config.NormalizeSetupCode(s) }

// NewSetupCode generates a random 8-character code and returns it for the log
// line ("XXXX-XXXX"); the SetupCode keeps only its hash.
func NewSetupCode() (*SetupCode, string) {
	// Rejection sampling: x % 31 over a whole byte would favour the first
	// symbols (256 is not a multiple of 31).
	const limit = 256 - 256%len(codeAlphabet)
	var sb strings.Builder
	buf := make([]byte, 32)
	for n := 0; n < 8; {
		_, _ = rand.Read(buf)
		for _, x := range buf {
			if int(x) >= limit || n == 8 {
				continue
			}
			if n == 4 {
				sb.WriteByte('-')
			}
			sb.WriteByte(codeAlphabet[int(x)%len(codeAlphabet)])
			n++
		}
	}
	display := sb.String()
	return SetupCodeFrom(display), display
}

// SetupCodeFrom wraps an operator-chosen code (HATAMOTO_SETUP_CODE).
func SetupCodeFrom(code string) *SetupCode {
	return &SetupCode{hash: sha256.Sum256([]byte(normalizeCode(code)))}
}

// Verify compares in constant time, ignoring case, spaces and dashes.
func (c *SetupCode) Verify(in string) bool {
	if c == nil || normalizeCode(in) == "" {
		return false
	}
	h := sha256.Sum256([]byte(normalizeCode(in)))
	return subtle.ConstantTimeCompare(h[:], c.hash[:]) == 1
}
