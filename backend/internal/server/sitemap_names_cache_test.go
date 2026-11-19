package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/cache"
)

// Names computed before a purge must not be stored after it.
func TestRememberSitemapNames_SkipsAfterPurge(t *testing.T) {
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := &Server{infra: &Infrastructure{Cache: c}}
	ctx := context.Background()
	key := cache.SitemapKey(sitemapNamesSub)
	names := sitemapNames{Keys: []string{"page"}}

	gen := s.cacheGeneration()
	_ = c.Delete(ctx, "unrelated") // a purge runs while the input loads
	s.rememberSitemapNames(ctx, gen, names)
	var got sitemapNames
	if err := c.GetJSON(ctx, key, &got); err == nil {
		t.Fatalf("names stored after a purge: %+v", got)
	}

	s.rememberSitemapNames(ctx, s.cacheGeneration(), names)
	if err := c.GetJSON(ctx, key, &got); err != nil || len(got.Keys) != 1 {
		t.Fatalf("names not stored on an undisturbed load: %v %+v", err, got)
	}
}
