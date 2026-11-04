package twofactor

import (
	"os"
	"strings"
	"testing"
)

// The ceremony purge runs on every unauthenticated begin and deletes by
// expires_at, so that column needs an index.
func TestCeremonyExpiryIsIndexed(t *testing.T) {
	b, err := os.ReadFile("../../migrations/202611041100_two_factor.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ON hatamoto_webauthn_ceremonies (expires_at)") {
		t.Fatal("no index on hatamoto_webauthn_ceremonies (expires_at)")
	}
}
