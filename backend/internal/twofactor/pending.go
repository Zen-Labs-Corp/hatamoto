package twofactor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/secretbox"
)

// PendingTTL is how long the password step stays valid for the second step.
const PendingTTL = 5 * time.Minute

// ErrBadPending is returned for a missing, forged, malformed or expired token.
var ErrBadPending = errors.New("twofactor: pending sign-in is not valid")

// Pending is the state between the password and the second factor. It grants
// nothing but the right to try a second factor for UserID.
type Pending struct {
	UserID  uuid.UUID `json:"u"`
	Next    string    `json:"n,omitempty"`
	Expires time.Time `json:"e"`
	ID      string    `json:"i"`
}

// PendingKey signs pending tokens.
type PendingKey []byte

// PendingKeyFor derives the signing key from the server secret.
func PendingKeyFor(serverSecret string) PendingKey {
	return PendingKey(secretbox.DeriveKey(serverSecret, "two-factor-pending"))
}

// NewPending starts a pending sign-in for userID that lasts PendingTTL.
func NewPending(userID uuid.UUID, next string, now time.Time) (Pending, error) {
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return Pending{}, err
	}
	return Pending{UserID: userID, Next: next, Expires: now.Add(PendingTTL), ID: base64.RawURLEncoding.EncodeToString(id)}, nil
}

func (k PendingKey) sig(payload string) string {
	m := hmac.New(sha256.New, k)
	m.Write([]byte("pending|" + payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Seal returns base64url(payload) "." base64url(HMAC-SHA256).
func (k PendingKey) Seal(p Pending) string {
	b, _ := json.Marshal(p)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + k.sig(payload)
}

// Open verifies the signature in constant time, then the expiry.
func (k PendingKey) Open(s string, now time.Time) (Pending, error) {
	var p Pending
	if len(k) == 0 {
		return p, ErrBadPending
	}
	payload, sig, ok := strings.Cut(s, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(k.sig(payload))) {
		return p, ErrBadPending
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &p) != nil {
		return Pending{}, ErrBadPending
	}
	if p.UserID == uuid.Nil || !now.Before(p.Expires) {
		return Pending{}, ErrBadPending
	}
	return p, nil
}
