package auth

import (
	"strings"
	"testing"

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
