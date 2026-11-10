package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/render"
)

func TestBusyPoolAnswersThemed503(t *testing.T) {
	f := newRenderFixture(t)
	f.h.buildView = func(context.Context, *http.Request, *routeResult) (*render.PageView, error) {
		t.Error("the 503 page must not build a view (it would touch the pool)")
		return nil, errors.New("unexpected")
	}
	f.next = func() (*routeResult, pageOutcome, error) {
		return nil, pageOutcome{}, fmt.Errorf("load: %w", database.ErrPoolBusy)
	}
	w := f.get("/x")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q", got)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Errorf("not the themed error page: %q", w.Body.String())
	}
	if len(f.cache.sets) != 0 {
		t.Error("503 cached")
	}
}

func TestBusyPoolFromLoaderFailureIs503(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return nil, pageOutcome{}, loadFailure(fmt.Errorf("get: %w", database.ErrPoolBusy), "failed to get page")
	}
	if w := f.get("/x"); w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("status = %d, Retry-After = %q", w.Code, w.Header().Get("Retry-After"))
	}
	f.next = func() (*routeResult, pageOutcome, error) {
		return nil, pageOutcome{}, loadFailure(errors.New("db"), "failed to get page")
	}
	if w := f.get("/x"); w.Code != http.StatusInternalServerError {
		t.Fatalf("other failures stay 500, got %d", w.Code)
	}
}

func TestPublicAcquireBoundMarksPublicRoutesOnly(t *testing.T) {
	f := newRenderFixture(t)
	var seen = map[string]time.Duration{}
	record := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			seen[name] = database.AcquireBound(r.Context())
		}
	}
	r, h := baseRouter()
	r.Group("/api/v1").HandleFunc("GET /bound", record("api"))
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: record("robots"), sitemap: record("sitemap"),
		subSitemap: record("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
		acquireBound: 1500 * time.Millisecond,
	})
	getPath(h, "/robots.txt")
	getPath(h, "/sitemap.xml")
	getPath(h, "/sitemap-posts.xml")
	getPath(h, "/api/v1/bound")
	for _, name := range []string{"robots", "sitemap"} {
		if seen[name] != 1500*time.Millisecond {
			t.Errorf("%s bound = %s, want 1.5s", name, seen[name])
		}
	}
	if seen["api"] != 0 {
		t.Errorf("api bound = %s, want none", seen["api"])
	}
}

func TestPublicAcquireBoundDisabled(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		var got time.Duration
		h := publicAcquireBound(d)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = database.AcquireBound(r.Context())
		}))
		h.ServeHTTP(nil, httptest.NewRequest(http.MethodGet, "/", nil))
		if got != 0 {
			t.Errorf("d=%s: bound = %s, want none", d, got)
		}
	}
}
