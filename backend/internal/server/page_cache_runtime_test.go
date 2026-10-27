package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
)

func TestResolveTTLPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		admin   int
		def     time.Duration
		want    time.Duration
		wantSrc string
	}{
		{"unset uses built-in default", 0, config.DefaultPageCacheTTL, 5 * time.Minute, ttlSourceServer},
		{"admin wins", 3600, config.DefaultPageCacheTTL, time.Hour, ttlSourceAdmin},
		{"unset uses env default", 0, 10 * time.Minute, 10 * time.Minute, ttlSourceServer},
		{"admin beats env", 1800, 10 * time.Minute, 30 * time.Minute, ttlSourceAdmin},
	}
	for _, c := range cases {
		got, src := resolveTTL(c.admin, c.def)
		if got != c.want || src != c.wantSrc {
			t.Errorf("%s: got %v/%s, want %v/%s", c.name, got, src, c.want, c.wantSrc)
		}
	}
}

func TestPageCacheRuntimeNilIsEnabledWithDefaults(t *testing.T) {
	var r *pageCacheRuntime
	if !r.Enabled() || r.TTL() != 0 {
		t.Fatalf("nil runtime: enabled=%v ttl=%v", r.Enabled(), r.TTL())
	}
	r.Apply(false, time.Hour) // must not panic
}

func TestPageCacheRuntimeApply(t *testing.T) {
	r := newPageCacheRuntime()
	if !r.Enabled() || r.TTL() != 0 {
		t.Fatalf("new runtime: enabled=%v ttl=%v", r.Enabled(), r.TTL())
	}
	r.Apply(false, time.Hour)
	if r.Enabled() || r.TTL() != time.Hour {
		t.Fatalf("after Apply: enabled=%v ttl=%v", r.Enabled(), r.TTL())
	}
}

func TestRefreshFlag(t *testing.T) {
	if refreshRequested(context.Background()) {
		t.Fatal("plain context must not request a refresh")
	}
	if !refreshRequested(withRefresh(context.Background())) {
		t.Fatal("withRefresh context must request a refresh")
	}
}

func TestPageCacheOffBypassesReadAndWrite(t *testing.T) {
	f := newRenderFixture(t)
	rr := &routeResult{Page: pageResponse("About"), Kind: resolveKindEntity} // fixed entity id: output is comparable
	f.next = func() (*routeResult, pageOutcome, error) { return rr, pageOutcome{Status: 200}, nil }
	f.h.runtime = newPageCacheRuntime()
	on := f.get("/about").Body.String()
	if len(f.cache.sets) != 1 {
		t.Fatalf("On should write once, sets=%v", f.cache.sets)
	}

	f.h.runtime.Apply(false, 0)
	before := f.resolves
	off := f.get("/about").Body.String()
	if f.resolves != before+1 {
		t.Fatalf("Off must not be served from cache (resolves %d -> %d)", before, f.resolves)
	}
	if len(f.cache.sets) != 1 {
		t.Fatalf("Off must not write, sets=%v", f.cache.sets)
	}
	if on != off {
		t.Fatal("body differs between cache On and Off")
	}

	f.h.runtime.Apply(true, 0)
	f.get("/about") // cached entry from the On run is served
	if f.resolves != before+1 {
		t.Fatalf("On again should read the cache (resolves %d)", f.resolves)
	}
	f.cache.data = nil
	f.get("/about")
	if len(f.cache.sets) != 2 {
		t.Fatalf("On again should write, sets=%v", f.cache.sets)
	}
}

func TestPageCacheLiveTTL(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.h.runtime = newPageCacheRuntime()
	f.h.cacheTTL = 10 * time.Minute

	f.get("/a")
	f.h.runtime.Apply(true, time.Hour)
	f.get("/b")
	if len(f.cache.ttls) != 2 || f.cache.ttls[0] != 10*time.Minute || f.cache.ttls[1] != time.Hour {
		t.Fatalf("ttls = %v, want [10m 1h]", f.cache.ttls)
	}
}

func TestPageCacheRefreshOverwrites(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.get("/about")
	key := f.cache.sets[0]
	f.cache.data[key] = []byte("stale")

	if w := f.get("/about"); w.Body.String() != "stale" {
		t.Fatalf("plain GET should serve the cached entry, got %q", w.Body.String())
	}
	resolves := f.resolves
	f.get("/about", func(r *http.Request) { *r = *r.WithContext(withRefresh(r.Context())) })
	if f.resolves != resolves+1 {
		t.Fatal("refresh must re-render")
	}
	if len(f.cache.sets) != 2 || string(f.cache.data[key]) == "stale" {
		t.Fatalf("refresh must overwrite the entry, sets=%v", f.cache.sets)
	}
}
