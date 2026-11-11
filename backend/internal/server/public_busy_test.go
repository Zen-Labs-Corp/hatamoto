package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
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

// A caller that swallows a pool-busy error (menus, fonts, settings) leaves a
// page without data: it must answer 503, never be cached or sent public.
func TestSwallowedPoolBusyIsNotCachedOrServed(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	inner := f.h.buildView
	f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		database.NotePoolBusy(ctx) // e.g. the menu lookup gave up and rendered empty
		return inner(ctx, r, rr)
	}
	w := f.get("/about", func(r *http.Request) {
		*r = *r.WithContext(database.WithAcquireBound(r.Context(), time.Second))
	})
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("status = %d, Retry-After = %q", w.Code, w.Header().Get("Retry-After"))
	}
	if strings.Contains(w.Header().Get("Cache-Control"), "public") {
		t.Errorf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	if len(f.cache.sets) != 0 {
		t.Errorf("degraded page cached: %v", f.cache.sets)
	}
}

func TestPoolBusyHitSkipsSitemapAndFeedCache(t *testing.T) {
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := &Server{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		infra:  &Infrastructure{Cache: c},
		cfg:    &config.Config{PublicSite: config.PublicSiteConfig{BaseURL: "https://x.io"}},
	}
	busy := database.WithAcquireBound(context.Background(), time.Second)
	database.NotePoolBusy(busy)
	r := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil).WithContext(busy)
	s.cacheSitemapXML(r, "index", []byte("<urlset/>"))
	s.cacheFeed(busy, "feed-key", []byte("<rss/>"))
	if b, _ := c.Get(busy, cache.SitemapKey("index")); len(b) > 0 {
		t.Error("degraded sitemap cached")
	}
	if b, _ := c.Get(busy, "feed-key"); len(b) > 0 {
		t.Error("degraded feed cached")
	}
	ok := database.WithAcquireBound(context.Background(), time.Second)
	s.cacheSitemapXML(r.WithContext(ok), "index", []byte("<urlset/>"))
	s.cacheFeed(ok, "feed-key", []byte("<rss/>"))
	if b, err := c.Get(ok, cache.SitemapKey("index")); err != nil || len(b) == 0 {
		t.Errorf("healthy sitemap not cached: %v", err)
	}
	if b, err := c.Get(ok, "feed-key"); err != nil || len(b) == 0 {
		t.Errorf("healthy feed not cached: %v", err)
	}
}

// Routes other than HTML pages (robots.txt, sitemaps, feeds, llms.txt, form
// pages) answer 503 + Retry-After, never cached, when a bounded acquire gave up
// -- whether they degraded to a 200 or failed with a 500.
func TestBusyPoolAnswers503OnNonPageRoutes(t *testing.T) {
	f := newRenderFixture(t)
	s := &Server{robotsPolicy: fakeRobotsStore{err: fmt.Errorf("denied: %w", database.ErrPoolBusy)}}
	busyThen := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			database.NotePoolBusy(r.Context())
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.WriteHeader(code)
			_, _ = w.Write([]byte("partial"))
		}
	}
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: s.handleRobots, sitemap: busyThen(http.StatusInternalServerError),
		subSitemap: busyThen(http.StatusInternalServerError), llms: busyThen(http.StatusOK), xsl: stub("xsl"),
		formPost: stub("form"), feed: busyThen(http.StatusInternalServerError),
		formPage: busyThen(http.StatusInternalServerError), acquireBound: time.Second,
	})
	for _, p := range []string{"/robots.txt", "/sitemap.xml", "/sitemap-posts.xml", "/llms.txt", "/feed.xml", "/f/contact"} {
		w := getPath(h, p)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
			t.Errorf("%s: status = %d, Retry-After = %q", p, w.Code, w.Header().Get("Retry-After"))
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control = %q", p, cc)
		}
		if strings.Contains(w.Body.String(), "partial") {
			t.Errorf("%s: degraded body leaked: %q", p, w.Body.String())
		}
	}
}

// Without a flagged acquire the converter is invisible.
func TestBusyConverterPassesHealthyAnswers(t *testing.T) {
	h := publicAcquireBound(time.Second)(busyConverter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	})))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusTeapot || w.Body.String() != "ok" || w.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("%d %q %v", w.Code, w.Body.String(), w.Header())
	}
}

// getThroughBusyConverter serves path the way the router does for a public
// page: bounded request context, handler wrapped in busyConverter.
func getThroughBusyConverter(f *renderFixture, path string) *httptest.ResponseRecorder {
	h := busyConverter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path == "/" {
			f.h.handleRenderHome(w, r)
		} else {
			f.h.handleRenderCatchAll(w, r)
		}
	}))
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r = r.WithContext(database.WithAcquireBound(r.Context(), time.Second))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A page in the cache is served as a 200 even when the pool is starved and the
// URL snapshot gave up with a pool-busy flag.
func TestStarvedPoolStillServesCachedPage(t *testing.T) {
	for _, path := range []string{"/", "/about"} {
		t.Run(path, func(t *testing.T) {
			f := newRenderFixture(t)
			f.next = okPage("About")
			if w := f.get(path); w.Code != http.StatusOK {
				t.Fatalf("warm: status %d", w.Code)
			}
			f.h.snapshot = func(ctx context.Context) (*entity.URLSnapshot, error) {
				database.NotePoolBusy(ctx)
				return nil, fmt.Errorf("waiting for shared load: %w", database.ErrPoolBusy)
			}
			w := getThroughBusyConverter(f, path)
			if w.Code != http.StatusOK {
				t.Fatalf("cached page under a starved pool: status %d, want 200", w.Code)
			}
		})
	}
}

// A page that has to be built while the snapshot is unavailable is still a 503.
func TestStarvedPoolUncachedPageIs503(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.h.snapshot = func(ctx context.Context) (*entity.URLSnapshot, error) {
		database.NotePoolBusy(ctx)
		return nil, fmt.Errorf("waiting for shared load: %w", database.ErrPoolBusy)
	}
	w := getThroughBusyConverter(f, "/about")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", w.Code)
	}
}
