//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"testing"
)

// Through buildSiteView: the same *siteconfig.Config is decoded once, and the
// new Config a write produces (Invalidate) shows its theme values.
func TestBuildSiteViewDecodesOncePerConfig(t *testing.T) {
	ctx := context.Background()
	e := newWireEnv(t, nil)
	s, _ := e.server(t, "/admin")

	if err := s.siteConfig.PatchThemeExtra(ctx, "colors", jsontext.Value(`{"primary":"#123456"}`)); err != nil {
		t.Fatal(err)
	}
	first, err := s.buildSiteView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Tokens.Primary != "#123456" {
		t.Fatalf("primary = %q, want #123456", first.Tokens.Primary)
	}
	memo := s.siteDerived.Load()
	if memo == nil {
		t.Fatal("buildSiteView left no memo")
	}
	for range 20 {
		if _, err := s.buildSiteView(ctx); err != nil {
			t.Fatal(err)
		}
		if s.siteDerived.Load() != memo {
			t.Fatal("same config pointer was decoded again")
		}
	}

	if err := s.siteConfig.PatchThemeExtra(ctx, "colors", jsontext.Value(`{"primary":"#654321"}`)); err != nil {
		t.Fatal(err)
	}
	second, err := s.buildSiteView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Tokens.Primary != "#654321" {
		t.Errorf("after the config changed primary = %q, want #654321", second.Tokens.Primary)
	}
	if s.siteDerived.Load() == memo {
		t.Error("a new config pointer reused the old memo")
	}
}
