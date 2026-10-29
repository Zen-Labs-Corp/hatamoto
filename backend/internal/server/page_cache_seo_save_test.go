package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/seo"
)

// A render that read the SEO settings before a settings save purged the page
// cache must be served but never stored: the page cache generation is sampled
// before the render reads settings, and the save's purge advances it.
func TestPageCacheDropsRenderThatRacedSettingsSave(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	infra := NewInfrastructure(logger)
	srv := &Server{
		logger:   logger,
		infra:    infra,
		seoStore: seo.NewStore(&seoQuerier{row: seoRow{raw: []byte(docTemplates)}}),
	}
	f := newRenderFixture(t)
	f.h.cache = infra.Cache
	saved := false
	f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v := pageViewFromRoute(rr, "https://x.io", requestPath(r))
		v.SEO = srv.seoSettings(ctx) // the render reads the settings...
		if !saved {
			saved = true
			srv.invalidateRendered(ctx) // ...then a settings save commits and purges
		}
		return v, nil
	}
	f.next = okPage("About")

	key := cache.RenderedPageKey(f.h.engine.Version(), "/about")
	get := func() {
		t.Helper()
		w := httptest.NewRecorder()
		f.h.handleRenderCatchAll(w, httptest.NewRequest(http.MethodGet, "/about", nil))
		if w.Code != 200 {
			t.Fatalf("status %d", w.Code)
		}
	}

	get()
	if _, err := infra.Cache.Get(context.Background(), key); err == nil {
		t.Fatal("page rendered across a settings save was cached")
	}

	// With no save in flight the same page is cached.
	get()
	if _, err := infra.Cache.Get(context.Background(), key); err != nil {
		t.Fatalf("page not cached without a racing save: %v", err)
	}
}
