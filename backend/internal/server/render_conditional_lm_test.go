package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/cache"
)

func httpDate(t time.Time) string { return t.UTC().Format(http.TimeFormat) }

func TestPageLastModifiedStableAcrossPurge(t *testing.T) {
	f := newRenderFixture(t)
	f.next = stablePage("About")
	f.advance(2 * time.Second)
	first := f.get("/about")
	lm := first.Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("no Last-Modified")
	}
	f.advance(time.Hour)
	f.cache.data = nil // purge: the next request re-renders the same bytes
	second := f.get("/about")
	if f.resolves != 2 {
		t.Fatalf("resolves = %d, want a re-render", f.resolves)
	}
	if got := second.Header().Get("Last-Modified"); got != lm {
		t.Errorf("Last-Modified moved on identical bytes: %q -> %q", lm, got)
	}
	if second.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Error("ETag moved on identical bytes")
	}
}

func TestPageLastModifiedMovesOnChange(t *testing.T) {
	f := newRenderFixture(t)
	f.next = stablePage("About")
	f.advance(2 * time.Second)
	first := f.get("/about")
	lm1 := f.now // whole second: the clock has no sub-second part
	if first.Header().Get("Last-Modified") != httpDate(lm1) {
		t.Fatalf("Last-Modified = %q, want %q", first.Header().Get("Last-Modified"), httpDate(lm1))
	}

	f.next = stablePage("Other")
	f.cache.data = nil
	same := f.get("/about")
	if same.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatal("changed bytes kept the ETag")
	}
	if got := same.Header().Get("Last-Modified"); got != "" {
		t.Fatalf("a change within the same second must not send Last-Modified yet (it would be in the future), got %q", got)
	}

	f.advance(time.Second)
	lm2 := lm1.Add(time.Second)
	if got := f.get("/about").Header().Get("Last-Modified"); got != httpDate(lm2) {
		t.Fatalf("Last-Modified = %q, want strictly later %q", got, httpDate(lm2))
	}
	if w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-Modified-Since": httpDate(lm1)}); w.Code != 200 {
		t.Errorf("If-Modified-Since the old date = %d, want 200 (bytes changed)", w.Code)
	}
	if w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-Modified-Since": httpDate(lm2)}); w.Code != 304 {
		t.Errorf("If-Modified-Since the new date = %d, want 304", w.Code)
	}
}

func TestPageIfModifiedSince(t *testing.T) {
	f := newRenderFixture(t)
	f.next = stablePage("About")

	// Right after construction the time is not yet past the floor: no
	// Last-Modified, and If-Modified-Since is not honoured.
	future := httpDate(f.now.Add(24 * time.Hour))
	for i := 0; i < 2; i++ { // fresh, then hit
		w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-Modified-Since": future})
		if w.Code != 200 || w.Header().Get("Last-Modified") != "" {
			t.Fatalf("request %d: status %d Last-Modified=%q", i, w.Code, w.Header().Get("Last-Modified"))
		}
	}

	f.advance(5 * time.Second)
	lm := f.get("/about").Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("no Last-Modified once the clock moved on")
	}
	since, err := http.ParseTime(lm)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ims  string
		want int
	}{
		{"equal", httpDate(since), 304},
		{"later", httpDate(since.Add(time.Hour)), 304},
		{"one second earlier", httpDate(since.Add(-time.Second)), 200},
		{"malformed", "last tuesday", 200},
	}
	for _, tc := range cases {
		if w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-Modified-Since": tc.ims}); w.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, w.Code, tc.want)
		}
	}
}

// A reset of the validator map must not let a changed page reuse a second
// already sent for that key.
func TestPageLastModifiedAfterReset(t *testing.T) {
	f := newRenderFixture(t)
	f.h.validators.max = 1
	f.next = stablePage("About")
	f.advance(2 * time.Second)
	lmA := f.get("/a").Header().Get("Last-Modified")
	f.get("/b") // the map is full: it starts over

	f.next = stablePage("Changed")
	f.cache.data = nil
	f.get("/a")
	f.advance(time.Second)
	got := f.get("/a").Header().Get("Last-Modified")
	if got == "" {
		t.Fatal("no Last-Modified after the clock moved on")
	}
	before, _ := http.ParseTime(lmA)
	after, err := http.ParseTime(got)
	if err != nil || !after.After(before) {
		t.Fatalf("Last-Modified %q is not strictly later than %q", got, lmA)
	}
}

func TestPageNotModifiedHeaders(t *testing.T) {
	f, _ := variantFixture(t, true)
	f.advance(2 * time.Second)
	_ = f.get("/about", withAE("gzip"))
	warm := f.get("/about", withAE("gzip")) // hit: builds the gzip variant
	if warm.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("setup: the hit should be served compressed")
	}
	if !strings.Contains(gunzip(t, warm.Body.Bytes()), "About us") {
		t.Fatal("the stored variant does not hold the page")
	}
	etag := warm.Header().Get("ETag")
	lm := warm.Header().Get("Last-Modified")
	if !weakETagRE.MatchString(etag) || lm == "" {
		t.Fatalf("compressed hit lacks validators: %q %q", etag, lm)
	}

	w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-None-Match": etag, "Accept-Encoding": "gzip"})
	hd := w.Header()
	if w.Code != 304 || w.Body.Len() != 0 {
		t.Fatalf("status %d, body %d bytes", w.Code, w.Body.Len())
	}
	for _, name := range []string{"Content-Length", "Content-Encoding", "Content-Type"} {
		if hd.Get(name) != "" {
			t.Errorf("304 carries %s: %q", name, hd.Get(name))
		}
	}
	if hd.Get("Cache-Control") != cachePublic || hd.Get("ETag") != etag || hd.Get("Last-Modified") != lm {
		t.Errorf("Cache-Control=%q ETag=%q Last-Modified=%q", hd.Get("Cache-Control"), hd.Get("ETag"), hd.Get("Last-Modified"))
	}
	if !slices.Equal(hd.Values("Vary"), warm.Header().Values("Vary")) {
		t.Errorf("304 Vary = %v, 200 Vary = %v", hd.Values("Vary"), warm.Header().Values("Vary"))
	}
	if hd.Get("Content-Security-Policy") == "" && hd.Get("Content-Security-Policy-Report-Only") == "" {
		t.Error("304 lacks the CSP header")
	}
}

// A cacheable 200 must not lose Last-Modified because the clock moved between
// two reads: each request reads it exactly once.
func TestPageLastModifiedOneClockRead(t *testing.T) {
	f := newRenderFixture(t)
	f.next = stablePage("About")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	calls := 0
	f.h.validators = newPageValidators(func() time.Time {
		n := calls
		calls++
		return base.Add(time.Duration(n) * time.Second)
	})
	for _, step := range []string{"render", "cache hit"} {
		before := calls
		w := f.get("/about")
		if w.Code != 200 {
			t.Fatalf("%s: status %d", step, w.Code)
		}
		if got := calls - before; got != 1 {
			t.Errorf("%s: %d clock reads, want 1", step, got)
		}
		if w.Header().Get("Last-Modified") == "" {
			t.Errorf("%s: Last-Modified missing", step)
		}
	}
}

func TestPageNotModifiedNoVaryWhenCompressionOff(t *testing.T) {
	plain := func(t *testing.T) *renderFixture {
		f := newRenderFixture(t) // encodeCached nil
		f.next = okPage(strings.Repeat("About us and more words. ", 120))
		return f
	}
	off := func(t *testing.T) *renderFixture { f, _ := variantFixture(t, false); return f }
	for name, mk := range map[string]func(*testing.T) *renderFixture{"nil": plain, "false": off} {
		t.Run(name, func(t *testing.T) {
			f := mk(t)
			f.advance(2 * time.Second)
			ok := f.get("/about", withAE("gzip"))
			etag := ok.Header().Get("ETag")
			if etag == "" {
				t.Fatal("setup: no ETag")
			}
			w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-None-Match": etag, "Accept-Encoding": "gzip"})
			if w.Code != 304 {
				t.Fatalf("status %d", w.Code)
			}
			if len(w.Header().Values("Vary")) != 0 || !slices.Equal(w.Header().Values("Vary"), ok.Header().Values("Vary")) {
				t.Errorf("304 Vary = %v, 200 Vary = %v", w.Header().Values("Vary"), ok.Header().Values("Vary"))
			}
		})
	}
}

func TestPageNotModifiedSkipsVariant(t *testing.T) {
	f, c := variantFixture(t, true)
	f.advance(2 * time.Second)
	etag := f.get("/about").Header().Get("ETag") // no Accept-Encoding: no variant stored
	if etag == "" {
		t.Fatal("no ETag")
	}
	w := f.conditionalGet(http.MethodGet, "/about", map[string]string{"If-None-Match": etag, "Accept-Encoding": "gzip"})
	if w.Code != 304 {
		t.Fatalf("status %d", w.Code)
	}
	key := cache.RenderedPageKey(f.h.engine.Version(), "/about")
	if v, _ := c.Get(context.Background(), cache.EncodedKey(key, "gzip")); v != nil {
		t.Fatal("a 304 built a compressed variant")
	}
	// A plain 200 afterwards still builds it, with the same ETag.
	w = f.get("/about", withAE("gzip"))
	if w.Header().Get("ETag") != etag || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("200 after 304: ETag=%q Content-Encoding=%q", w.Header().Get("ETag"), w.Header().Get("Content-Encoding"))
	}
}
