package spam

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"hatamoto.dev/core/internal/secretbox"
)

const fieldNameDomain = "hatamoto/spam/comment-field-names"

// Names are the rotated form field names for one comment token. Extra is
// reserved for a later form field (the reply opt-in); the default form does
// not use it.
type Names struct{ Body, Name, Email, Honeypot, Extra string }

// FieldNameKey derives the field name key from the server secret.
func FieldNameKey(serverSecret string) []byte {
	return secretbox.DeriveKey(serverSecret, fieldNameDomain)
}

// FieldNames derives one "c_<10 hex>" name per role from the token. Names are
// pairwise distinct; on a collision the role initial is appended.
func FieldNames(key []byte, token string) Names {
	seen := map[string]bool{}
	derive := func(role string) string {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(token + "|" + role))
		n := "c_" + hex.EncodeToString(m.Sum(nil))[:10]
		if seen[n] {
			n += role[:1]
		}
		seen[n] = true
		return n
	}
	return Names{
		Body:     derive("body"),
		Name:     derive("name"),
		Email:    derive("email"),
		Honeypot: derive("hp"),
		Extra:    derive("extra"),
	}
}
