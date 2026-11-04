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
	stored, _ := c.Get(ctx, zKey)
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
	_, _ = c.SetIfGeneration(ctx, zKey, marker, time.Hour, c.Generation())
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
