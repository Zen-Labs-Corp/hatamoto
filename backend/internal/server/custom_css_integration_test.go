//go:build integration

package server

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/render"
)

// Saving Additional CSS (the store write the admin page does) makes the head
// link name and the served file agree, and the previous name stops resolving.
func TestAdditionalCSSSaveThenServe(t *testing.T) {
	ctx := context.Background()
	e := newWireEnv(t, nil)
	s, _ := e.server(t, "/admin")
	t.Cleanup(func() { render.SetCustomCSS(nil) })

	save := func(css string) string {
		t.Helper()
		raw, _ := jsonx.Marshal(css)
		if err := s.siteConfig.PatchThemeExtra(ctx, render.AdditionalCSSKey, raw); err != nil {
			t.Fatal(err)
		}
		site, err := s.buildSiteView(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return site.CustomCSSName
	}

	first := "a{color:red}"
	n1 := save(first)
	if n1 == "" {
		t.Fatal("no stylesheet name after save")
	}
	if rec := get(s, "/theme/assets/"+n1); rec.Code != 200 || rec.Body.String() != first {
		t.Fatalf("served %d %q", rec.Code, rec.Body.String())
	}
	n2 := save("a{color:blue}")
	if n2 == n1 {
		t.Fatal("name did not change with the CSS")
	}
	if rec := get(s, "/theme/assets/"+n2); rec.Code != 200 || !strings.Contains(rec.Body.String(), "blue") {
		t.Fatalf("new name served %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(s, "/theme/assets/"+n1); rec.Code != 404 {
		t.Fatalf("old name = %d, want 404", rec.Code)
	}
	if save("") != "" {
		t.Error("cleared CSS still has a name")
	}
	if rec := get(s, "/theme/assets/"+n2); rec.Code != 404 {
		t.Fatalf("cleared name = %d, want 404", rec.Code)
	}
}
