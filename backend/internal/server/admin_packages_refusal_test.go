package server

import (
	"errors"
	"fmt"
	"strings"
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

// The developer-trust refusals are readable refusals (422), never a 500, and
// the client-view text reaches the owner.
func TestRefusalDeveloperTrust(t *testing.T) {
	off := &packages.DeveloperError{Name: "capa", KeyID: "abcd", Err: packages.ErrDeveloperOff}
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"off", fmt.Errorf("install capa: %w", off), "Allow developer packages"},
		{"key mismatch", &packages.DeveloperError{Name: "capa", KeyID: "aaaa", WantKeyID: "bbbb", Err: packages.ErrDeveloperKeyMismatch}, "bbbb"},
		{"signer changed", &packages.DeveloperError{Name: "capa", Version: "2", KeyID: "aaaa", WantKeyID: "bbbb", Err: packages.ErrDeveloperSignerChanged}, "installed version"},
		{"revoked", fmt.Errorf("capa: %w: leaked", packages.ErrRevoked), "revoked"},
	} {
		err := refusal(c.err)
		var r *admin.Refusal
		if !errors.As(err, &r) {
			t.Fatalf("%s: got %T %v, want *admin.Refusal", c.name, err, err)
		}
		if !strings.Contains(r.Msg, c.want) {
			t.Errorf("%s: message %q lacks %q", c.name, r.Msg, c.want)
		}
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
