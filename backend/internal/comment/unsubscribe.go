package comment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/secretbox"
)

// UnsubscribeKind says what an unsubscribe link turns off.
type UnsubscribeKind byte

const (
	UnsubscribeReplies   UnsubscribeKind = 'r' // id = commenter id
	UnsubscribeModerator UnsubscribeKind = 'm' // id = user id
)

const unsubscribeMACLen = 16

// UnsubscribeKey derives the link key from the server secret.
func UnsubscribeKey(serverSecret string) []byte {
	return secretbox.DeriveKey(serverSecret, "hatamoto/comment/unsubscribe")
}

func unsubscribeMAC(key []byte, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return m.Sum(nil)[:unsubscribeMACLen]
}

// UnsubscribeToken is base64url(kind || id(16) || HMAC-SHA256(key, kind||id)[:16]).
// It never expires: it can only turn mail off (or a moderator's back on).
func UnsubscribeToken(key []byte, kind UnsubscribeKind, id uuid.UUID) string {
	payload := append([]byte{byte(kind)}, id[:]...)
	return base64.RawURLEncoding.EncodeToString(append(payload, unsubscribeMAC(key, payload)...))
}

// VerifyUnsubscribe returns the kind and id of a valid token. Any length, kind,
// encoding or MAC error gives ok=false.
func VerifyUnsubscribe(key []byte, token string) (UnsubscribeKind, uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 1+16+unsubscribeMACLen {
		return 0, uuid.Nil, false
	}
	payload, got := raw[:17], raw[17:]
	if !hmac.Equal(got, unsubscribeMAC(key, payload)) {
		return 0, uuid.Nil, false
	}
	kind := UnsubscribeKind(payload[0])
	if kind != UnsubscribeReplies && kind != UnsubscribeModerator {
		return 0, uuid.Nil, false
	}
	id, err := uuid.FromBytes(payload[1:])
	if err != nil {
		return 0, uuid.Nil, false
	}
	return kind, id, true
}
