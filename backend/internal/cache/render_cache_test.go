package cache

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRenderedPageKey(t *testing.T) {
	a := RenderedPageKey("v1abc", "/about")
	if a != "render:v1:v1abc:/about" {
		t.Fatalf("key = %q", a)
	}
	for _, other := range []string{
		RenderedPageKey("v2", "/about"),
		RenderedPageKey("v1abc", "/contact"),
	} {
		if other == a {
			t.Errorf("key collision: %q", other)
		}
		if !strings.HasPrefix(other, "render:") {
			t.Errorf("key %q outside render:* namespace", other)
		}
	}
}

func TestCacheTTLAndPatternDelete(t *testing.T) {
	ctx := context.Background()
	c := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	_ = c.Set(ctx, "render:v1:a:/x/y", []byte("1"), time.Minute)
	_ = c.Set(ctx, "other:k", []byte("2"), time.Minute)
	_ = c.Set(ctx, "expired", []byte("3"), -time.Second)

	if _, err := c.Get(ctx, "expired"); err != ErrCacheMiss {
		t.Errorf("expired entry: err = %v", err)
	}
	if err := c.DeletePattern(ctx, "render:*"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "render:v1:a:/x/y"); err != ErrCacheMiss {
		t.Errorf("pattern delete left render key: %v", err)
	}
	if _, err := c.Get(ctx, "other:k"); err != nil {
		t.Errorf("pattern delete removed unrelated key: %v", err)
	}
}

func TestContentInvalidationsAlsoDropRenderedPages(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	calls := map[string]func(*CacheInvalidator) error{
		"entity": func(ci *CacheInvalidator) error { return ci.InvalidateEntity(ctx, uuid.New(), "about") },
		"menu":   func(ci *CacheInvalidator) error { return ci.InvalidateMenu(ctx) },
		"tenant": func(ci *CacheInvalidator) error { return ci.InvalidateTenant(ctx) },
	}
	for name, call := range calls {
		c := New(log)
		key := RenderedPageKey("v", "/p")
		_ = c.Set(ctx, key, []byte("x"), time.Minute)
		if err := call(NewCacheInvalidator(c, log)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := c.Get(ctx, key); err == nil {
			t.Errorf("%s: rendered page survived invalidation", name)
		}
	}
}

// sumRender recomputes the render stats straight from the map.
func sumRender(c *Cache) (int, int64) {
	n, b := 0, int64(0)
	for k, e := range c.entries {
		if isRenderKey(k) {
			n++
			b += int64(len(e.value))
		}
	}
	return n, b
}

func checkStats(t *testing.T, c *Cache) {
	t.Helper()
	wantN, wantB := sumRender(c)
	n, b := c.RenderStats()
	if n != wantN || b != wantB {
		t.Fatalf("RenderStats = (%d, %d), map holds (%d, %d)", n, b, wantN, wantB)
	}
}

func TestRenderStatsTracksSetOverwriteDelete(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	_ = c.Set(ctx, "render:a", []byte("12345"), time.Hour)
	_ = c.Set(ctx, "render:b", []byte("123"), time.Hour)
	_ = c.Set(ctx, "public:x", []byte("ignored"), time.Hour)
	if n, b := c.RenderStats(); n != 2 || b != 8 {
		t.Fatalf("after sets = (%d, %d), want (2, 8)", n, b)
	}
	_ = c.Set(ctx, "render:a", []byte("12"), time.Hour) // overwrite subtracts old
	if n, b := c.RenderStats(); n != 2 || b != 5 {
		t.Fatalf("after overwrite = (%d, %d), want (2, 5)", n, b)
	}
	_ = c.Delete(ctx, "render:b", "missing")
	if n, b := c.RenderStats(); n != 1 || b != 2 {
		t.Fatalf("after delete = (%d, %d), want (1, 2)", n, b)
	}
	_ = c.DeletePattern(ctx, "render:*")
	if n, b := c.RenderStats(); n != 0 || b != 0 {
		t.Fatalf("after pattern delete = (%d, %d), want (0, 0)", n, b)
	}
	if _, err := c.Get(ctx, "public:x"); err != nil {
		t.Fatalf("non-render key must survive: %v", err)
	}
}

func TestRenderStatsExpirySweepAndArbitraryEviction(t *testing.T) {
	c := New(nil)
	c.maxEntries = 100
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		_ = c.Set(ctx, fmt.Sprintf("render:old%d", i), []byte("xx"), -time.Second)
	}
	for i := 0; i < sweepEvery; i++ { // forces the expired sweep
		_ = c.Set(ctx, fmt.Sprintf("render:new%d", i), []byte("yyy"), time.Hour)
		checkStats(t, c)
	}
	for i := 0; i < 500; i++ { // forces arbitrary eviction at maxEntries
		_ = c.Set(ctx, fmt.Sprintf("render:more%d", i), []byte("z"), time.Hour)
	}
	checkStats(t, c)
}

func TestRenderCapEvictsOldestFirst(t *testing.T) {
	c := New(nil)
	c.SetRenderMaxBytes(100)
	ctx := context.Background()
	for i := 0; i < 10; i++ { // 10 x 10 bytes fills the cap exactly
		_ = c.Set(ctx, fmt.Sprintf("render:%d", i), make([]byte, 10), time.Hour)
	}
	if c.Evictions() != 0 {
		t.Fatalf("evictions = %d before the cap is exceeded", c.Evictions())
	}
	_ = c.Set(ctx, "render:new", make([]byte, 10), time.Hour)
	if c.Evictions() != 1 {
		t.Fatalf("evictions = %d, want 1", c.Evictions())
	}
	if _, b := c.RenderStats(); b > 100 {
		t.Fatalf("bytes %d exceed cap", b)
	}
	// 90% of 100 = 90: 80 old + 10 new would be 90, so two oldest are gone.
	for _, k := range []string{"render:0", "render:1"} {
		if _, err := c.Get(ctx, k); err == nil {
			t.Fatalf("%s should be evicted", k)
		}
	}
	for _, k := range []string{"render:2", "render:9", "render:new"} {
		if _, err := c.Get(ctx, k); err != nil {
			t.Fatalf("%s should survive: %v", k, err)
		}
	}
	checkStats(t, c)
}

func TestRenderBytesNeverExceedCap(t *testing.T) {
	c := New(nil)
	c.SetRenderMaxBytes(1000)
	ctx := context.Background()
	for i := 0; i < 500; i++ {
		_ = c.Set(ctx, fmt.Sprintf("render:%d", i%70), make([]byte, 1+i%97), time.Hour)
		if _, b := c.RenderStats(); b > 1000 {
			t.Fatalf("write %d: bytes %d exceed cap", i, b)
		}
	}
	checkStats(t, c)
}

func TestRenderValueLargerThanCapNotStored(t *testing.T) {
	c := New(nil)
	c.SetRenderMaxBytes(10)
	ctx := context.Background()
	_ = c.Set(ctx, "render:small", []byte("abc"), time.Hour)
	gen := c.Generation()
	ok, _ := c.SetIfGeneration(ctx, "render:big", make([]byte, 11), time.Hour, gen)
	if ok {
		t.Fatal("SetIfGeneration must report false for a refused value")
	}
	if _, err := c.Get(ctx, "render:big"); err == nil {
		t.Fatal("oversized value was stored")
	}
	if c.Evictions() != 1 {
		t.Fatalf("evictions = %d, want 1 (cap hit)", c.Evictions())
	}
	if _, err := c.Get(ctx, "render:small"); err != nil {
		t.Fatal("existing entries must survive a refused write")
	}
	checkStats(t, c)
}

func TestCapNeverEvictsNonRenderKeys(t *testing.T) {
	c := New(nil)
	c.SetRenderMaxBytes(20)
	ctx := context.Background()
	_ = c.Set(ctx, "public:big", make([]byte, 500), time.Hour)
	_ = c.Set(ctx, "sitemap:x", make([]byte, 500), time.Hour)
	for i := 0; i < 20; i++ {
		_ = c.Set(ctx, fmt.Sprintf("render:%d", i), make([]byte, 10), time.Hour)
	}
	for _, k := range []string{"public:big", "sitemap:x"} {
		if _, err := c.Get(ctx, k); err != nil {
			t.Fatalf("%s was evicted: %v", k, err)
		}
	}
	if _, b := c.RenderStats(); b > 20 {
		t.Fatalf("render bytes %d exceed cap", b)
	}
}

func TestCapEvictionKeepsGenerationDeleteBumpsIt(t *testing.T) {
	c := New(nil)
	c.SetRenderMaxBytes(20)
	ctx := context.Background()
	gen := c.Generation()
	for i := 0; i < 10; i++ {
		_ = c.Set(ctx, fmt.Sprintf("render:%d", i), make([]byte, 10), time.Hour)
	}
	if c.Evictions() == 0 {
		t.Fatal("expected cap evictions")
	}
	if c.Generation() != gen {
		t.Fatal("cap eviction must not bump the generation")
	}
	_ = c.Delete(ctx, "render:9")
	if c.Generation() == gen {
		t.Fatal("Delete must bump the generation")
	}
}

func TestSetRenderMaxBytesZeroMeansNoLimit(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	c.SetRenderMaxBytes(10)
	c.SetRenderMaxBytes(0)
	if c.RenderMaxBytes() != 0 {
		t.Fatal("cap not cleared")
	}
	_ = c.Set(ctx, "render:a", make([]byte, 1000), time.Hour)
	if n, _ := c.RenderStats(); n != 1 || c.Evictions() != 0 {
		t.Fatal("no limit must store everything")
	}
}

func TestCapEvictionTenThousandEntriesIsFast(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	for i := 0; i < 10000; i++ {
		_ = c.Set(ctx, fmt.Sprintf("render:%d", i), make([]byte, 100), time.Hour)
	}
	c.SetRenderMaxBytes(500_000) // below the 1,000,000 held
	start := time.Now()
	_ = c.Set(ctx, "render:trigger", make([]byte, 100), time.Hour)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("cap eviction over 10k entries took %s", d)
	}
	if _, b := c.RenderStats(); b > 500_000 {
		t.Fatalf("bytes %d exceed cap", b)
	}
	checkStats(t, c)
}

// A paged builder loop caches ?page=N under path+"?page=N": the entity purge
// drops those keys with every other rendered page.
func TestEntityInvalidationDropsPagedLoopKeys(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	c := New(log)
	keys := []string{RenderedPageKey("v", "/news"), RenderedPageKey("v", "/news?page=2"), RenderedPageKey("v", "/news?page=3")}
	for _, k := range keys {
		_ = c.Set(ctx, k, []byte("x"), time.Minute)
	}
	if err := NewCacheInvalidator(c, log).InvalidateEntity(ctx, uuid.New(), "news"); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if _, err := c.Get(ctx, k); err == nil {
			t.Errorf("%s survived the purge", k)
		}
	}
}
