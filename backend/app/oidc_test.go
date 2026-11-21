package app

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/oidcsignin"
)

// The provider is registered by package init. Tests that call
// authstate.Reset live in the integration build, so this one sees the
// registration untouched.
func TestOIDCProviderRegistered(t *testing.T) {
	var found bool
	for _, p := range authstate.Providers() {
		if p.ID == "oidc" {
			found = true
		}
	}
	if !found {
		t.Fatal("oidc provider is not registered")
	}
	if got := authstate.AvailableProviders(context.Background()); len(got) != 0 {
		t.Fatalf("without a runtime no provider is available, got %v", got)
	}
	if _, ok := any(oidcsignin.Default()).(authstate.ProviderAvailability); !ok {
		t.Fatal("provider does not implement ProviderAvailability")
	}
}
