//go:build integration

package siteconfig_test

import (
	"context"
	"testing"
)

// The switch is off by default, SetPublicContentAPI turns it on and off, and
// the cached Get sees each change.
func TestPublicContentAPISwitch(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)

	cfg, err := s.Get(ctx)
	if err != nil || cfg.PublicContentAPI {
		t.Fatalf("default Get().PublicContentAPI = %v, %v; want false", cfg != nil && cfg.PublicContentAPI, err)
	}
	for _, want := range []bool{true, false} {
		if err := s.SetPublicContentAPI(ctx, want); err != nil {
			t.Fatal(err)
		}
		if cfg, err := s.Get(ctx); err != nil || cfg.PublicContentAPI != want {
			t.Fatalf("Get after set(%v): PublicContentAPI = %v, %v", want, cfg != nil && cfg.PublicContentAPI, err)
		}
	}
}
