package cache

import (
	"context"
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
