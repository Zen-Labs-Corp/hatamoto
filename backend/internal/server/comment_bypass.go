package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment/identity"
)

const (
	ownCacheTTL = 30 * time.Second
	ownCacheCap = 10000
)

// commentBypass reports whether this request must skip the shared page cache
// because the visitor holds a validly SIGNED device cookie and has non-public
// comments (draft, pending or spam).
type commentBypass struct {
	verify func(val string, now time.Time) (identity.DeviceClaims, bool)  // identity.VerifyDevice with the device key; no DB
	hasOwn func(ctx context.Context, commenterID uuid.UUID) (bool, error) // comment.Service.HasNonPublic
	own    *ownCache
	now    func() time.Time
}

// newCommentBypass wires the checks. verify must be signature-only.
func newCommentBypass(verify func(string, time.Time) (identity.DeviceClaims, bool),
	hasOwn func(context.Context, uuid.UUID) (bool, error), now func() time.Time) *commentBypass {
	if now == nil {
		now = time.Now
	}
	return &commentBypass{verify: verify, hasOwn: hasOwn, own: newOwnCache(now), now: now}
}

// applies reports whether the request needs its own, uncached render. A nil
// receiver or a request without the cookie costs nothing. A lookup error falls
// back to the shared page: private data is never shown on a failure.
func (b *commentBypass) applies(r *http.Request) bool {
	if b == nil || r.Header.Get("Cookie") == "" {
		return false
	}
	c, err := r.Cookie(identity.CookieName)
	if err != nil {
		return false
	}
	claims, ok := b.verify(c.Value, b.now())
	if !ok {
		return false
	}
	if has, known := b.own.get(claims.CommenterID); known {
		return has
	}
	has, err := b.hasOwn(r.Context(), claims.CommenterID)
	if err != nil {
		return false
	}
	b.own.set(claims.CommenterID, has)
	return has
}

// forget drops the cached answer for a commenter, after they posted.
func (b *commentBypass) forget(id uuid.UUID) {
	if b != nil {
		b.own.forget(id)
	}
}

type ownEntry struct {
	has     bool
	expires time.Time
}

// ownCache remembers "has non-public comments" per commenter for a short time.
type ownCache struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[uuid.UUID]ownEntry
}

func newOwnCache(now func() time.Time) *ownCache {
	return &ownCache{now: now, m: map[uuid.UUID]ownEntry{}}
}

func (c *ownCache) get(id uuid.UUID) (has, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	if !ok || !c.now().Before(e.expires) {
		return false, false
	}
	return e.has, true
}

func (c *ownCache) set(id uuid.UUID, has bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.m) >= ownCacheCap {
		for k, e := range c.m {
			if !now.Before(e.expires) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= ownCacheCap { // all live: start over rather than grow
			clear(c.m)
		}
	}
	c.m[id] = ownEntry{has: has, expires: now.Add(ownCacheTTL)}
}

func (c *ownCache) forget(id uuid.UUID) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}
