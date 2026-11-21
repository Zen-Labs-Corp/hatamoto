package server

import (
	"bytes"
	"strconv"
	"sync"
	"testing"
	"time"
)

var nfT0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestNotFoundCacheHit(t *testing.T) {
	c := newNotFoundCache()
	c.put("k", []byte("body"), 3, nfT0.Add(time.Minute))
	got, ok := c.get("k", 3, nfT0)
	if !ok || string(got) != "body" {
		t.Fatalf("get = %q, %v; want hit", got, ok)
	}
}

func TestNotFoundCacheStaleGeneration(t *testing.T) {
	for _, gen := range []uint64{2, 4} {
		c := newNotFoundCache()
		c.put("k", []byte("body"), 3, nfT0.Add(time.Minute))
		if _, ok := c.get("k", gen, nfT0); ok {
			t.Fatalf("gen %d: hit on an entry rendered under 3", gen)
		}
		if len(c.m) != 0 || c.bytes != 0 {
			t.Fatalf("gen %d: stale entry kept (%d entries, %d bytes)", gen, len(c.m), c.bytes)
		}
	}
}

func TestNotFoundCacheExpiry(t *testing.T) {
	c := newNotFoundCache()
	c.put("k", []byte("body"), 1, nfT0.Add(time.Minute))
	if _, ok := c.get("k", 1, nfT0.Add(time.Minute)); ok {
		t.Fatal("hit at the expiry instant")
	}
	if len(c.m) != 0 {
		t.Fatal("expired entry kept")
	}
}

func TestNotFoundCacheEntryCap(t *testing.T) {
	c := newNotFoundCache()
	c.maxItems = 2
	exp := nfT0.Add(time.Minute)
	c.put("a", []byte("1"), 1, exp)
	c.put("b", []byte("2"), 1, exp)
	c.get("a", 1, nfT0)
	c.put("c", []byte("3"), 1, exp)
	if _, ok := c.get("b", 1, nfT0); ok {
		t.Fatal("b should have been evicted")
	}
	for _, k := range []string{"a", "c"} {
		if _, ok := c.get(k, 1, nfT0); !ok {
			t.Fatalf("%s evicted", k)
		}
	}
}

func TestNotFoundCacheByteCap(t *testing.T) {
	c := newNotFoundCache()
	c.maxBytes = 10
	exp := nfT0.Add(time.Minute)
	c.put("a", bytes.Repeat([]byte("x"), 6), 1, exp)
	c.put("b", bytes.Repeat([]byte("y"), 6), 1, exp)
	if c.bytes > c.maxBytes {
		t.Fatalf("bytes %d over cap %d", c.bytes, c.maxBytes)
	}
	if _, ok := c.get("a", 1, nfT0); ok {
		t.Fatal("oldest entry kept past the byte cap")
	}
	if _, ok := c.get("b", 1, nfT0); !ok {
		t.Fatal("newest entry lost")
	}
}

func TestNotFoundCacheByteCapIsLRU(t *testing.T) {
	c := newNotFoundCache()
	c.maxBytes = 20
	exp := nfT0.Add(time.Minute)
	c.put("a", bytes.Repeat([]byte("x"), 6), 1, exp)
	c.put("b", bytes.Repeat([]byte("y"), 6), 1, exp)
	c.get("a", 1, nfT0) // a is now the most recent
	c.put("c", bytes.Repeat([]byte("z"), 6), 1, exp)
	if _, ok := c.get("b", 1, nfT0); ok {
		t.Fatal("least recently used entry kept")
	}
	for _, k := range []string{"a", "c"} {
		if _, ok := c.get(k, 1, nfT0); !ok {
			t.Fatalf("%s evicted", k)
		}
	}
}

func TestNotFoundCacheReplaceOversize(t *testing.T) {
	c := newNotFoundCache()
	c.maxBytes = 10
	exp := nfT0.Add(time.Minute)
	c.put("k", []byte("ab"), 1, exp)
	c.put("k", bytes.Repeat([]byte("x"), 10), 1, exp) // 11 with the key: never fits
	if c.bytes != 0 || len(c.m) != 0 || c.ll.Len() != 0 {
		t.Fatalf("bytes=%d entries=%d list=%d, want an empty store", c.bytes, len(c.m), c.ll.Len())
	}
}

func TestNotFoundCacheReplace(t *testing.T) {
	c := newNotFoundCache()
	exp := nfT0.Add(time.Minute)
	c.put("k", []byte("long body"), 1, exp)
	c.put("k", []byte("s"), 1, exp)
	if c.bytes != int64(len("k")+len("s")) || len(c.m) != 1 || c.ll.Len() != 1 {
		t.Fatalf("bytes=%d entries=%d list=%d", c.bytes, len(c.m), c.ll.Len())
	}
}

func TestNotFoundCacheOversizeBody(t *testing.T) {
	c := newNotFoundCache()
	c.put("k", make([]byte, maxNotFoundBody+1), 1, nfT0.Add(time.Minute))
	if len(c.m) != 0 {
		t.Fatal("oversize body stored")
	}
}

func TestNotFoundCacheNil(t *testing.T) {
	var c *notFoundCache
	if _, ok := c.get("k", 1, nfT0); ok {
		t.Fatal("nil cache hit")
	}
	c.put("k", []byte("x"), 1, nfT0.Add(time.Minute))
}

func TestNotFoundCacheConcurrent(t *testing.T) {
	c := newNotFoundCache()
	c.maxItems = 8
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				k := strconv.Itoa(i % 20)
				c.put(k, []byte(k), 1, nfT0.Add(time.Minute))
				c.get(k, 1, nfT0)
			}
		}()
	}
	wg.Wait()
	if c.ll.Len() > 8 || len(c.m) != c.ll.Len() {
		t.Fatalf("inconsistent: list=%d map=%d", c.ll.Len(), len(c.m))
	}
}
