package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// TestNotFoundStoreBytesMatchUncached: each path's cold and warm answers equal
// the uncached render; different paths never share bytes.
func TestNotFoundStoreBytesMatchUncached(t *testing.T) {
	full, s := bootThemed404(t)
	rh := s.renderHandlers
	store := rh.notFound
	if store == nil || rh.cache == nil {
		t.Fatal("route shell has no 404 store or page cache")
	}
	paths := []string{"/no-such-page-q7x2", "/wp-login.php", "/a/b/c", "/a/b/c?x=1"}
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		rh.notFound = newNotFoundCache() // each method starts cold
		store = rh.notFound
		for _, p := range paths {
			rh.notFound = nil
			want := serveDump(full, m, p) // uncached reference
			rh.notFound = store
			cold, warm := serveDump(full, m, p), serveDump(full, m, p)
			if cold != want || warm != want {
				t.Fatalf("%s %s: cold/warm differ from the uncached render\n--- want ---\n%.600s\n--- cold ---\n%.600s\n--- warm ---\n%.600s", m, p, want, cold, warm)
			}
		}
	}
	keys := map[string]bool{}
	for k := range store.m {
		keys[k[strings.IndexByte(k, 0)+1:]] = true
	}
	for _, p := range []string{"/no-such-page-q7x2", "/wp-login.php", "/a/b/c"} {
		if !keys[p] {
			t.Fatalf("no stored 404 for %s (keys %v)", p, keys)
		}
	}
	if keys["/a/b/c?x=1"] || len(keys) != 3 {
		t.Fatalf("a queried path was stored: %v", keys)
	}

	// Warm bytes are per path: never another path's canonical.
	a, b := serveDump(full, "GET", "/no-such-page-q7x2"), serveDump(full, "GET", "/wp-login.php")
	if a == b {
		t.Fatal("two paths got identical bytes")
	}
	for p, dump := range map[string]string{"/no-such-page-q7x2": a, "/wp-login.php": b} {
		if !strings.Contains(dump, "https://x.io"+p) {
			t.Fatalf("%s: warm body lacks its own canonical", p)
		}
	}
	other := map[string]string{"/no-such-page-q7x2": "/wp-login.php", "/wp-login.php": "/no-such-page-q7x2"}
	for p, dump := range map[string]string{"/no-such-page-q7x2": a, "/wp-login.php": b} {
		if strings.Contains(dump, "https://x.io"+other[p]) {
			t.Fatalf("%s: warm body carries %s", p, other[p])
		}
	}

	// Accept-Encoding does not change the stored bytes.
	gz := func() string {
		req := httptest.NewRequest(http.MethodGet, "/a/b/c", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		full.ServeHTTP(rec, req)
		return dumpResponse(rec)
	}
	rh.notFound = nil
	wantGz := gz()
	rh.notFound = store
	gz()
	if got := gz(); got != wantGz {
		t.Fatalf("gzip warm differs from uncached\n--- want ---\n%.400s\n--- got ---\n%.400s", wantGz, got)
	}
}

// TestNotFoundStoreReflectsPurge: a chrome change shows after a page-cache purge.
func TestNotFoundStoreReflectsPurge(t *testing.T) {
	full, s := bootThemed404(t)
	name := "Before Site"
	s.renderHandlers.buildView = func(_ context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v := pageViewFromRoute(rr, "https://x.io", requestPath(r))
		v.Site = render.SiteView{Name: name}
		return v, nil
	}
	if got := serveDump(full, "GET", "/gone"); !strings.Contains(got, "Before Site") {
		t.Fatalf("first 404 lacks the site name:\n%.400s", got)
	}
	name = "After Site"
	if got := serveDump(full, "GET", "/gone"); !strings.Contains(got, "Before Site") {
		t.Fatalf("second 404 was rebuilt, not reused:\n%.400s", got)
	}
	s.purgeRendered(context.Background())
	if got := serveDump(full, "GET", "/gone"); !strings.Contains(got, "After Site") {
		t.Fatalf("404 after a purge is stale:\n%.400s", got)
	}
}

// TestNotFoundStoreLogsEveryHit: log404 runs for each 404, hits included.
func TestNotFoundStoreLogsEveryHit(t *testing.T) {
	f := newRenderFixture(t)
	f.h.notFound = newNotFoundCache()
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	serve := func(hdr ...string) {
		req := httptest.NewRequest(http.MethodGet, "/nope", nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		rec := httptest.NewRecorder()
		f.h.serve(rec, req, "/nope", nil, f.next)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d", rec.Code)
		}
	}
	serve()
	serve()
	if len(f.notFound) != 2 || f.notFound[0] != "/nope" || f.notFound[1] != "/nope" {
		t.Fatalf("404 log = %v, want two entries", f.notFound)
	}
	if len(f.h.notFound.m) != 1 {
		t.Fatalf("store holds %d entries, want 1", len(f.h.notFound.m))
	}
	serve("Sec-Purpose", "prefetch")
	if len(f.notFound) != 2 {
		t.Fatalf("prefetch was logged: %v", f.notFound)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("404 body reached the page cache: %v", f.cache.sets)
	}
}

// TestNotFoundStoreBypass: none of these reads or fills the store. Cases that
// are decided before the render start with a sentinel body stored under the
// request's key, which must not be served; the others (decided from what the
// render produced) must leave the store empty.

func TestNotFoundStoreBypass(t *testing.T) {
	long := "/" + strings.Repeat("a", maxNotFoundPath)
	cases := []struct {
		name   string
		method string
		target string
		ctx    func(context.Context) context.Context
		req    func(*http.Request)
		setup  func(f *renderFixture)
		// afterRender: decided from the render, so no sentinel can be seeded.
		afterRender bool
		// wantStatus -1: the response status is not checked.
		wantStatus int
	}{
		{name: "query", target: "/nope?x=1"},
		{name: "post", method: http.MethodPost, target: "/nope"},
		{name: "uncached handler", target: "/nope", setup: func(f *renderFixture) { f.h.uncached = true }},
		{name: "uncached request", target: "/nope", ctx: withUncachedRequest},
		{name: "smoke", target: "/nope", ctx: smokeContext},
		{name: "refresh", target: "/nope", ctx: withRefresh},
		{name: "hidden locales", target: "/nope", ctx: withHiddenLocalesServed},
		{name: "base url not pinned", target: "/nope", setup: func(f *renderFixture) { f.h.baseURLPinned = false }},
		{name: "cache off", target: "/nope", setup: func(f *renderFixture) {
			f.h.runtime = &pageCacheRuntime{}
			f.h.runtime.enabled.Store(false)
		}},
		{name: "no page cache", target: "/nope", setup: func(f *renderFixture) { f.h.cache = nil }},
		{name: "view build error", target: "/nope", afterRender: true, setup: func(f *renderFixture) {
			f.h.buildView = func(context.Context, *http.Request, *routeResult) (*render.PageView, error) {
				return nil, context.DeadlineExceeded
			}
		}},
		{name: "uncacheable view", target: "/nope", afterRender: true, setup: func(f *renderFixture) {
			base := f.h.buildView
			f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
				v, err := base(ctx, r, rr)
				u := true
				v.Uncacheable = &u
				return v, err
			}
		}},
		{name: "degraded render", target: "/nope", afterRender: true, setup: func(f *renderFixture) {
			base := f.h.buildView
			f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
				render.NoteDegraded()
				return base(ctx, r, rr)
			}
		}},
		{name: "data failure", target: "/nope", afterRender: true, setup: func(f *renderFixture) {
			var n int64
			f.h.dataFailures = func() int64 { n++; return n }
		}},
		{name: "long path", target: long},
		{name: "degraded snapshot", target: "/nope", afterRender: true, setup: func(f *renderFixture) {
			f.h.snapshot = func(context.Context) (*entity.URLSnapshot, error) { return nil, errors.New("db down") }
		}},
		{name: "page filter", target: "/members", setup: func(f *renderFixture) {
			_, s := bootRoutes(t)
			capaRuntime(t, s)
			f.h.pageFilter = pluginhost.NewPageFilter(s.plugins, s.hooks)
		}},
		// A 404 under a prefix renders afresh each request but never calls the
		// plugin (404s do not run applyPageFilter).
		{name: "page prefix", target: "/item/nope", setup: func(f *renderFixture) {
			_, s := bootRoutes(t)
			rt := capaRuntimeWith(t, s, func(m *sdk.Manifest) { m.PagePathPrefixes = []string{"/item/"} })
			f.h.pageFilter = pluginhost.NewPageFilter(rt, s.hooks)
		}},
		{name: "comment bypass", target: "/nope", setup: func(f *renderFixture) {
			key := identity.DeviceKey("bypass-secret")
			f.h.commentBypass = newCommentBypass(
				func(val string, now time.Time) (identity.DeviceClaims, bool) {
					return identity.VerifyDevice(key, val, now)
				},
				func(context.Context, uuid.UUID) (bool, error) { return true, nil },
				nil)
		}, req: func(r *http.Request) {
			val := identity.SignDevice(identity.DeviceKey("bypass-secret"), identity.DeviceClaims{CommenterID: uuid.New(), TokenVersion: 1, Expires: time.Now().Add(time.Hour)})
			r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: val})
		}},
		{name: "busy pool", target: "/nope", afterRender: true, wantStatus: -1, // the caller maps a busy pool to 503
			ctx: func(ctx context.Context) context.Context {
				return database.WithPoolBusyScope(database.WithAcquireBound(ctx, time.Second))
			},
			setup: func(f *renderFixture) {
				base := f.h.buildView
				f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
					database.NotePoolBusy(ctx)
					return base(ctx, r, rr)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRenderFixture(t)
			f.h.notFound = newNotFoundCache()
			if tc.setup != nil {
				tc.setup(f)
			}
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			seeded := 0
			if !tc.afterRender {
				req := httptest.NewRequest(method, tc.target, nil)
				key := f.h.engine.Version() + "\x00" + requestPath(req)
				f.h.notFound.put(key, []byte("STORED-SENTINEL"), f.cache.Generation(), time.Now().Add(time.Hour))
				seeded = 1
			}
			for i := 0; i < 2; i++ {
				req := httptest.NewRequest(method, tc.target, nil)
				if tc.req != nil {
					tc.req(req)
				}
				if tc.ctx != nil {
					req = req.WithContext(tc.ctx(req.Context()))
				}
				rec := httptest.NewRecorder()
				f.h.writeNotFoundQuiet(rec, req)
				if tc.wantStatus >= 0 && rec.Code != http.StatusNotFound {
					t.Fatalf("status %d", rec.Code)
				}
				if strings.Contains(rec.Body.String(), "STORED-SENTINEL") {
					t.Fatal("the stored body was served to a bypassing request")
				}
			}
			if n := len(f.h.notFound.m); n != seeded {
				t.Fatalf("store holds %d entries, want %d", n, seeded)
			}
		})
	}
}

// TestNotFoundStoreSkipsDegradedSnapshot: a body stored from a healthy
// snapshot stays readable while the URL snapshot cannot be read, but a body
// built meanwhile is never stored (a recovered snapshot would not purge a
// frozen wrong-locale body).
func TestNotFoundStoreSkipsDegradedSnapshot(t *testing.T) {
	f := newRenderFixture(t)
	f.h.notFound = newNotFoundCache()
	f.h.snapshot = func(context.Context) (*entity.URLSnapshot, error) { return nil, errors.New("db down") }
	key := f.h.engine.Version() + "\x00/nope"

	rec := httptest.NewRecorder()
	f.h.writeNotFoundQuiet(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if n := len(f.h.notFound.m); n != 0 {
		t.Fatalf("a body built from a degraded snapshot was stored (%d entries)", n)
	}

	f.h.notFound.put(key, []byte("STORED-SENTINEL"), f.cache.Generation(), time.Now().Add(time.Hour))
	rec = httptest.NewRecorder()
	f.h.writeNotFoundQuiet(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if !strings.Contains(rec.Body.String(), "STORED-SENTINEL") {
		t.Fatal("a stored body was not served while the snapshot is degraded")
	}
}
