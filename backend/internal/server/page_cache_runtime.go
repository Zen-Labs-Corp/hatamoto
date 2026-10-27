package server

import (
	"context"
	"sync/atomic"
	"time"
)

// pageCacheRuntime holds the page-cache settings the render path reads per
// request. A nil runtime means "enabled, default lifetime", so servers built
// without one (tests, the route table shell) behave as before.
type pageCacheRuntime struct {
	enabled  atomic.Bool
	ttlNanos atomic.Int64 // 0 = use the default
}

func newPageCacheRuntime() *pageCacheRuntime {
	r := &pageCacheRuntime{}
	r.enabled.Store(true)
	return r
}

// Enabled reports whether rendered pages may be read from and written to the cache.
func (r *pageCacheRuntime) Enabled() bool { return r == nil || r.enabled.Load() }

// TTL is the admin-set page lifetime, or 0 when the default applies.
func (r *pageCacheRuntime) TTL() time.Duration {
	if r == nil {
		return 0
	}
	return time.Duration(r.ttlNanos.Load())
}

// Apply swaps the live settings; it takes effect on the next request.
func (r *pageCacheRuntime) Apply(enabled bool, ttl time.Duration) {
	if r == nil {
		return
	}
	r.ttlNanos.Store(int64(ttl))
	r.enabled.Store(enabled)
}

// Source labels returned by resolveTTL.
const (
	ttlSourceAdmin  = "admin"
	ttlSourceServer = "server"
)

// resolveTTL returns the effective page lifetime and where it came from: the
// admin value when set (> 0), otherwise the server default (env or built-in).
func resolveTTL(adminSeconds int, serverDefault time.Duration) (time.Duration, string) {
	if adminSeconds > 0 {
		return time.Duration(adminSeconds) * time.Second, ttlSourceAdmin
	}
	return serverDefault, ttlSourceServer
}

type refreshKey struct{}

// withRefresh marks a request as a cache refresh: serve skips the cache read,
// re-renders and overwrites the entry (used by the preloader).
func withRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, refreshKey{}, true)
}

func refreshRequested(ctx context.Context) bool {
	v, _ := ctx.Value(refreshKey{}).(bool)
	return v
}
