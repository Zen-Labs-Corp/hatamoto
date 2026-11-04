package server

import (
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/internal/media"
)

// A media change must drop the cached variant lookups: otherwise a page built
// right after a rebuild still lists copies the run has just deleted.
func TestMediaChangeClearsVariantCache(t *testing.T) {
	t.Parallel()
	s := &Server{}
	s.renderData = newRenderData(s)
	s.renderData.variants = map[string]cachedVariants{
		"/m/a.jpg": {w: 10, h: 10, expires: time.Now().Add(time.Minute)},
	}
	s.onMediaChange(context.Background())
	if n := len(s.renderData.variants); n != 0 {
		t.Fatalf("variant cache holds %d entries after a media change", n)
	}
}

// The format policy is a settings read: it is cached across ImageVariants
// calls, and a media change makes the next call read it again.
func TestMediaFormatsPolicyCachedUntilMediaChange(t *testing.T) {
	t.Parallel()
	reads := 0
	s := &Server{}
	s.formatPolicy = func(context.Context) media.Formats { reads++; return media.Formats{WebP: true} }
	s.renderData = newRenderData(s)
	for range 5 {
		s.renderData.mediaFormats()
	}
	if reads != 1 {
		t.Fatalf("policy read %d times for 5 calls, want 1", reads)
	}
	s.onMediaChange(context.Background())
	s.renderData.mediaFormats()
	if reads != 2 {
		t.Fatalf("policy read %d times after a media change, want 2", reads)
	}
}
