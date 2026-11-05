//go:build integration

package siteconfig_test

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func TestAdminBrandingRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)

	cfg, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminBrand != (siteconfig.AdminBrand{}) {
		t.Fatalf("default = %+v, want empty", cfg.AdminBrand)
	}

	want := siteconfig.AdminBrand{
		Name: "Studio Console", LogoURL: "/media/logo.png", Accent: "#1f4e9a",
		ManagedText: "Managed by Example", ManagedURL: "https://example.test",
	}
	if err := s.SetAdminBranding(ctx, want); err != nil {
		t.Fatal(err)
	}
	if cfg, err = s.Get(ctx); err != nil {
		t.Fatal(err)
	}
	if cfg.AdminBrand != want {
		t.Fatalf("got %+v, want %+v", cfg.AdminBrand, want)
	}

	if err := s.ResetAdminBranding(ctx); err != nil {
		t.Fatal(err)
	}
	if cfg, err = s.Get(ctx); err != nil {
		t.Fatal(err)
	}
	if cfg.AdminBrand != (siteconfig.AdminBrand{}) {
		t.Fatalf("after reset = %+v, want empty", cfg.AdminBrand)
	}
}
