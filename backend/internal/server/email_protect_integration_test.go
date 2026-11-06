//go:build integration

package server

import (
	"context"
	"encoding/json"
	"testing"
)

// buildSiteView maps the stored protectEmail setting to SiteView.EmailPlain:
// absent or true protects, false renders literal addresses.
func TestSiteViewEmailPlainFollowsSetting(t *testing.T) {
	ctx := context.Background()
	e := newWireEnv(t, nil)
	s, _ := e.server(t, "/admin")

	plain := func() bool {
		t.Helper()
		site, err := s.buildSiteView(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return site.EmailPlain
	}
	if plain() {
		t.Error("default must protect")
	}
	for _, c := range []struct {
		value string
		plain bool
	}{{"false", true}, {"true", false}} {
		if err := s.siteConfig.PatchThemeExtra(ctx, "protectEmail", json.RawMessage(c.value)); err != nil {
			t.Fatal(err)
		}
		if got := plain(); got != c.plain {
			t.Errorf("protectEmail=%s: EmailPlain=%v, want %v", c.value, got, c.plain)
		}
	}
}
