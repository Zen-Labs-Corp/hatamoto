package server

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/render"
)

var weakETagRE = regexp.MustCompile(`^W/"[0-9a-f]{16}"$`)

// conditionalGet is f.get with a method and request headers.
func (f *renderFixture) conditionalGet(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	return f.get(path, func(r *http.Request) {
		r.Method = method
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
	})
}

// stablePage serves the same entity on every render: a fresh uuid per call
// would make byte-identical re-renders impossible.
func stablePage(name string) func() (*routeResult, pageOutcome, error) {
	p := pageResponse(name)
	return func() (*routeResult, pageOutcome, error) {
		return &routeResult{Page: p, Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
	}
}

// warmedFixture is a fixture whose /about page is cached and whose clock is
// past the construction floor, so Last-Modified is sent.
func warmedFixture(t *testing.T) (*renderFixture, string) {
	t.Helper()
	f := newRenderFixture(t)
	f.next = stablePage("About")
	f.advance(2 * time.Second)
	w := f.get("/about")
	etag := w.Header().Get("ETag")
	if w.Code != 200 || !weakETagRE.MatchString(etag) {
		t.Fatalf("warm-up: %d ETag=%q", w.Code, etag)
	}
	return f, etag
}

func TestPageValidatorsOnCacheablePages(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.advance(2 * time.Second)

	fresh := f.get("/about")
	hit := f.get("/about")
	if f.resolves != 1 {
		t.Fatalf("resolves = %d, the second request must be a cache hit", f.resolves)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{"fresh": fresh, "hit": hit} {
		if w.Code != 200 || !weakETagRE.MatchString(w.Header().Get("ETag")) || w.Header().Get("Last-Modified") == "" {
			t.Errorf("%s: status %d ETag=%q Last-Modified=%q", name, w.Code, w.Header().Get("ETag"), w.Header().Get("Last-Modified"))
		}
	}
	if fresh.Header().Get("ETag") != hit.Header().Get("ETag") || fresh.Header().Get("Last-Modified") != hit.Header().Get("Last-Modified") {
		t.Errorf("fresh %v and hit %v disagree", fresh.Header(), hit.Header())
	}
}

func TestPageValidatorsNeverOnPrivate(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		setup func(t *testing.T, f *renderFixture) *renderFixture
	}{
		{"filtered", "/members", func(t *testing.T, f *renderFixture) *renderFixture {
			_, s := bootRoutes(t)
			capaRuntime(t, s)
			f.h.pageFilter = pluginhost.NewPageFilter(s.plugins, s.hooks)
			f.next = membersPage
			return f
		}},
		{"uncached", "/about", func(_ *testing.T, f *renderFixture) *renderFixture {
			f.h.uncached = true
			return f
		}},
		{"degraded", "/about", func(_ *testing.T, f *renderFixture) *renderFixture {
			f.next = func() (*routeResult, pageOutcome, error) {
				p := pageResponse("Degraded")
				p.Entity.Attributes = jsontext.Value(`{"page_sections":[
					{"id":"b","type":"faq","content":{"faqs":"not a list"}}]}`)
				return &routeResult{Page: p, Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
			}
			return f
		}},
		{"preview", "/about", func(_ *testing.T, f *renderFixture) *renderFixture {
			return withView(f, func(v *render.PageView) { v.Preview = true })
		}},
		{"dynamic", "/about", func(_ *testing.T, f *renderFixture) *renderFixture {
			return withView(f, func(v *render.PageView) { v.Dynamic = true })
		}},
		{"paged without loop", "/news?page=2", func(t *testing.T, _ *renderFixture) *renderFixture {
			return pagedFixture(t, false)
		}},
		{"not found", "/about", func(_ *testing.T, f *renderFixture) *renderFixture {
			f.next = func() (*routeResult, pageOutcome, error) {
				return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
			}
			return f
		}},
		{"error", "/about", func(_ *testing.T, f *renderFixture) *renderFixture {
			f.next = func() (*routeResult, pageOutcome, error) { return nil, pageOutcome{}, errors.New("boom") }
			return f
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRenderFixture(t)
			f.next = okPage("About")
			f = tc.setup(t, f)
			f.advance(2 * time.Second)
			for _, hdr := range []map[string]string{nil, {"If-None-Match": "*"}} {
				w := f.conditionalGet(http.MethodGet, tc.path, hdr)
				if w.Header().Get("ETag") != "" || w.Header().Get("Last-Modified") != "" {
					t.Errorf("validators on a private response: %v", w.Header())
				}
				if w.Code == http.StatusNotModified {
					t.Error("a private response answered 304")
				}
			}
		})
	}
}

// withView lets a test adjust every view the fixture builds.
func withView(f *renderFixture, adjust func(*render.PageView)) *renderFixture {
	build := f.h.buildView
	f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v, err := build(ctx, r, rr)
		if err == nil {
			adjust(v)
		}
		return v, err
	}
	return f
}

func TestPageIfNoneMatch(t *testing.T) {
	f, etag := warmedFixture(t)
	bare := strings.TrimPrefix(etag, "W/")
	cases := []struct {
		name   string
		method string
		hdr    map[string]string
		want   int
	}{
		{"exact", "GET", map[string]string{"If-None-Match": etag}, 304},
		{"bare", "GET", map[string]string{"If-None-Match": bare}, 304},
		{"list", "GET", map[string]string{"If-None-Match": `"nope", ` + etag}, 304},
		{"star", "GET", map[string]string{"If-None-Match": "*"}, 304},
		{"head", "HEAD", map[string]string{"If-None-Match": etag}, 304},
		{"empty", "GET", map[string]string{"If-None-Match": ""}, 200},
		{"mismatch", "GET", map[string]string{"If-None-Match": `W/"0000000000000000"`}, 200},
		{"mismatch beats If-Modified-Since", "GET", map[string]string{
			"If-None-Match":     `W/"0000000000000000"`,
			"If-Modified-Since": time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat),
		}, 200},
	}
	check := func(t *testing.T, path, name string, method string, hdr map[string]string, want int) {
		t.Helper()
		w := f.conditionalGet(method, path, hdr)
		if w.Code != want {
			t.Fatalf("%s: status %d, want %d", name, w.Code, want)
		}
		if want == 304 && w.Body.Len() != 0 {
			t.Fatalf("%s: 304 with a body", name)
		}
		if want == 200 && method == "GET" && !strings.Contains(w.Body.String(), "About") {
			t.Fatalf("%s: 200 without the page", name)
		}
	}
	for _, tc := range cases {
		t.Run("hit "+tc.name, func(t *testing.T) { check(t, "/about", tc.name, tc.method, tc.hdr, tc.want) })
	}
	for _, tc := range cases {
		t.Run("fresh "+tc.name, func(t *testing.T) {
			f.cache.data = nil // purge: the same bytes are re-rendered and stored
			before := f.resolves
			check(t, "/about", tc.name, tc.method, tc.hdr, tc.want)
			if f.resolves != before+1 {
				t.Fatalf("resolves %d -> %d, want a fresh render", before, f.resolves)
			}
		})
	}
}

// An empty If-None-Match is an absent one: If-Modified-Since still decides.
func TestPageEmptyIfNoneMatchFallsBack(t *testing.T) {
	f, _ := warmedFixture(t)
	lm := f.conditionalGet(http.MethodGet, "/about", nil).Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("no Last-Modified to revalidate with")
	}
	w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-None-Match": "", "If-Modified-Since": lm})
	if w.Code != http.StatusNotModified {
		t.Fatalf("empty If-None-Match + current If-Modified-Since: status %d, want 304", w.Code)
	}
}

// A request whose bounded acquire gave up never gets validators or a 304,
// whether the page comes from the cache or is rendered.
func TestPageValidatorsNeverOnPoolBusy(t *testing.T) {
	for _, purge := range []bool{false, true} {
		name := "hit"
		if purge {
			name = "fresh"
		}
		t.Run(name, func(t *testing.T) {
			f, etag := warmedFixture(t)
			if purge {
				f.cache.data = nil
			}
			if purge { // a menu or settings lookup gave up while the page was built
				build := f.h.buildView
				f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
					database.NotePoolBusy(ctx)
					return build(ctx, r, rr)
				}
			} else { // the redirect lookup gave up in front of the cached copy
				f.h.checkRedirect = func(ctx context.Context, _ string) *redirect.Redirect {
					database.NotePoolBusy(ctx)
					return nil
				}
			}
			for _, inm := range []string{"", etag} {
				h := busyConverter(http.HandlerFunc(f.h.handleRenderCatchAll))
				r := httptest.NewRequest(http.MethodGet, "/about", nil)
				r = r.WithContext(database.WithAcquireBound(r.Context(), time.Second))
				if inm != "" {
					r.Header.Set("If-None-Match", inm)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusServiceUnavailable || w.Header().Get("ETag") != "" || w.Header().Get("Last-Modified") != "" {
					t.Errorf("If-None-Match %q: status %d headers %v, want a bare 503", inm, w.Code, w.Header())
				}
			}
		})
	}
}

func TestPageRedirectWinsOverNotModified(t *testing.T) {
	f, etag := warmedFixture(t)
	f.redirect = &redirect.Redirect{TargetURL: "/elsewhere", StatusCode: 302}
	w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-None-Match": etag})
	if w.Code != 302 || w.Header().Get("ETag") != "" || w.Header().Get("Last-Modified") != "" {
		t.Fatalf("status %d headers %v, want a bare redirect", w.Code, w.Header())
	}
}

// A page whose theme emits data-hm-directory (a site theme's registry
// directory) gets the short shared-cache lifetime on a fresh 200 and on both
// 304 paths; other pages keep cachePublic.
func TestDirectoryPageValidators(t *testing.T) {
	engine, err := render.New(render.Options{Layers: []fs.FS{fstest.MapFS{
		"theme.json":        {Data: []byte(`{"name":"dir","version":"1.0.0"}`)},
		"layouts/base.html": {Data: []byte(`<!doctype html><main data-hm-directory>directory</main>`)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	f := newRenderFixture(t)
	f.h.engine = engine
	f.next = stablePage("Plugins")
	f.advance(2 * time.Second)

	first := f.get("/plugins")
	etag := first.Header().Get("ETag")
	if first.Code != 200 || !weakETagRE.MatchString(etag) || first.Header().Get("Cache-Control") != cachePublicDirectory {
		t.Fatalf("directory page: %d ETag=%q Cache-Control=%q", first.Code, etag, first.Header().Get("Cache-Control"))
	}
	for name, purge := range map[string]bool{"hit": false, "fresh": true} {
		if purge {
			f.cache.data = nil
		}
		w := f.conditionalGet(http.MethodGet, "/plugins", map[string]string{"If-None-Match": etag})
		if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != cachePublicDirectory {
			t.Errorf("%s: status %d Cache-Control=%q body=%d", name, w.Code, w.Header().Get("Cache-Control"), w.Body.Len())
		}
	}
	other := newRenderFixture(t)
	other.next = okPage("About")
	if cc := other.get("/about").Header().Get("Cache-Control"); cc != cachePublic {
		t.Errorf("other page Cache-Control = %q, want %q", cc, cachePublic)
	}
}

func TestDemoServerHasNoValidators(t *testing.T) {
	for _, demo := range []bool{false, true} {
		s := &Server{cfg: &config.Config{}}
		s.cfg.Demo.Enabled = demo
		if got := s.newRenderHandlers(nil, nil).validators != nil; got == demo {
			t.Errorf("demo=%v: validators present=%v", demo, got)
		}
	}
}
