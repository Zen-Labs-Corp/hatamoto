package cache

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSetBoundsEntries(t *testing.T) {
	c := New(nil)
	c.maxEntries = 100
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		_ = c.Set(ctx, "k"+string(rune('a'+i%26))+time.Duration(i).String(), []byte("v"), time.Hour)
		if len(c.entries) > c.maxEntries {
			t.Fatalf("entries = %d after %d sets, cap %d", len(c.entries), i+1, c.maxEntries)
		}
	}
	// The newest write always survives eviction.
	_ = c.Set(ctx, "newest", []byte("v"), time.Hour)
	if _, err := c.Get(ctx, "newest"); err != nil {
		t.Fatalf("newest entry evicted: %v", err)
	}
}

func TestSetSweepsExpiredAmortised(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		_ = c.Set(ctx, "old"+time.Duration(i).String(), []byte("v"), -time.Second)
	}
	// Fewer than sweepEvery writes: expired entries may linger (no full walk per write).
	if len(c.entries) != 50 {
		t.Fatalf("entries = %d, want 50 (no per-write sweep)", len(c.entries))
	}
	for i := 0; i < sweepEvery; i++ {
		_ = c.Set(ctx, "live"+time.Duration(i).String(), []byte("v"), time.Hour)
	}
	for k := range c.entries {
		if len(k) >= 3 && k[:3] == "old" {
			t.Fatalf("expired entry %q survived the sweep", k)
		}
	}
}

func TestGetTreatsExpiredAsMiss(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	_ = c.Set(ctx, "k", []byte("v"), -time.Second)
	if _, err := c.Get(ctx, "k"); err != ErrCacheMiss {
		t.Fatalf("err = %v, want ErrCacheMiss", err)
	}
}

func TestSetIfGenerationSkipsAfterDelete(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	gen := c.Generation()
	if ok, _ := c.SetIfGeneration(ctx, "a", []byte("v"), time.Hour, gen); !ok {
		t.Fatal("set without an intervening delete was skipped")
	}
	_ = c.DeletePattern(ctx, "zzz*") // matches nothing, still a purge
	if ok, _ := c.SetIfGeneration(ctx, "b", []byte("v"), time.Hour, gen); ok {
		t.Fatal("set after a purge with a stale generation was stored")
	}
	if _, err := c.Get(ctx, "b"); err == nil {
		t.Fatal("stale entry present")
	}
	gen = c.Generation()
	_ = c.Delete(ctx, "a")
	if ok, _ := c.SetIfGeneration(ctx, "c", []byte("v"), time.Hour, gen); ok {
		t.Fatal("set after Delete with a stale generation was stored")
	}
}

// checkTags recounts the tag index from the entries and compares it with
// tagKeys: no stale key, no missing key, no empty set.
func checkTags(t *testing.T, c *Cache) {
	t.Helper()
	c.mu.RLock()
	defer c.mu.RUnlock()
	want := map[string]map[string]struct{}{}
	for k, e := range c.entries {
		for _, tag := range e.tags {
			if want[tag] == nil {
				want[tag] = map[string]struct{}{}
			}
			want[tag][k] = struct{}{}
		}
	}
	if len(want) != len(c.tagKeys) {
		t.Fatalf("tag index has %d tags, entries carry %d", len(c.tagKeys), len(want))
	}
	for tag, keys := range c.tagKeys {
		if len(keys) == 0 {
			t.Fatalf("tag %q has an empty set", tag)
		}
		if len(keys) != len(want[tag]) {
			t.Fatalf("tag %q indexes %d keys, entries carry %d", tag, len(keys), len(want[tag]))
		}
		for k := range keys {
			if _, ok := want[tag][k]; !ok {
				t.Fatalf("tag %q indexes %q, which does not carry it", tag, k)
			}
		}
	}
}

func TestTaggedStoreRejectedAfterOwnTagDrop(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	tok := c.Generation()
	c.DeleteTagged(ctx, "a")
	if ok, _ := c.SetIfGenerationTagged(ctx, "render:v1:x:/a", []byte("v"), time.Hour, tok, "a"); ok {
		t.Fatal("store tagged with a dropped tag was accepted")
	}
	if ok, _ := c.SetIfGenerationTagged(ctx, "render:v1:x:/a", []byte("v"), time.Hour, tok, "a", "b"); ok {
		t.Fatal("store carrying a dropped tag among others was accepted")
	}
	checkTags(t, c)
}

func TestTaggedStoreSurvivesOtherTagDrop(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	tok := c.Generation()
	c.DeleteTagged(ctx, "a")
	if ok, _ := c.SetIfGenerationTagged(ctx, "render:v1:x:/b", []byte("v"), time.Hour, tok, "b"); !ok {
		t.Fatal("store of an unrelated tag was rejected")
	}
	checkTags(t, c)
}

func TestGlobalDeleteRejectsTaggedStore(t *testing.T) {
	ctx := context.Background()
	for name, drop := range map[string]func(*Cache){
		"Delete":        func(c *Cache) { _ = c.Delete(ctx, "nothing") },
		"DeletePattern": func(c *Cache) { _ = c.DeletePattern(ctx, "nothing*") },
	} {
		c := New(nil)
		tok := c.Generation()
		drop(c)
		if ok, _ := c.SetIfGenerationTagged(ctx, "render:v1:x:/b", []byte("v"), time.Hour, tok, "b"); ok {
			t.Errorf("%s: tagged store accepted", name)
		}
		if ok, _ := c.SetIfGenerationTagged(ctx, "render:v1:x:/b", []byte("v"), time.Hour, tok); ok {
			t.Errorf("%s: implicitly tagged store accepted", name)
		}
	}
}

func TestDeleteTaggedKeepsGlobalGeneration(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	tok := c.Generation()
	c.DeleteTagged(ctx, "a")
	if c.Generation() == tok {
		t.Fatal("DeleteTagged must advance the token")
	}
	if ok, _ := c.SetIfGeneration(ctx, "site:data", []byte("v"), time.Hour, tok); !ok {
		t.Fatal("untagged non-render store rejected by a tag drop")
	}
}

func TestTagIndexMatchesEntries(t *testing.T) {
	ctx := context.Background()
	tagged := func(c *Cache, key string, tags ...string) {
		t.Helper()
		if ok, _ := c.SetIfGenerationTagged(ctx, key, []byte("vv"), time.Hour, c.Generation(), tags...); !ok {
			t.Fatalf("store %q rejected", key)
		}
	}

	t.Run("overwrite", func(t *testing.T) {
		c := New(nil)
		tagged(c, "render:a", "x", "y")
		tagged(c, "render:a", "z")
		checkTags(t, c)
		if _, ok := c.tagKeys["x"]; ok {
			t.Fatal("old tag still indexed after overwrite")
		}
	})
	t.Run("sweep", func(t *testing.T) {
		c := New(nil)
		for i := 0; i < 20; i++ {
			_ = c.Set(ctx, fmt.Sprintf("render:old%d", i), []byte("v"), -time.Second)
		}
		for i := 0; i < sweepEvery; i++ {
			tagged(c, fmt.Sprintf("render:new%d", i), "n")
		}
		checkTags(t, c)
	})
	t.Run("maxEntries", func(t *testing.T) {
		c := New(nil)
		c.maxEntries = 100
		for i := 0; i < 500; i++ {
			tagged(c, fmt.Sprintf("render:k%d", i), fmt.Sprintf("t%d", i%7))
		}
		checkTags(t, c)
	})
	t.Run("byteCap", func(t *testing.T) {
		c := New(nil)
		c.SetRenderMaxBytes(40)
		for i := 0; i < 50; i++ {
			tagged(c, fmt.Sprintf("render:k%d", i), "t", fmt.Sprintf("u%d", i%3))
		}
		if c.Evictions() == 0 {
			t.Fatal("expected cap evictions")
		}
		checkTags(t, c)
	})
	t.Run("deletes", func(t *testing.T) {
		c := New(nil)
		for i := 0; i < 30; i++ {
			tagged(c, fmt.Sprintf("render:k%d", i), "t", fmt.Sprintf("u%d", i%3))
		}
		_ = c.Delete(ctx, "render:k0", "missing")
		checkTags(t, c)
		_ = c.DeletePattern(ctx, "render:k1*")
		checkTags(t, c)
		if n := c.DeleteTagged(ctx, "u1"); n == 0 {
			t.Fatal("DeleteTagged removed nothing")
		}
		checkTags(t, c)
		c.DeleteTagged(ctx, "t")
		checkTags(t, c)
		if len(c.entries) != 0 || len(c.tagKeys) != 0 {
			t.Fatalf("entries=%d tags=%d, want none", len(c.entries), len(c.tagKeys))
		}
	})
}

func TestTagTicksBounded(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	old := c.Generation()
	for i := 0; i < 5000; i++ {
		c.DeleteTagged(ctx, fmt.Sprintf("t%d", i))
		if len(c.tagTick) > maxTagTicks {
			t.Fatalf("tagTick = %d, cap %d", len(c.tagTick), maxTagTicks)
		}
	}
	if ok, _ := c.SetIfGenerationTagged(ctx, "render:v1:x:/p", []byte("v"), time.Hour, old, "fresh"); ok {
		t.Fatal("token from before the prune accepted a tagged store")
	}
	if ok, _ := c.SetIfGenerationTagged(ctx, "render:v1:x:/p", []byte("v"), time.Hour, c.Generation(), "fresh"); !ok {
		t.Fatal("fresh token rejected")
	}
}

func TestVariantStoreInheritsPlainTags(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	plain := "render:v1:x:/a"
	zk, gk := EncodedKey(plain, "zstd"), EncodedKey(plain, "gzip")

	if ok, _ := c.SetIfGeneration(ctx, zk, []byte("z"), time.Hour, c.Generation()); ok {
		t.Fatal("variant stored without its plain key")
	}
	if ok, _ := c.SetIfGenerationTagged(ctx, plain, []byte("p"), time.Hour, c.Generation(), "X"); !ok {
		t.Fatal("plain store rejected")
	}
	tok := c.Generation()
	for _, k := range []string{zk, gk} {
		if ok, _ := c.SetIfGeneration(ctx, k, []byte("v"), time.Hour, tok); !ok {
			t.Fatalf("variant %q rejected", k)
		}
		if _, ok := c.tagKeys["X"][k]; !ok {
			t.Fatalf("variant %q not indexed under X", k)
		}
	}
	checkTags(t, c)

	c.DeleteTagged(ctx, "X")
	for _, k := range []string{plain, zk, gk} {
		if _, err := c.Get(ctx, k); err == nil {
			t.Fatalf("%q survived the drop of X", k)
		}
	}
	if ok, _ := c.SetIfGeneration(ctx, zk, []byte("v"), time.Hour, tok); ok {
		t.Fatal("variant with a pre-drop token stored")
	}
	checkTags(t, c)
}
