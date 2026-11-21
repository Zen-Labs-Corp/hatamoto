//go:build integration

package siteconfig_test

import (
	"context"
	"testing"
)

// The switch is off by default, SetReviewEnabled turns it on, and both the
// uncached read and the cached Get see the change.
func TestReviewEnabled(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)

	if on, err := s.ReviewEnabled(ctx); err != nil || on {
		t.Fatalf("default ReviewEnabled = %v, %v; want false, nil", on, err)
	}
	cfg, err := s.Get(ctx)
	if err != nil || cfg.ReviewEnabled {
		t.Fatalf("default Get().ReviewEnabled = %v, %v; want false", cfg != nil && cfg.ReviewEnabled, err)
	}

	if err := s.SetReviewEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if on, err := s.ReviewEnabled(ctx); err != nil || !on {
		t.Fatalf("ReviewEnabled after set = %v, %v; want true", on, err)
	}
	if cfg, err := s.Get(ctx); err != nil || !cfg.ReviewEnabled {
		t.Fatalf("Get after set: ReviewEnabled = %v, %v; want true", cfg != nil && cfg.ReviewEnabled, err)
	}

	if err := s.SetReviewEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if on, err := s.ReviewEnabled(ctx); err != nil || on {
		t.Fatalf("ReviewEnabled after unset = %v, %v; want false", on, err)
	}
}
