package importjob

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"

	"github.com/google/uuid"
)

// DeriveKey makes the key that seals stored source credentials from the
// server's secret, in its own MAC domain.
func DeriveKey(secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("hatamoto/import/credentials"))
	return m.Sum(nil)
}

// seal encrypts plain with AES-256-GCM. The run id is authenticated, so a
// sealed credential cannot be moved to another run.
func seal(key []byte, run uuid.UUID, plain []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, run[:]), nil
}

func open(key []byte, run uuid.UUID, sealed []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("stored credential is damaged")
	}
	plain, err := g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], run[:])
	if err != nil {
		return nil, errors.New("stored credential cannot be read (the server's secret changed?)")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("import credential key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
