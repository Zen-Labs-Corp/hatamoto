package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/render"
)

// formEngine renders a page that carries the comment form placeholders.
type formEngine struct {
	pageRenderer
	entity uuid.UUID
}

func (e formEngine) RenderPage(w io.Writer, _ *render.PageView) error {
	_, err := io.WriteString(w, `<html><body><form method="post" action="/-/comments/`+e.entity.String()+`" data-hm-cform>`+
		`<input type="hidden" name="hm_ct" value="__hmcf_tok__"><textarea name="__hmcf_body__"></textarea>`+
		`<input name="__hmcf_name__"><input name="__hmcf_email__"><input name="__hmcf_hp__"></form></body></html>`)
	return err
}

type bypassFixture struct {
	*renderFixture
	key    []byte
	hasOwn atomic.Int32 // lookups
	own    bool
	err    error
}

func newBypassFixture(t *testing.T) *bypassFixture {
	t.Helper()
	f := &bypassFixture{renderFixture: newRenderFixture(t), key: identity.DeviceKey("bypass-secret")}
	f.h.engine = formEngine{pageRenderer: f.h.engine, entity: uuid.New()}
	slot, _ := slotFixture(true)
	f.h.slot = slot
	f.h.commentBypass = newCommentBypass(
		func(val string, now time.Time) (identity.DeviceClaims, bool) {
			return identity.VerifyDevice(f.key, val, now)
		},
		func(context.Context, uuid.UUID) (bool, error) { f.hasOwn.Add(1); return f.own, f.err },
		nil)
	f.next = okPage("Go")
	return f
}

func (f *bypassFixture) cookie(key []byte) func(*http.Request) {
	val := identity.SignDevice(key, identity.DeviceClaims{CommenterID: uuid.New(), TokenVersion: 1, Expires: time.Now().Add(time.Hour)})
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: val}) }
}

func TestCommentBypassOwnItemsSkipCache(t *testing.T) {
	f := newBypassFixture(t)
	f.own = true
	f.get("/go") // primes the cache
	if len(f.cache.sets) != 1 {
		t.Fatalf("sets = %v", f.cache.sets)
	}
	before := f.resolves
	w := f.get("/go", f.cookie(f.key))
	if f.resolves != before+1 {
		t.Fatalf("resolves = %d, want %d (cache bypassed)", f.resolves, before+1)
	}
	if len(f.cache.sets) != 1 {
		t.Fatalf("bypass render was stored: %v", f.cache.sets)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "private") {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if strings.Contains(w.Body.String(), "__hmcf_") {
		t.Fatal("placeholders left in a bypass render")
	}
}

func TestCommentBypassForgedCookieUsesCache(t *testing.T) {
	f := newBypassFixture(t)
	f.own = true
	f.get("/go")
	before := f.resolves
	f.get("/go", f.cookie([]byte("some other key")))
	if f.resolves != before {
		t.Fatal("forged cookie bypassed the cache")
	}
	if f.hasOwn.Load() != 0 {
		t.Fatal("a forged cookie reached the database lookup")
	}
}

func TestCommentBypassNoOwnItemsUsesCache(t *testing.T) {
	f := newBypassFixture(t)
	f.get("/go")
	before := f.resolves
	c := f.cookie(f.key)
	f.get("/go", c)
	if f.resolves != before {
		t.Fatal("visitor without non-public comments bypassed the cache")
	}
	f.get("/go", c) // a new commenter id per cookie: no assertion on the count here
	f.err = errors.New("db down")
	f.own = true
	f.get("/go", f.cookie(f.key))
	if f.resolves != before {
		t.Fatal("a lookup error must fall back to the shared page")
	}
}

func TestCommentBypassNoCookieNoLookup(t *testing.T) {
	f := newBypassFixture(t)
	f.own = true
	f.get("/go")
	f.get("/go")
	if f.hasOwn.Load() != 0 {
		t.Fatal("lookup without a cookie")
	}
	var nilB *commentBypass
	if nilB.applies(httpGet("/go")) {
		t.Fatal("nil bypass applied")
	}
}

func TestCommentBypassOwnCacheTTL(t *testing.T) {
	now := time.Now()
	n := 0
	b := newCommentBypass(
		func(string, time.Time) (identity.DeviceClaims, bool) {
			return identity.DeviceClaims{CommenterID: uuid.Nil}, true
		},
		func(context.Context, uuid.UUID) (bool, error) { n++; return true, nil },
		func() time.Time { return now })
	r := httpGet("/go")
	r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "x"})
	for range 3 {
		if !b.applies(r) {
			t.Fatal("not applied")
		}
	}
	if n != 1 {
		t.Fatalf("lookups = %d, want 1 within the TTL", n)
	}
	now = now.Add(ownCacheTTL + time.Second)
	b.applies(r)
	if n != 2 {
		t.Fatalf("lookups = %d, want 2 after the TTL", n)
	}
	b.forget(uuid.Nil)
	b.applies(r)
	if n != 3 {
		t.Fatalf("lookups = %d, want 3 after forget", n)
	}
}

func TestCommentSlotServedFromCacheHit(t *testing.T) {
	f := newBypassFixture(t)
	f.get("/go")
	if len(f.cache.sets) != 1 {
		t.Fatalf("sets = %v", f.cache.sets)
	}
	for k, v := range f.cache.data {
		if !strings.Contains(string(v), "__hmcf_tok__") {
			t.Fatalf("stored %s is not the template", k)
		}
	}
	gzip := func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") }
	w := f.get("/go", gzip)
	if f.resolves != 1 {
		t.Fatalf("resolves = %d, want a cache hit", f.resolves)
	}
	if strings.Contains(w.Body.String(), "__hmcf_") {
		t.Fatal("placeholders left in a hit")
	}
	h := w.Header()
	if !strings.Contains(h.Get("Cache-Control"), "private") {
		t.Fatalf("Cache-Control = %q", h.Get("Cache-Control"))
	}
	if h.Get("ETag") != "" || h.Get("Last-Modified") != "" || h.Get("Content-Encoding") != "" {
		t.Fatalf("validators or encoding on a personalised page: %v", h)
	}
	etag := f.get("/go").Header().Get("ETag")
	w = f.get("/go", func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("conditional request on a marker page = %d, want 200 with a body", w.Code)
	}
}

func TestCommentSlotFlightFollowerFillsOwnBody(t *testing.T) {
	const n = 4
	f := newBypassFixture(t)
	key := cache.RenderedPageKey(f.h.engine.Version(), "/go")
	f.next = func() (*routeResult, pageOutcome, error) {
		flightWaitFor(t, "followers to park", func() bool { return f.h.flight.waiting(key) == n-1 })
		return okPage("Go")()
	}
	bodies := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bodies[i] = f.get("/go").Body.String()
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, b := range bodies {
		if strings.Contains(b, "__hmcf_") {
			t.Fatalf("body %d has placeholders", i)
		}
		if seen[b] {
			t.Fatalf("body %d equals another response: followers must fill their own token", i)
		}
		seen[b] = true
	}
	if f.resolves != 1 {
		t.Fatalf("resolves = %d, want 1", f.resolves)
	}
}

func httpGet(path string) *http.Request { return httptest.NewRequest(http.MethodGet, path, nil) }

type fakeShowDead struct {
	flagged []string
	shown   map[string]string // plugin -> the key that carries the flag
	err     error
	asked   map[string]string
}

func (f *fakeShowDead) FlaggedPlugins(context.Context) ([]string, error) { return f.flagged, f.err }
func (f *fakeShowDead) ShowDeadPlugins(_ context.Context, keys map[string]string) ([]string, error) {
	f.asked = keys
	var out []string
	for p, k := range keys {
		if f.shown[p] == k {
			out = append(out, p)
		}
	}
	return out, f.err
}

func TestCommentBypassGrantedShowDeadFlag(t *testing.T) {
	const secret = "bypass-secret"
	cid := uuid.New()
	granted := func(p string) bool { return p == "example" }
	flags := &fakeShowDead{
		flagged: []string{"example", "stranger"},
		shown: map[string]string{
			"example":  comment.PluginCommenterKey(secret, "example", cid),
			"stranger": comment.PluginCommenterKey(secret, "stranger", cid),
		},
	}
	ctx := context.Background()
	if has, err := anyShowDead(ctx, flags, granted, secret, cid); err != nil || !has {
		t.Fatalf("a granted plugin's flag = %v, %v; want the bypass", has, err)
	}
	if _, asked := flags.asked["stranger"]; asked {
		t.Fatal("a plugin without comments.moderate was consulted")
	}
	if has, _ := anyShowDead(ctx, flags, granted, secret, uuid.New()); has {
		t.Fatal("another commenter has no flag")
	}
	if has, _ := anyShowDead(ctx, flags, func(string) bool { return false }, secret, cid); has {
		t.Fatal("a flag of a plugin without the grant counts")
	}
	if has, err := anyShowDead(ctx, &fakeShowDead{err: errors.New("db")}, granted, secret, cid); has || err == nil {
		t.Fatalf("a lookup error = %v, %v; want an error and no bypass", has, err)
	}
	if !viewerShowsDead(ctx, flags, granted, secret, "example", cid) || viewerShowsDead(ctx, flags, granted, secret, "stranger", cid) {
		t.Fatal("viewerShowsDead must follow the flag and the grant")
	}
	if viewerShowsDead(ctx, &fakeShowDead{err: errors.New("db")}, granted, secret, "example", cid) {
		t.Fatal("a lookup error must not reveal")
	}

	// Wired as the bypass: the flagged commenter's request applies, others' do not.
	key := identity.DeviceKey(secret)
	b := newCommentBypass(
		func(val string, now time.Time) (identity.DeviceClaims, bool) {
			return identity.VerifyDevice(key, val, now)
		},
		func(ctx context.Context, id uuid.UUID) (bool, error) {
			return anyShowDead(ctx, flags, granted, secret, id)
		}, nil)
	req := func(id uuid.UUID) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/post", nil)
		r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: identity.SignDevice(key, identity.DeviceClaims{CommenterID: id, TokenVersion: 1, Expires: time.Now().Add(time.Hour)})})
		return r
	}
	if !b.applies(req(cid)) || b.applies(req(uuid.New())) {
		t.Fatal("the bypass must apply to the flagged commenter only")
	}
}
