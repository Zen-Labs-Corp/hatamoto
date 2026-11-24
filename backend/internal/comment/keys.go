package comment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/core/internal/secretbox"
)

// Keys are the per-purpose keys derived from the server secret, one per
// purpose. Rotating the server secret changes every hash and makes sealed
// emails unreadable.
//
// The hashes are pseudonymous personal data, not anonymous: anyone holding the
// server secret can link them to a person, and an IPv4 address can be
// brute-forced with it.
type Keys struct{ seal, emailHash, ipHash, netHash []byte }

// NewKeys derives the comment keys from the server secret.
func NewKeys(serverSecret string) Keys {
	return Keys{
		seal:      secretbox.DeriveKey(serverSecret, "hatamoto/comment/email-seal"),
		emailHash: secretbox.DeriveKey(serverSecret, "hatamoto/comment/email-hash"),
		ipHash:    secretbox.DeriveKey(serverSecret, "hatamoto/comment/ip-hash"),
		netHash:   secretbox.DeriveKey(serverSecret, "hatamoto/comment/net-hash"),
	}
}

// NormalizeEmail trims and lowercases. It does not fold plus tags or dots.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// EmailDomain returns the lowercased part after the last "@", or "".
func EmailDomain(email string) string {
	e := NormalizeEmail(email)
	i := strings.LastIndex(e, "@")
	if i < 0 {
		return ""
	}
	return e[i+1:]
}

func mac(key []byte, s string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// HashEmail returns the pseudonymous HMAC of the normalised address.
func (k Keys) HashEmail(email string) string { return mac(k.emailHash, NormalizeEmail(email)) }

// HashIP returns pseudonymous HMACs of the address and of its network: the /24
// for IPv4 (4-in-6 addresses are unmapped first) or the /64 for IPv6. An
// unparsable ip returns "", "". The address itself is never stored.
func (k Keys) HashIP(ip string) (ipHash, netHash string) {
	addr, network, ok := clientkey.Prefix(ip)
	if !ok {
		return "", ""
	}
	return mac(k.ipHash, addr), mac(k.netHash, network)
}

func emailAAD(emailHash string) []byte { return []byte("comment-email:" + emailHash) }

// SealEmail encrypts the normalised address, bound to its hash.
func (k Keys) SealEmail(email, emailHash string) ([]byte, error) {
	return secretbox.Seal(k.seal, emailAAD(emailHash), []byte(NormalizeEmail(email)))
}

// OpenEmail returns the sealed address, or "" and false when it cannot be
// opened (for instance after the server secret changed).
func (k Keys) OpenEmail(blob []byte, emailHash string) (string, bool) {
	plain, err := secretbox.Open(k.seal, emailAAD(emailHash), blob)
	if err != nil {
		return "", false
	}
	return string(plain), true
}
