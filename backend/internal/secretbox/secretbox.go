// Package secretbox seals small secrets (such as API keys) at rest with
// AES-256-GCM, keyed from the server's secret in a caller-chosen domain.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

// DeriveKey makes a 32-byte key from the server's secret in its own MAC domain.
func DeriveKey(secret, domain string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(domain))
	return m.Sum(nil)
}

// Seal encrypts plain and authenticates aad. The result is nonce||ciphertext.
func Seal(key, aad, plain []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, aad), nil
}

// Open reverses Seal. It fails when the key, aad or blob differ.
func Open(key, aad, blob []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < g.NonceSize() {
		return nil, errors.New("sealed value is damaged")
	}
	plain, err := g.Open(nil, blob[:g.NonceSize()], blob[g.NonceSize():], aad)
	if err != nil {
		return nil, errors.New("sealed value cannot be read (the server's secret changed?)")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("secretbox key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
