package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"

	"hatamoto.dev/core/internal/testutil"
)

func TestHashPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
	}{
		{
			name:     "typical password",
			password: "correcthorsebatterystaple",
		},
		{
			name:     "short password",
			password: "a",
		},
		{
			name:     "empty password",
			password: "",
		},
		{
			name:     "unicode password",
			password: "p@$$w0rd-with-unicode-\u00e9\u00e8\u00ea",
		},
		{
			name:     "very long password",
			password: strings.Repeat("x", 1000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hash, err := HashPassword(tt.password)
			testutil.RequireNoError(t, err)
			testutil.NotEmpty(t, hash)
			testutil.True(t, strings.HasPrefix(hash, "$argon2id$"), "hash should start with $argon2id$")

			// Hash should contain all 6 dollar-delimited parts
			parts := strings.Split(hash, "$")
			testutil.Len(t, parts, 6, "hash should have 6 dollar-delimited parts")
		})
	}
}

func TestHashPassword_ProducesDifferentHashesForSameInput(t *testing.T) {
	t.Parallel()

	hash1, err := HashPassword("samepassword")
	testutil.RequireNoError(t, err)

	hash2, err := HashPassword("samepassword")
	testutil.RequireNoError(t, err)

	testutil.NotEqual(t, hash1, hash2, "same password should produce different hashes due to random salt")
}

func TestVerifyPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		password  string
		hashFrom  string // password used to generate the hash
		wantValid bool
		wantErr   bool
	}{
		{
			name:      "correct password matches its hash",
			password:  "mySecurePassword123!",
			hashFrom:  "mySecurePassword123!",
			wantValid: true,
			wantErr:   false,
		},
		{
			name:      "wrong password does not match",
			password:  "wrongpassword",
			hashFrom:  "mySecurePassword123!",
			wantValid: false,
			wantErr:   false,
		},
		{
			name:      "empty password matches empty hash",
			password:  "",
			hashFrom:  "",
			wantValid: true,
			wantErr:   false,
		},
		{
			name:      "empty password does not match non-empty hash",
			password:  "",
			hashFrom:  "notempty",
			wantValid: false,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hash, err := HashPassword(tt.hashFrom)
			testutil.RequireNoError(t, err)

			valid, err := VerifyPassword(tt.password, hash)
			if tt.wantErr {
				testutil.Error(t, err)
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, tt.wantValid, valid)
		})
	}
}

func TestVerifyPassword_InvalidHashFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hash    string
		wantErr error
	}{
		{
			name:    "completely invalid string",
			hash:    "notahash",
			wantErr: ErrInvalidHash,
		},
		{
			name:    "wrong algorithm prefix",
			hash:    "$bcrypt$v=19$m=65536,t=1,p=4$c29tZXNhbHQ$c29tZWhhc2g",
			wantErr: ErrInvalidHash,
		},
		{
			name:    "too few parts",
			hash:    "$argon2id$v=19$m=65536",
			wantErr: ErrInvalidHash,
		},
		{
			name:    "invalid version number",
			hash:    "$argon2id$v=99$m=65536,t=1,p=4$c29tZXNhbHQ$c29tZWhhc2g",
			wantErr: ErrIncompatibleVersion,
		},
		{
			name:    "invalid base64 salt",
			hash:    "$argon2id$v=19$m=65536,t=1,p=4$!!!invalid!!!$c29tZWhhc2g",
			wantErr: ErrInvalidHash,
		},
		{
			name:    "invalid base64 hash",
			hash:    "$argon2id$v=19$m=65536,t=1,p=4$c29tZXNhbHQ$!!!invalid!!!",
			wantErr: ErrInvalidHash,
		},
		{
			name:    "empty string",
			hash:    "",
			wantErr: ErrInvalidHash,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			valid, err := VerifyPassword("anypassword", tt.hash)
			testutil.False(t, valid)
			testutil.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestGenerateRandomString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		length int
	}{
		{name: "length 16", length: 16},
		{name: "length 32", length: 32},
		{name: "length 1", length: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := GenerateRandomString(tt.length)
			testutil.RequireNoError(t, err)
			testutil.Len(t, s, tt.length)
		})
	}
}

func TestGenerateRandomString_Uniqueness(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{})
	for range 100 {
		s, err := GenerateRandomString(32)
		testutil.RequireNoError(t, err)
		_, exists := seen[s]
		testutil.False(t, exists, "generated string should be unique")
		seen[s] = struct{}{}
	}
}

// encodeWith builds a stored hash with explicit parameters, as an older
// install would have written it.
func encodeWith(t *testing.T, password string, m, ti uint32, p uint8) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(password), salt, ti, m, p, 32)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, m, ti, p,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func TestHashPassword_TargetParams(t *testing.T) {
	enc, err := HashPassword("pw")
	testutil.RequireNoError(t, err)
	if !strings.Contains(enc, "$m=19456,t=2,p=1$") {
		t.Fatalf("hash = %q, want m=19456,t=2,p=1", enc)
	}
	testutil.False(t, NeedsRehash(enc))
}

func TestNeedsRehash(t *testing.T) {
	legacy := encodeWith(t, "pw", 65536, 1, 4)
	ok, err := VerifyPassword("pw", legacy)
	testutil.RequireNoError(t, err)
	testutil.True(t, ok)

	tests := []struct {
		name string
		enc  string
		want bool
	}{
		{"legacy", legacy, true},
		{"target", encodeWith(t, "pw", 19456, 2, 1), false},
		{"more memory", encodeWith(t, "pw", 47104, 2, 1), false},
		{"more time, other lanes", encodeWith(t, "pw", 65536, 2, 4), false},
		{"time below target", encodeWith(t, "pw", 19456, 1, 1), true},
		{"memory below target", encodeWith(t, "pw", 12288, 3, 1), true},
		{"reset marker", "!reset-required", false},
		{"garbage", "notahash", false},
		{"wrong version", "$argon2id$v=99$m=19456,t=2,p=1$c29tZXNhbHQ$c29tZWhhc2g", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsRehash(tt.enc); got != tt.want {
				t.Errorf("NeedsRehash = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVerifyPassword_OutOfBoundsParams(t *testing.T) {
	for _, params := range []string{
		"m=0,t=1,p=1", "m=19456,t=0,p=1", "m=19456,t=2,p=0", "m=1048576,t=1,p=1",
		"m=19456,t=11,p=1", "m=19456,t=2,p=17", "m=4,t=1,p=1",
	} {
		enc := "$argon2id$v=19$" + params + "$c29tZXNhbHQ$c29tZWhhc2g"
		ok, err := VerifyPassword("pw", enc)
		testutil.False(t, ok)
		testutil.ErrorIs(t, err, ErrInvalidHash)
		testutil.False(t, NeedsRehash(enc))
	}
	// key length outside 4..1024
	enc := "$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$YWI"
	if _, err := VerifyPassword("pw", enc); err == nil {
		t.Error("2-byte key accepted")
	}
}
