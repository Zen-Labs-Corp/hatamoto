package server

import (
	"context"
	"testing"
	"time"
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
