//go:build integration

package siteconfig_test

import (
	"context"
	"testing"
)

// A fresh site has never chosen (nil); SetCSPEnforce stores the choice and Get
// sees it at once.
func TestCSPEnforce(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)

	cfg, err := s.Get(ctx)
	if err != nil || cfg.CSPEnforce != nil {
		t.Fatalf("default CSPEnforce = %v, %v; want nil", cfg.CSPEnforce, err)
	}
	for _, want := range []bool{true, false} {
		if err := s.SetCSPEnforce(ctx, want); err != nil {
			t.Fatal(err)
		}
		cfg, err := s.Get(ctx)
		if err != nil || cfg.CSPEnforce == nil || *cfg.CSPEnforce != want {
			t.Fatalf("CSPEnforce after set(%v) = %v, %v", want, cfg.CSPEnforce, err)
		}
	}
}
