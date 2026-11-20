package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"

	"hatamoto.dev/core/internal/comment"
)

// TokenTTL is how long a confirmation link works: as long as the draft lives.
const TokenTTL = comment.DraftTTL

const tokenBytes = 32

// tokenLen is the length of a base64url (unpadded) token of tokenBytes bytes.
var tokenLen = base64.RawURLEncoding.EncodedLen(tokenBytes)

// NewToken returns a random token and its SHA-256. Only the hash is stored.
func NewToken() (raw string, hash []byte, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}

// HashToken returns the SHA-256 of a well-formed token. ok is false for
// anything that NewToken could not have produced.
func HashToken(raw string) (hash []byte, ok bool) {
	if len(raw) != tokenLen {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != tokenBytes || base64.RawURLEncoding.EncodeToString(b) != raw {
		return nil, false
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:], true
}
