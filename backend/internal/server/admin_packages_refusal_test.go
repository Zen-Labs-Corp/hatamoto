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
