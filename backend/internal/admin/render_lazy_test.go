package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// New does not build the assets or parse the page sources: that waits for the
// first request that needs them.
func TestAdminBuildsOnFirstUse(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.a.mu.RLock()
	loaded, assets, pages := g.a.loaded, g.a.assets, g.a.pages
	g.a.mu.RUnlock()
	if loaded || assets != nil || pages != nil {
		t.Fatal("New built the admin assets and pages")
	}
	if err := g.a.ensureBuilt(); err != nil {
		t.Fatal(err)
	}
	as, pages := g.a.state()
	if as == nil || len(pages) == 0 {
		t.Fatal("nothing built on first use")
	}
	again, _ := g.a.state()
	if again != as {
		t.Error("the bundle must be built once")
	}
}

// The lazy build still parses every embedded page, so a template error is
// caught in CI and not only on the first visit.
func TestLazyBuildParsesEveryPage(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if err := g.a.ensureBuilt(); err != nil {
		t.Fatal(err)
	}
	as, pages := g.a.state()
	if as.CSSURL == "" {
		t.Error("the asset bundle has no stylesheet")
	}
	for key, ps := range pages {
		if _, err := ps.tmpl(); err != nil {
			t.Errorf("page %s: %v", key, err)
		}
	}
	if _, err := g.a.partials.tmpl(); err != nil {
		t.Errorf("partials: %v", err)
	}
}

// An asset request is what first builds the admin on a site nobody browses.
func TestAssetRequestBuildsAdmin(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	req := httptest.NewRequest(http.MethodGet, "/_/tokens.css", nil)
	req.SetPathValue("path", "tokens.css")
	rec := httptest.NewRecorder()
	g.a.serveAssets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tokens.css = %d", rec.Code)
	}
	g.a.mu.RLock()
	loaded := g.a.loaded
	g.a.mu.RUnlock()
	if !loaded {
		t.Error("serving an asset did not build the admin")
	}
}
