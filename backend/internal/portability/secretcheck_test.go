package portability

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/secretbox"
)

func TestSecretCheckBasics(t *testing.T) {
	a := SecretCheck("s")
	if a == "" || a != SecretCheck("s") {
		t.Fatalf("want deterministic non-empty check, got %q", a)
	}
	if SecretCheck("") != "" {
		t.Fatal("empty secret must give an empty check")
	}
	secret := "correct-horse-battery"
	if c := SecretCheck(secret); strings.Contains(c, secret) || c == SecretCheck(secret+"x") {
		t.Fatal("check must not contain the secret and must differ per secret")
	}
}

func TestVerifySecretVerdicts(t *testing.T) {
	chk := SecretCheck("one")
	for _, tc := range []struct {
		check, secret string
		want          SecretVerdict
	}{
		{chk, "one", SecretMatch},
		{"", "one", SecretNoCheck},
		{"", "", SecretNoCheck},
		{chk, "", SecretUnknown},
		{chk, "two", SecretMismatch},
	} {
		if got := VerifySecret(tc.check, tc.secret); got != tc.want {
			t.Errorf("VerifySecret(%q,%q) = %v, want %v", tc.check, tc.secret, got, tc.want)
		}
	}
}

func TestSecretCheckKnownVector(t *testing.T) {
	m := hmac.New(sha256.New, secretbox.DeriveKey("vector-secret", "hatamoto/archive/check"))
	m.Write([]byte("hatamoto-archive-check-v1"))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if got := SecretCheck("vector-secret"); got != want {
		t.Fatalf("SecretCheck = %q, want %q", got, want)
	}
}
