//go:build integration

package siteconfig_test

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func TestFaviconRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)

	cfg, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FaviconURL != "" {
		t.Fatalf("default favicon = %q, want empty", cfg.FaviconURL)
	}
	icon := "/media/icon.png"
	if cfg, err = s.Update(ctx, siteconfig.UpdateInput{FaviconURL: &icon}); err != nil {
		t.Fatal(err)
	}
	if cfg.FaviconURL != icon {
		t.Fatalf("favicon = %q, want %q", cfg.FaviconURL, icon)
	}
	// A patch without the field leaves it alone; an empty string clears it.
	name := "Acme"
	if cfg, err = s.Update(ctx, siteconfig.UpdateInput{CompanyName: &name}); err != nil || cfg.FaviconURL != icon {
		t.Fatalf("unrelated update: favicon %q, err %v", cfg.FaviconURL, err)
	}
	empty := ""
	if cfg, err = s.Update(ctx, siteconfig.UpdateInput{FaviconURL: &empty}); err != nil || cfg.FaviconURL != "" {
		t.Fatalf("clear: favicon %q, err %v", cfg.FaviconURL, err)
	}
}
