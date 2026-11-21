package auditlog

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"hatamoto.dev/core/internal/secretbox"
)

// EmailHashPrefix marks an actor that is a hashed, typed email (a failed sign-in).
const EmailHashPrefix = "email-hash:"

// NewEmailHasher returns a func that maps an email to "email-hash:<32 hex>", an
// HMAC under secret in its own domain. The same address (any case, surrounding
// space) gives the same value, so an owner can hash an address to search for it
// without the log ever holding the address typed at the sign-in form.
func NewEmailHasher(secret string) func(email string) string {
	key := secretbox.DeriveKey(secret, "hatamoto/audit/email-hash")
	return func(email string) string {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(strings.ToLower(strings.TrimSpace(email))))
		return EmailHashPrefix + hex.EncodeToString(m.Sum(nil))[:32]
	}
}
