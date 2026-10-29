package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
)

type memCache struct {
	data map[string][]byte
	sets []string
	ttls []time.Duration
	gen  uint64
}

func (m *memCache) Generation() uint64 { return m.gen }

func (m *memCache) SetIfGeneration(ctx context.Context, k string, v []byte, ttl time.Duration, gen uint64) (bool, error) {
	if m.gen != gen {
		return false, nil
	}
	return true, m.Set(ctx, k, v, ttl)
}

func (m *memCache) Get(_ context.Context, k string) ([]byte, error) {
	if b, ok := m.data[k]; ok {
		return b, nil
	}
	return nil, cache.ErrCacheMiss
}

func (m *memCache) Set(_ context.Context, k string, v []byte, ttl time.Duration) error {
	m.ttls = append(m.ttls, ttl)
	if m.data == nil {
		m.data = map[string][]byte{}
	}
	m.data[k] = v
	m.sets = append(m.sets, k)
	return nil
}

type renderFixture struct {
	h        *renderHandlers
	cache    *memCache
	notFound []string
	resolves int
	next     func() (*routeResult, pageOutcome, error)
	redirect *redirect.Redirect
}

func newRenderFixture(t *testing.T) *renderFixture {
	t.Helper()
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := &renderFixture{cache: &memCache{}}
	f.h = &renderHandlers{
		baseURLPinned: true,
		engine:        engine,
		cache:         f.cache,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		log404:        func(path, _, _ string) { f.notFound = append(f.notFound, path) },
		checkRedirect: func(context.Context, string) *redirect.Redirect {
			return f.redirect
		},
		loadHome: func(*http.Request) (*PublicPageResponse, pageOutcome, error) {
			return pageResponse("Home"), pageOutcome{Status: 200}, nil
		},
		resolve: func(context.Context, string, *http.Request) (*routeResult, pageOutcome, error) {
			f.resolves++
			return f.next()
		},
		buildView: func(_ context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
			v := pageViewFromRoute(rr, "https://x.io", requestPath(r))
			v.Site = render.SiteView{Name: "Fixture Site"}
			return v, nil
		},
	}
	return f
}

func pageResponse(name string) *PublicPageResponse {
	return &PublicPageResponse{
		Entity: &entity.Entity{ID: uuid.New(), Name: name, Attributes: json.RawMessage(`{"page_sections":[]}`)},
		Meta:   PageMeta{Title: name},
	}
}

func okPage(name string) func() (*routeResult, pageOutcome, error) {
	return func() (*routeResult, pageOutcome, error) {
		return &routeResult{Page: pageResponse(name), Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
	}
}

func (f *renderFixture) get(path string, mut ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for _, m := range mut {
		m(r)
	}
	w := httptest.NewRecorder()
	// Global security middleware headers the handler must override.
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy-Report-Only", "default-src 'self'; frame-ancestors 'none'")
	if path == "/" {
		f.h.handleRenderHome(w, r)
	} else {
		f.h.handleRenderCatchAll(w, r)
	}
	return w
}

func TestRenderPageHeadersAndCache(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")

	w := f.get("/about")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "About") {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
	hd := w.Header()
	for k, want := range map[string]string{
		"Content-Type":    "text/html; charset=utf-8",
		"Cache-Control":   "public, max-age=60, s-maxage=300",
		"X-Frame-Options": "SAMEORIGIN",
	} {
		if got := hd.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	csp := hd.Get("Content-Security-Policy-Report-Only")
	if !strings.Contains(csp, "frame-ancestors 'self'") || strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP not overridden: %q", csp)
	}
	if len(f.cache.sets) != 1 {
		t.Fatalf("cache sets = %v", f.cache.sets)
	}
	if !strings.HasPrefix(f.cache.sets[0], "render:v1:") || !strings.HasSuffix(f.cache.sets[0], ":/about") {
		t.Errorf("cache key = %q", f.cache.sets[0])
	}

	w2 := f.get("/about")
	if f.resolves != 1 || w2.Body.String() != w.Body.String() {
		t.Errorf("second request must be a cache hit (resolves=%d)", f.resolves)
	}
}

func TestRenderQueryNotCached(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.get("/about?submitted=1")
	if len(f.cache.sets) != 0 {
		t.Errorf("query request cached: %v", f.cache.sets)
	}
}

func TestRenderRedirectIsReal(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{Redirect: &redirectHit{Target: "/new", Status: 301}}, pageOutcome{Status: 200}, nil
	}
	w := f.get("/old")
	if w.Code != 301 || w.Header().Get("Location") != "/new" {
		t.Fatalf("got %d %q", w.Code, w.Header().Get("Location"))
	}
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{Redirect: &redirectHit{Target: "/tmp", Status: 302}}, pageOutcome{Status: 200}, nil
	}
	if w := f.get("/old2"); w.Code != 302 {
		t.Fatalf("302 rule answered %d", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Errorf("redirects cached: %v", f.cache.sets)
	}
}

func TestRenderHomeRedirect(t *testing.T) {
	f := newRenderFixture(t)
	f.redirect = &redirect.Redirect{TargetURL: "/en", StatusCode: 302}
	if w := f.get("/"); w.Code != 302 || w.Header().Get("Location") != "/en" {
		t.Fatalf("got %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestRenderNotFoundThemedAndLogged(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	w := f.get("/nope")
	if w.Code != 404 || !strings.Contains(w.Body.String(), "<html") || !strings.Contains(w.Body.String(), "Fixture Site") {
		t.Fatalf("not a themed 404: %d %q", w.Code, w.Body.String())
	}
	if len(f.notFound) != 1 || f.notFound[0] != "/nope" {
		t.Errorf("Log404Async calls = %v", f.notFound)
	}
	if len(f.cache.sets) != 0 {
		t.Errorf("404 cached: %v", f.cache.sets)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content type %q", w.Header().Get("Content-Type"))
	}
}

func TestRenderReservedPathsAreJSON404(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("x")
	for _, p := range []string{"/api/x", "/media/a.png", "/api"} {
		w := f.get(p)
		if w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: %d %q", p, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if f.resolves != 0 {
		t.Errorf("reserved paths must not resolve (resolves=%d)", f.resolves)
	}
}

func TestRenderHomeServesHomepage(t *testing.T) {
	f := newRenderFixture(t)
	w := f.get("/")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Home") || f.resolves != 0 {
		t.Fatalf("home: %d resolves=%d", w.Code, f.resolves)
	}
	f.cache.data = nil
	f.h.loadHome = func(*http.Request) (*PublicPageResponse, pageOutcome, error) {
		return nil, pageOutcome{NotFound: true}, nil
	}
	if w := f.get("/"); w.Code != 404 {
		t.Errorf("no homepage: %d", w.Code)
	}
}

func TestRenderLoaderErrorIsThemedError(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return nil, pageOutcome{}, &pageError{http.StatusInternalServerError, "boom"}
	}
	w := f.get("/x")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	if len(f.cache.sets) != 0 {
		t.Error("error cached")
	}
}

func TestRenderNilCacheWorks(t *testing.T) {
	f := newRenderFixture(t)
	f.h.cache = nil
	f.next = okPage("About")
	if w := f.get("/about"); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestSectionsFromAttributes(t *testing.T) {
	got := sectionsFromAttributes(json.RawMessage(
		`{"page_sections":[{"id":"a","type":"hero","content":{"headline":"H"},"sort_order":2},{"type":"faq","isVisible":false}]}`))
	if len(got) != 2 || got[0].ID != "a" || got[0].Content["headline"] != "H" || got[0].SortOrder != 2 {
		t.Fatalf("%+v", got)
	}
	if got[1].ID != "s1" || got[1].IsVisible == nil || *got[1].IsVisible {
		t.Errorf("%+v", got[1])
	}
	if sectionsFromAttributes(json.RawMessage(`not json`)) != nil {
		t.Error("bad json must yield nil")
	}
}

func TestPageViewFromRoute(t *testing.T) {
	v := pageViewFromRoute(&routeResult{
		Page: pageResponse("Post"), CanonicalURL: "https://x.io/post/a",
	}, "https://x.io", "/post/a")
	if v.EntityID == "" || v.Page.Title != "Post" {
		t.Fatalf("%+v", v)
	}
}

func TestRenderRedirectBeatsCachedPage(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	if w := f.get("/about"); w.Code != 200 || len(f.cache.sets) != 1 {
		t.Fatalf("priming failed: %d %v", w.Code, f.cache.sets)
	}
	// A redirect added after the page was cached applies immediately.
	f.redirect = &redirect.Redirect{TargetURL: "/about-us", StatusCode: 301}
	w := f.get("/about")
	if w.Code != 301 || w.Header().Get("Location") != "/about-us" {
		t.Fatalf("got %d %q", w.Code, w.Header().Get("Location"))
	}
}

// Deleting or disabling the redirect puts the cached page back in charge.
func TestRenderCachedPageServedAgainAfterRedirectRemoved(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.get("/about")
	f.redirect = &redirect.Redirect{TargetURL: "/elsewhere", StatusCode: 302}
	if w := f.get("/about"); w.Code != 302 {
		t.Fatalf("redirect not applied: %d", w.Code)
	}
	f.redirect = nil
	if w := f.get("/about"); w.Code != 200 {
		t.Fatalf("page not served after redirect removal: %d", w.Code)
	}
}

func TestRenderEmptyRouteIsUncachedError(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
	}
	w := f.get("/post/hello", func(r *http.Request) { r.Header.Set("Accept", "application/ld+json") })
	if w.Code != 500 {
		t.Fatalf("status %d, want 500", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("empty route was cached: %v", f.cache.sets)
	}
	f.next = okPage("Hello")
	if w := f.get("/post/hello"); w.Code != 200 || !strings.Contains(w.Body.String(), "Hello") {
		t.Fatalf("follow-up request blank: %d %q", w.Code, w.Body.String())
	}
}

func TestWithoutAcceptDropsLDJSON(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/post/hello", nil)
	r.Header.Set("Accept", "application/ld+json")
	r2 := withoutAccept(r)
	if r2.Header.Get("Accept") != "" {
		t.Fatal("Accept leaked into the render sub-request")
	}
	if r.Header.Get("Accept") == "" {
		t.Fatal("original request mutated")
	}
}

func TestRenderUnpinnedBaseURLNeverCached(t *testing.T) {
	f := newRenderFixture(t)
	f.h.baseURLPinned = false
	f.next = okPage("About")
	if w := f.get("/about", func(r *http.Request) { r.Host = "evil.example" }); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("Host-derived page was cached: %v", f.cache.sets)
	}
}

func TestRenderDegradedPageIsServedButNotCached(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		p := pageResponse("Degraded")
		p.Entity.Attributes = json.RawMessage(`{"page_sections":[
			{"id":"g","type":"content","content":{"body":"<p>still here</p>"}},
			{"id":"b","type":"faq","content":{"faqs":"not a list"}}]}`)
		return &routeResult{Page: p, Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
	}
	w := f.get("/about")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "still here") || !strings.Contains(w.Body.String(), render.SectionFailedMarker) {
		t.Fatalf("degraded page not served: %d %q", w.Code, w.Body.String())
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("degraded page was cached: %v", f.cache.sets)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// newRenderHandlers is what mountRenderRoutes calls: with PUBLIC_BASE_URL
// unset the handlers must refuse to cache (Host-derived canonicals).
func TestNewRenderHandlersPinsBaseURLFromConfig(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for base, want := range map[string]bool{"": false, "  ": false, "https://x.io": true} {
		cfg := &config.Config{}
		cfg.PublicSite.BaseURL = base
		s := &Server{cfg: cfg, logger: log}
		if got := s.newRenderHandlers(nil, nil).baseURLPinned; got != want {
			t.Errorf("PUBLIC_BASE_URL %q: baseURLPinned = %v, want %v", base, got, want)
		}
	}
}

func TestRenderUncacheableViewIsServedButNotCached(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	build := f.h.buildView
	f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v, err := build(ctx, r, rr)
		if v != nil {
			failed := true // a Related query failed during render
			v.Uncacheable = &failed
		}
		return v, err
	}
	if w := f.get("/about"); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("page with a failed Related query was cached: %v", f.cache.sets)
	}
}

// A plugin that failed during the render (here a render.head filter that
// panics; template funcs and the other filters feed the same counter) degrades
// the page: served, never cached. A failure before the render does not count.
func TestRenderPluginFailureIsServedButNotCached(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	render.NoteDegraded() // earlier failure, outside any render
	if w := f.get("/ok"); w.Code != 200 || len(f.cache.sets) != 1 {
		t.Fatalf("healthy page: status %d, cache sets %v", w.Code, f.cache.sets)
	}

	engine, err := render.New(render.Options{HeadFilter: func(context.Context, render.HeadContext) render.HeadContext {
		panic("plugin trapped")
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.h.engine = engine
	f.cache.sets = nil
	w := f.get("/degraded")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "About") {
		t.Fatalf("degraded page not served: %d", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("page rendered with a failed plugin was cached: %v", f.cache.sets)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// A plugin failure during load() (jsonld.enrich runs inside the page loaders)
// degrades the page just as one during the render does.
func TestRenderPluginFailureDuringLoadIsNotCached(t *testing.T) {
	f := newRenderFixture(t)
	ok := okPage("About")
	f.next = func() (*routeResult, pageOutcome, error) {
		render.NoteDegraded() // a failing jsonld.enrich filter
		return ok()
	}
	w := f.get("/enrich")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("page degraded during load was cached: %v", f.cache.sets)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestRenderCacheUsesConfiguredTTL(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	if w := f.get("/about"); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(f.cache.ttls) != 1 || f.cache.ttls[0] != config.DefaultPageCacheTTL {
		t.Fatalf("unset TTL: got %v, want default %s", f.cache.ttls, config.DefaultPageCacheTTL)
	}

	f = newRenderFixture(t)
	f.next = okPage("About")
	f.h.cacheTTL = 6 * time.Hour
	f.get("/about")
	if len(f.cache.ttls) != 1 || f.cache.ttls[0] != 6*time.Hour {
		t.Fatalf("configured TTL: got %v", f.cache.ttls)
	}
}

// A render that started before a purge may have read the data from before the
// change: its page must be served but not cached, or it would stay for the TTL.
func TestRenderDoesNotCachePagesRacingAPurge(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		f.cache.gen++ // a write commits and purges while the page is loading
		return okPage("About")()
	}
	if w := f.get("/about"); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("page rendered across a purge was cached: %v", f.cache.sets)
	}
	// Without a purge in flight the page is cached as before.
	f.next = okPage("About")
	f.get("/about")
	if len(f.cache.sets) != 1 {
		t.Fatalf("sets = %v, want one", f.cache.sets)
	}
}

// A smoke request whose page came out degraded by a plugin failure counts as
// a smoke-attributed render error. The same page for anyone else, including a
// request that forges the smoke header or carries a theme-preview cookie
// (admin previews), does not.
func TestDegradedPageCountsOnlyForSmokeRequests(t *testing.T) {
	log := slog.New(NewCountingHandler(slog.NewTextHandler(io.Discard, nil)))
	f := newRenderFixture(t)
	f.h.logger = log
	f.next = okPage("About")
	engine, err := render.New(render.Options{HeadFilter: func(context.Context, render.HeadContext) render.HeadContext {
		panic("plugin trapped")
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.h.engine = engine

	before := RenderErrorCount()
	f.get("/a", func(r *http.Request) {
		r.Header.Set("X-Hatamoto-Smoke", "1")
		r.AddCookie(&http.Cookie{Name: previewCookie, Value: "x"})
	})
	if got := RenderErrorCount() - before; got != 0 {
		t.Fatalf("a non-smoke degraded page counted: %d", got)
	}
	f.get("/b", func(r *http.Request) { *r = *r.WithContext(smokeContext(r.Context())) })
	if got := RenderErrorCount() - before; got != 1 {
		t.Fatalf("a smoke degraded page counted %d, want 1", got)
	}
}

// Visitor traffic that degrades (plugin timeout, NoteDegraded) while a smoke
// request renders a healthy page must not count against that request.
func TestSmokeDegradedIgnoresConcurrentVisitorFailures(t *testing.T) {
	log := slog.New(NewCountingHandler(slog.NewTextHandler(io.Discard, nil)))
	f := newRenderFixture(t)
	f.h.logger = log
	f.next = okPage("About")
	engine, err := render.New(render.Options{HeadFilter: func(_ context.Context, in render.HeadContext) render.HeadContext {
		done := make(chan struct{})
		go func() { render.NoteDegraded(); close(done) }() // a visitor's render failing meanwhile
		<-done
		return in
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.h.engine = engine

	before := RenderErrorCount()
	f.get("/b", func(r *http.Request) { *r = *r.WithContext(smokeContext(r.Context())) })
	if got := RenderErrorCount() - before; got != 0 {
		t.Fatalf("visitor failure counted against a healthy smoke render: %d", got)
	}
}

func TestRenderCSPCaptchaProvider(t *testing.T) {
	cspOf := func(w *httptest.ResponseRecorder) string {
		return w.Header().Get("Content-Security-Policy-Report-Only")
	}
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.h.engine = struct{ pageRenderer }{f.h.engine} // a theme without csp.strictScripts

	// No provider: byte-identical to the default policy.
	if got := cspOf(f.get("/about")); got != renderCSP {
		t.Errorf("no provider: CSP = %q, want renderCSP", got)
	}
	provider := "none"
	f.h.captchaProvider = func(context.Context) string { return provider }
	if got := cspOf(f.get("/about")); got != renderCSP {
		t.Errorf("provider none: CSP = %q, want renderCSP", got)
	}

	// The page is now cached; a provider set afterwards must still reach the
	// cache-hit response, and only that provider's origins appear.
	provider = "turnstile"
	w := f.get("/about")
	if f.resolves != 1 {
		t.Fatalf("expected a cache hit, resolves = %d", f.resolves)
	}
	csp := cspOf(w)
	if !strings.Contains(csp, "script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com;") ||
		!strings.Contains(csp, "connect-src 'self' https://challenges.cloudflare.com;") {
		t.Errorf("turnstile CSP = %q", csp)
	}
	for _, other := range []string{"google.com", "gstatic.com", "hcaptcha.com"} {
		if strings.Contains(csp, other) {
			t.Errorf("turnstile CSP mentions %s: %q", other, csp)
		}
	}
	if w.Header().Get("Content-Security-Policy") != "" {
		t.Error("report-only header must stay report-only")
	}

	for p, want := range map[string]string{
		"recaptcha": "https://www.google.com https://www.gstatic.com",
		"hcaptcha":  "https://hcaptcha.com https://*.hcaptcha.com",
	} {
		provider = p
		if csp := cspOf(f.get("/about")); !strings.Contains(csp, "script-src 'self' 'unsafe-inline' "+want+";") ||
			!strings.Contains(csp, "connect-src 'self' "+want+";") {
			t.Errorf("%s CSP = %q", p, csp)
		}
	}

	// A provider that errors or is unknown falls back to the default policy.
	provider = "bogus"
	if got := cspOf(f.get("/about")); got != renderCSP {
		t.Errorf("unknown provider: CSP = %q", got)
	}
}

func TestCaptchaProviderFromSettings(t *testing.T) {
	srv := &Server{}
	if got := srv.captchaProvider(context.Background()); got != "" {
		t.Errorf("no pipeline: %q", got)
	}
	sk := spam.SecretKey("test-secret")
	sealed, err := spam.SealSecret(sk, "provider-secret")
	if err != nil {
		t.Fatal(err)
	}
	// A provider whose secret cannot be read renders no widget, so it gets no CSP origins.
	srv.spam = &spam.Pipeline{SecretKey: sk, Settings: fixedSettings{spam.Settings{Provider: spam.ProviderHCaptcha}}}
	if got := srv.captchaProvider(context.Background()); got != "" {
		t.Errorf("no secret: %q", got)
	}
	srv.spam = &spam.Pipeline{SecretKey: sk, Settings: fixedSettings{spam.Settings{Provider: spam.ProviderHCaptcha, SecretSealed: sealed}}}
	if got := srv.captchaProvider(context.Background()); got != spam.ProviderHCaptcha {
		t.Errorf("got %q", got)
	}
}

// A theme that opted in to csp.strictScripts (the default theme) gets a
// script-src without 'unsafe-inline'; provider origins are added to it too.
func TestRenderCSPStrictScripts(t *testing.T) {
	cspOf := func(w *httptest.ResponseRecorder) string {
		return w.Header().Get("Content-Security-Policy-Report-Only")
	}
	f := newRenderFixture(t)
	f.next = okPage("About")
	if _, ok := f.h.engine.(interface{ StrictScripts() bool }); !ok {
		t.Fatal("engine has no StrictScripts")
	}
	got := cspOf(f.get("/about"))
	if got != renderCSPStrict {
		t.Errorf("default theme CSP = %q, want renderCSPStrict", got)
	}
	if !strings.Contains(got, "script-src 'self';") || strings.Contains(strings.SplitN(got, "style-src", 2)[0], "unsafe-inline") {
		t.Errorf("script-src is not strict: %q", got)
	}
	if want := strings.Replace(renderCSP, "script-src 'self' 'unsafe-inline'", "script-src 'self'", 1); renderCSPStrict != want {
		t.Errorf("renderCSPStrict differs from renderCSP beyond script-src:\n%s\n%s", renderCSPStrict, want)
	}

	f.h.captchaProvider = func(context.Context) string { return "turnstile" }
	got = cspOf(f.get("/about"))
	if !strings.Contains(got, "script-src 'self' https://challenges.cloudflare.com;") || strings.Contains(strings.SplitN(got, "style-src", 2)[0], "unsafe-inline") {
		t.Errorf("strict + provider CSP = %q", got)
	}
}

// With a live community script every public HTML response carries its own
// enforced sandbox policy (opaque origin); without one, none does.
func TestWriteHTMLSandboxCSP(t *testing.T) {
	pages := map[string]struct {
		code int
		next func() (*routeResult, pageOutcome, error)
	}{
		"page": {200, okPage("About")},
		"404": {404, func() (*routeResult, pageOutcome, error) {
			return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
		}},
		"error": {500, func() (*routeResult, pageOutcome, error) {
			return nil, pageOutcome{}, &pageError{http.StatusInternalServerError, "boom"}
		}},
	}
	for name, pg := range pages {
		for _, live := range []bool{false, true} {
			for _, enforced := range []bool{false, true} {
				t.Run(name, func(t *testing.T) {
					f := newRenderFixture(t)
					f.h.communityScripts = func() bool { return live }
					f.next = pg.next
					for _, path := range []string{"/p", "/p"} { // the second hit of a cacheable page is served from the cache
						r := httptest.NewRequest(http.MethodGet, path, nil)
						w := httptest.NewRecorder()
						if enforced {
							w.Header().Set("Content-Security-Policy", "default-src 'self'")
						}
						f.h.handleRenderCatchAll(w, r)
						if w.Code != pg.code {
							t.Fatalf("status %d, want %d", w.Code, pg.code)
						}
						var sandboxed int
						for _, v := range w.Header().Values("Content-Security-Policy") {
							if strings.Contains(v, "allow-same-origin") {
								t.Fatalf("sandbox CSP allows the site origin: %q", v)
							}
							if strings.HasPrefix(v, "sandbox ") {
								sandboxed++
							}
						}
						if strings.Contains(w.Header().Get("Content-Security-Policy-Report-Only"), "sandbox") {
							t.Error("sandbox must not ride in Report-Only (ignored there)")
						}
						if want := map[bool]int{false: 0, true: 1}[live]; sandboxed != want {
							t.Fatalf("live=%v enforced=%v: %d sandbox policies, want %d: %q", live, enforced, sandboxed, want, w.Header().Values("Content-Security-Policy"))
						}
						if w.Header().Get("Cross-Origin-Opener-Policy") == "same-origin" {
							t.Error("COOP same-origin must not be set on public pages")
						}
					}
				})
			}
		}
	}
}

// A real snapshot error on a request is a 500 and never cached, for the home
// and every other path; only a build without an entity service (the route
// table) behaves as one locale.
func TestRenderSnapshotErrorIs500(t *testing.T) {
	for _, path := range []string{"/", "/about"} {
		t.Run(path, func(t *testing.T) {
			f := newRenderFixture(t)
			f.next = okPage("About")
			f.h.snapshot = func(context.Context) (*entity.URLSnapshot, error) { return nil, errors.New("db down") }
			if w := f.get(path); w.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", w.Code)
			}
			if len(f.cache.sets) != 0 {
				t.Errorf("an error page was cached: %v", f.cache.sets)
			}

			f.h.snapshot = func(context.Context) (*entity.URLSnapshot, error) { return nil, errNoEntityService }
			if w := f.get(path); w.Code != http.StatusOK {
				t.Fatalf("no entity service: status %d, want 200", w.Code)
			}
		})
	}
}
