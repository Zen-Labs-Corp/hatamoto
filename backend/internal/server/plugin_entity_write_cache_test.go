package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
)

// TestPluginEntityWriteCacheCost pins what a plugin's entity writes cost the
// page cache (documented in docs/PLUGIN_API.md):
//   - each write, through the entity hook, drops every rendered page and every
//     cached sitemap (a tagged drop: the cache is not flushed, and a store of
//     something carrying no entity tag, started before the writes, still lands);
//   - the content-change poller adds a coalesced full purge: the first change
//     purges at once, then at most one per gap while writes keep coming, plus
//     one trailing purge. The gap is 15 s in production, 5 s in development.
func TestPluginEntityWriteCacheCost(t *testing.T) {
	t.Run("hook drops pages and sitemaps per write, not the whole cache", func(t *testing.T) {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		probe := newRenderProbe(log)
		c := probe.c
		s := &Server{
			logger: log,
			hooks:  hooks.New(log),
			infra:  &Infrastructure{Cache: c, CacheInvalidator: cache.NewCacheInvalidator(c, log)},
		}
		s.registerRenderInvalidation()
		sink := pluginhost.NewEntitySink(s.hooks, log)
		tok := c.Generation()
		const writes = 20
		for i := range writes {
			probe.prime()
			_ = c.Set(context.Background(), cache.SitemapKey("misc"), []byte("<xml/>"), time.Minute)
			id := uuid.New()
			// What PluginCreate emits for a published entry.
			sink.Emit(context.Background(), entity.Event{Kind: entity.EventCreated, ID: id, Slug: "x"})
			sink.Emit(context.Background(), entity.Event{Kind: entity.EventPublished, ID: id, Slug: "x", PrevStatus: string(entity.StatusDraft)})
			s.hooks.Wait()
			if !probe.cleared() {
				t.Fatalf("write %d left a rendered page", i)
			}
			if _, err := c.Get(context.Background(), cache.SitemapKey("misc")); err == nil {
				t.Fatalf("write %d left a cached sitemap", i)
			}
		}
		ctx := context.Background()
		if ok, _ := c.SetIfGenerationTagged(ctx, "other:key", []byte("x"), time.Minute, tok, "unrelated-tag"); !ok {
			t.Fatalf("%d writes flushed the whole cache: a store with no entity tag was refused", writes)
		}
		// Every rendered page carries the any-entity tag, so one started before a write is refused.
		if ok, _ := c.SetIfGenerationTagged(ctx, probeKey, []byte("x"), time.Minute, tok, cache.TagAnyEntity); ok {
			t.Fatal("an in-flight page render survived a write")
		}
	})

	t.Run("a burst inside one gap is one purge plus a trailing one", func(t *testing.T) {
		// 20 writes, one per tick (the limiter's burst), gap of 5 ticks.
		steps := []pollStep{quiet}
		for range 20 {
			steps = append(steps, change)
		}
		steps = append(steps, quiet, quiet)
		sp := &scriptedPoll{steps: steps}
		sp.run(t, 5*scriptedEvery)
		if want := 20/5 + 2; len(sp.purges) > want {
			t.Fatalf("%d epoch purges for 20 writes, want at most %d: %v", len(sp.purges), want, sp.purges)
		}
	})

	t.Run("a minute at the limiter's steady rate stays under one purge per gap", func(t *testing.T) {
		// 60 writes in 60 s (the sustained limit), production gap of 15 s.
		const gap = 15
		steps := []pollStep{quiet}
		for range 60 {
			steps = append(steps, change)
		}
		steps = append(steps, quiet, quiet)
		sp := &scriptedPoll{steps: steps}
		sp.run(t, gap*scriptedEvery)
		if want := 60/gap + 2; len(sp.purges) > want {
			t.Fatalf("%d epoch purges for a minute of writes, want at most %d: %v", len(sp.purges), want, sp.purges)
		}
		if len(sp.purges) < 2 {
			t.Fatalf("expected the leading and the trailing purge, got %v", sp.purges)
		}
	})
}
