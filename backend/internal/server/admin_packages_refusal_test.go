package server

import (
	"errors"
	"fmt"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/packages"
)

// A community grant refusal on install or update is a readable refusal, not a
// 500 for the owner.
func TestRefusalCommunityGrant(t *testing.T) {
	err := refusal(fmt.Errorf("install fancy: %w", packages.ErrCommunityScripts))
	var r *admin.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("got %T %v, want *admin.Refusal", err, err)
	}
	if r.Msg == "" {
		t.Fatal("empty refusal message")
	}
}

// The plugin header and list star only a listed first-party plugin: the
// registry key also signs community plugins.
func TestFirstPartyRow(t *testing.T) {
	reg := packages.FirstPartyKeyIDs[0]
	for _, c := range []struct {
		name, signer string
		want         bool
	}{
		{"searchconsole", reg, true},
		{"fancy", reg, false},
		{"searchconsole", "0123456789abcdef", false},
		{"searchconsole", "", false},
	} {
		if got := firstPartyRow(packages.Row{Name: c.name, SignerKeyID: c.signer}); got != c.want {
			t.Errorf("%s signed by %q: got %v, want %v", c.name, c.signer, got, c.want)
		}
	}
}
