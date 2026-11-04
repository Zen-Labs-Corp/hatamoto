package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/httpx"
)

// variantFixture is a render fixture whose page is large enough to be
// compressed and whose cache is the real one (purges, generations, stats).
func variantFixture(t *testing.T, on bool) (*renderFixture, *cache.Cache) {
	t.Helper()
	f := newRenderFixture(t)
	f.next = okPage(strings.Repeat("About us and more words. ", 120))
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.h.cache = c
	f.h.cacheTTL = time.Hour
	f.h.encodeCached = func() bool { return on }
	return f, c
}

func withAE(ae string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Accept-Encoding", ae) }
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(gr)
	return string(out)
}

func TestCachedVariantServedWithoutRecompression(t *testing.T) {
	f, c := variantFixture(t, true)
	ctx := context.Background()
	_ = f.get("/about", withAE("zstd")) // miss: renders, stores plain only
	if f.resolves != 1 {
		t.Fatalf("resolves = %d", f.resolves)
	}
	plainKey := cache.RenderedPageKey(f.h.engine.Version(), "/about")
	zKey := cache.EncodedKey(plainKey, "zstd")
	if v, _ := c.Get(ctx, zKey); v != nil {
		t.Fatal("a miss stores the plain page only; the variant is built on the first hit")
	}

	first := f.get("/about", withAE("zstd")) // first hit: compress + store
	if first.Header().Get("Content-Encoding") != "zstd" || !strings.Contains(first.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("headers: %v", first.Header())
	}
	stored := storedVariant(t, c, zKey)
	if len(stored) == 0 || !bytes.Equal(stored, first.Body.Bytes()) {
		t.Fatal("served bytes are not the stored variant")
	}
	if first.Header().Get("Content-Length") == "" {
		t.Error("Content-Length missing")
	}
	zr, _ := zstd.NewReader(bytes.NewReader(stored))
	plain, _ := zr.DecodeAll(stored, nil)
	if !strings.Contains(string(plain), "About us") {
		t.Fatal("variant does not decode to the page")
	}

	// Second hit: the same stored slice, so the encoder did not run again.
	// Overwrite the stored variant with a marker body: if the hit path
	// compressed again it would not be served.
	marker, _ := httpx.CompressBytes(httpx.EncodingZstd, []byte(strings.Repeat("marker ", 300)), true)
	raw, _ := c.Get(ctx, zKey)
	_, _ = c.SetIfGeneration(ctx, zKey, append(raw[:fingerprintLen:fingerprintLen], marker...), time.Hour, c.Generation())
	second := f.get("/about", withAE("zstd"))
	if !bytes.Equal(second.Body.Bytes(), marker) {
		t.Fatal("a variant hit must serve the stored bytes untouched")
	}
	if f.resolves != 1 {
		t.Fatalf("hits must not re-render (resolves=%d)", f.resolves)
	}
}

func TestCachedVariantsPerCoding(t *testing.T) {
	f, _ := variantFixture(t, true)
	_ = f.get("/about")
	z := f.get("/about", withAE("zstd, gzip"))
	g := f.get("/about", withAE("gzip"))
	p := f.get("/about")
	if z.Header().Get("Content-Encoding") != "zstd" || g.Header().Get("Content-Encoding") != "gzip" || p.Header().Get("Content-Encoding") != "" {
		t.Fatalf("codings: %q %q %q", z.Header().Get("Content-Encoding"), g.Header().Get("Content-Encoding"), p.Header().Get("Content-Encoding"))
	}
	if gunzip(t, g.Body.Bytes()) != p.Body.String() {
		t.Fatal("gzip variant differs from the plain page")
	}
	if p.Header().Get("Content-Length") != "" && p.Header().Get("Vary") != "" {
		t.Errorf("plain hit must not claim a variant: %v", p.Header())
	}
}

func TestCachedVariantHead(t *testing.T) {
	f, _ := variantFixture(t, true)
	_ = f.get("/about")
	r := httptest.NewRequest(http.MethodHead, "/about", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	f.h.handleRenderCatchAll(w, r)
	if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Content-Length") == "" || w.Body.Len() != 0 {
		t.Fatalf("HEAD: %v body=%d", w.Header(), w.Body.Len())
	}
}

func TestInvalidateRenderedDropsVariants(t *testing.T) {
	f, c := variantFixture(t, true)
	ctx := context.Background()
	_ = f.get("/about")
	_ = f.get("/about", withAE("zstd"))
	_ = f.get("/about", withAE("gzip"))
	if pages, _ := c.RenderStats(); pages != 1 {
		t.Fatalf("pages = %d, variants must not count as pages", pages)
	}
	if _, b := c.RenderStats(); b == 0 {
		t.Fatal("bytes must include variants")
	}
	if err := cache.NewCacheInvalidator(c, slog.New(slog.NewTextHandler(io.Discard, nil))).InvalidateRendered(ctx); err != nil {
		t.Fatal(err)
	}
	if pages, b := c.RenderStats(); pages != 0 || b != 0 {
		t.Fatalf("after purge: pages=%d bytes=%d", pages, b)
	}
	plainKey := cache.RenderedPageKey(f.h.engine.Version(), "/about")
	for _, enc := range []string{"zstd", "gzip"} {
		if v, _ := c.Get(ctx, cache.EncodedKey(plainKey, enc)); v != nil {
			t.Errorf("%s variant survived the purge", enc)
		}
	}
}

// A purge between reading the plain entry and storing the variant must not
// leave a compressed copy of the old page behind.
func TestCachedVariantPurgeRace(t *testing.T) {
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	key := cache.RenderedPageKey("v", "/p")
	body := []byte(strings.Repeat("old page ", 300))
	_ = c.Set(ctx, key, body, time.Hour)

	gen := c.Generation()
	got, _ := c.Get(ctx, key)
	_ = c.DeletePattern(ctx, "render:*") // the purge lands after the read
	r := httptest.NewRequest(http.MethodGet, "/p", nil)
	r.Header.Set("Accept-Encoding", "zstd")
	enc, packed := cachedVariant(r, c, key, got, time.Hour, gen, func(string, ...any) {})
	if enc != httpx.EncodingZstd || len(packed) == 0 {
		t.Fatal("the in-flight request still answers")
	}
	if v, _ := c.Get(ctx, cache.EncodedKey(key, "zstd")); v != nil {
		t.Fatal("stale variant stored after a purge")
	}
}

func TestCachedVariantOffServesPlain(t *testing.T) {
	f, _ := variantFixture(t, false)
	_ = f.get("/about")
	w := f.get("/about", withAE("zstd"))
	if w.Header().Get("Content-Encoding") != "" || w.Header().Get("Vary") != "" {
		t.Fatalf("switch off (or demo) must serve plain: %v", w.Header())
	}
}

// The preloader renders through the handler directly with a refresh context:
// it must still fill and find the plain entry, whatever clients negotiate.
func TestCachedVariantLeavesPlainEntryForPreloader(t *testing.T) {
	f, c := variantFixture(t, true)
	_ = f.get("/about", withAE("zstd"))
	_ = f.get("/about", withAE("zstd"))
	plain, _ := c.Get(context.Background(), cache.RenderedPageKey(f.h.engine.Version(), "/about"))
	if !bytes.Contains(plain, []byte("About us")) {
		t.Fatal("plain entry must stay uncompressed")
	}
}

func TestFeedVariant(t *testing.T) {
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), infra: &Infrastructure{Cache: c}}
	s.compressOn.Store(true)
	body := []byte(`<?xml version="1.0"?><rss>` + strings.Repeat("<item>entry</item>", 200) + `</rss>`)
	probe := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
	key := cache.RenderedPageKey("feed:"+s.publicBaseURL(probe), "/feed.xml")
	_ = c.Set(context.Background(), key, body, time.Hour)
	get := func(ae string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		w := httptest.NewRecorder()
		s.handleFeed(w, r)
		return w
	}
	g := get("gzip")
	if g.Header().Get("Content-Encoding") != "gzip" || gunzip(t, g.Body.Bytes()) != string(body) {
		t.Fatalf("gzip feed: %v", g.Header())
	}
	if p := get(""); p.Header().Get("Content-Encoding") != "" || !bytes.Equal(p.Body.Bytes(), body) {
		t.Fatal("plain feed")
	}
}

// A request path is decoded before the cache lookup, so "%23zstd" must not
// reach a variant key: it is a miss that renders, never the stored variant,
// and a variant of it never answers a longer "%23zstd%23zstd" path.
func TestCachedVariantKeyUnreachableFromPath(t *testing.T) {
	f, c := variantFixture(t, true)
	_ = f.get("/about")
	z := f.get("/about", withAE("zstd")) // stores the variant
	if z.Header().Get("Content-Encoding") != "zstd" {
		t.Fatal("setup: no variant")
	}
	resolves := f.resolves
	for _, path := range []string{"/about%23zstd", "/about%23zstd%23zstd"} {
		w := f.get(path)
		resolves++
		if f.resolves != resolves {
			t.Fatalf("GET %s resolves = %d, want %d: it hit a cached entry", path, f.resolves, resolves)
		}
		if bytes.Equal(w.Body.Bytes(), z.Body.Bytes()) {
			t.Fatalf("GET %s served the stored variant bytes", path)
		}
		_ = f.get(path, withAE("zstd")) // hit on its own plain entry, variant under its own key
		if f.resolves != resolves {
			t.Fatalf("GET %s with zstd re-rendered", path)
		}
	}
	pages, _ := c.RenderStats()
	if pages != 3 {
		t.Errorf("pages = %d, want 3 (variants are not pages)", pages)
	}
	if v, _ := c.Get(context.Background(), cache.RenderedPageKey(f.h.engine.Version(), "/about#zstd#zstd")); len(v) == 0 {
		t.Error("the nested path should only own its plain entry")
	}
}

// storedVariant is the compressed bytes of a stored variant, without its fingerprint.
func storedVariant(t *testing.T, c *cache.Cache, key string) []byte {
	t.Helper()
	raw, _ := c.Get(context.Background(), key)
	if len(raw) <= fingerprintLen {
		return nil
	}
	return raw[fingerprintLen:]
}

// The plain entry is rewritten without touching its variants (a preload
// refresh, a short MaxCacheAge, expiry): a variant made from other bytes must
// never be served.
func TestCachedVariantFollowsPlainBytes(t *testing.T) {
	f, c := variantFixture(t, true)
	ctx := context.Background()
	_ = f.get("/about")
	old := f.get("/about", withAE("zstd")) // variant of the old page
	if old.Header().Get("Content-Encoding") != "zstd" {
		t.Fatal("setup: no variant")
	}
	plainKey := cache.RenderedPageKey(f.h.engine.Version(), "/about")
	fresh := strings.Repeat("Fresh words after a refresh. ", 120)
	_ = c.Set(ctx, plainKey, []byte(fresh), time.Hour) // plain overwritten, variant left in place

	for _, ae := range []string{"zstd", "gzip"} {
		w := f.get("/about", withAE(ae))
		var got string
		if ae == "zstd" {
			zr, _ := zstd.NewReader(nil)
			b, err := zr.DecodeAll(w.Body.Bytes(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got = string(b)
		} else {
			got = gunzip(t, w.Body.Bytes())
		}
		if got != fresh {
			t.Errorf("%s hit served stale bytes: %.40q", ae, got)
		}
	}
	// A plain entry that expired and came back with the same bytes keeps its variant.
	_ = c.Set(ctx, plainKey, []byte(fresh), time.Hour)
	marker := storedVariant(t, c, cache.EncodedKey(plainKey, "zstd"))
	if again := f.get("/about", withAE("zstd")); !bytes.Equal(again.Body.Bytes(), marker) {
		t.Error("identical plain bytes must reuse the variant")
	}
}

// Feeds are tied to their plain body the same way.
func TestFeedVariantFollowsPlainBytes(t *testing.T) {
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), infra: &Infrastructure{Cache: c}}
	s.compressOn.Store(true)
	probe := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
	key := cache.RenderedPageKey("feed:"+s.publicBaseURL(probe), "/feed.xml")
	feed := func(item string) []byte {
		return []byte(`<rss>` + strings.Repeat("<item>"+item+"</item>", 200) + `</rss>`)
	}
	get := func() string {
		r := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		s.handleFeed(w, r)
		return gunzip(t, w.Body.Bytes())
	}
	_ = c.Set(context.Background(), key, feed("old"), time.Hour)
	_ = get()
	_ = c.Set(context.Background(), key, feed("new"), time.Hour)
	if got := get(); got != string(feed("new")) {
		t.Fatalf("feed variant is stale: %.40q", got)
	}
}
