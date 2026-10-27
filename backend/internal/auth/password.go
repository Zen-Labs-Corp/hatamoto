// Package auth provides authentication utilities including password hashing, sessions and API keys.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters following OWASP recommendations.
const (
	argon2Time    = 1
	argon2Memory  = 64 * 1024 // 64 MB
	argon2Threads = 4
	argon2KeyLen  = 32
	argon2SaltLen = 16
)

// maxConcurrentHashes bounds argon2 work in flight. Each hash holds 64 MB, so
// a burst of sign-in attempts must queue rather than exhaust memory.
const maxConcurrentHashes = 4

var hashSlots = make(chan struct{}, maxConcurrentHashes)

// argon2ID runs argon2id once a slot is free. It stops waiting, without
// hashing, when ctx ends (a request whose client has gone must not keep a
// place in the queue).
func argon2ID(ctx context.Context, password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSlots }()
	return argon2.IDKey(password, salt, time, memory, threads, keyLen), nil
}

var (
	// ErrInvalidHash indicates the hash format is invalid.
	ErrInvalidHash = errors.New("invalid password hash format")
	// ErrIncompatibleVersion indicates an unsupported Argon2 version.
	ErrIncompatibleVersion = errors.New("incompatible argon2 version")
)

// HashPassword creates an Argon2id hash of the password.
func HashPassword(password string) (string, error) {
	return HashPasswordContext(context.Background(), password)
}

// HashPasswordContext is HashPassword that stops waiting for a hashing slot
// when ctx is cancelled.
func HashPasswordContext(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	hash, err := argon2ID(
		ctx,
		[]byte(password),
		salt,
		argon2Time,
		argon2Memory,
		argon2Threads,
		argon2KeyLen,
	)
	if err != nil {
		return "", err
	}

	// Encode as $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argon2Memory,
		argon2Time,
		argon2Threads,
		b64Salt,
		b64Hash,
	)

	return encoded, nil
}

// VerifyPassword checks if a password matches the hash.
func VerifyPassword(password, encodedHash string) (bool, error) {
	return VerifyPasswordContext(context.Background(), password, encodedHash)
}

// VerifyPasswordContext is VerifyPassword that stops waiting for a hashing
// slot when ctx is cancelled (it then returns ctx's error and does not hash).
func VerifyPasswordContext(ctx context.Context, password, encodedHash string) (bool, error) {
	params, salt, hash, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}

	otherHash, err := argon2ID(
		ctx,
		[]byte(password),
		salt,
		params.time,
		params.memory,
		params.threads,
		params.keyLen,
	)
	if err != nil {
		return false, err
	}

	// Constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare(hash, otherHash) == 1 {
		return true, nil
	}

	return false, nil
}

type argon2Params struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
}

func decodeHash(encodedHash string) (*argon2Params, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return nil, nil, nil, ErrInvalidHash
	}

	if parts[1] != "argon2id" {
		return nil, nil, nil, ErrInvalidHash
	}

	var version int
	_, err := fmt.Sscanf(parts[2], "v=%d", &version)
	if err != nil {
		return nil, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return nil, nil, nil, ErrIncompatibleVersion
	}

	params := &argon2Params{}
	_, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads)
	if err != nil {
		return nil, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, nil, ErrInvalidHash
	}

	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, nil, ErrInvalidHash
	}
	params.keyLen = uint32(len(hash))

	return params, salt, hash, nil
}

// GenerateRandomString creates a cryptographically secure random string.
func GenerateRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random string: %w", err)
	}
	return base64.URLEncoding.EncodeToString(bytes)[:length], nil
}
