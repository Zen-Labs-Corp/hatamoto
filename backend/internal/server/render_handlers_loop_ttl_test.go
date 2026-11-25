package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"hatamoto.dev/core/internal/render"
)

// A page view carries the cap a today-relative query loop sets (MaxCacheAge);
// the stored TTL is the shorter of it and the configured TTL.
func TestLoopTTLCapsStoredPage(t *testing.T) {
	for name, tc := range map[string]struct {
		cap, want time.Duration
	}{
		"shorter cap": {3 * time.Hour, 3 * time.Hour},
		"longer cap":  {10 * time.Hour, 6 * time.Hour},
		"no cap":      {0, 6 * time.Hour},
	} {
		f := newRenderFixture(t)
		f.next = okPage("Events")
		f.h.cacheTTL = 6 * time.Hour
		f.h.buildView = func(_ context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
			v := pageViewFromRoute(rr, "https://x.io", requestPath(r))
			v.Site = render.SiteView{Name: "Fixture Site"}
			v.MaxCacheAge = tc.cap
			return v, nil
		}
		f.get("/events")
		if len(f.cache.ttls) != 1 || f.cache.ttls[0] != tc.want {
			t.Errorf("%s: stored with TTL %v, want %v", name, f.cache.ttls, tc.want)
		}
	}
}
