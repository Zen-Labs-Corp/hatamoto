package server

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// commentDropWindow is how long one entity's page drops are coalesced: a burst
// of rank or dead writes drops the pages once at once and once more at the end
// of the window, not once per write.
const commentDropWindow = 10 * time.Second

// dropCoalescer rate-limits per-entity cache drops. The first call in a window
// drops now; later calls in the window schedule one trailing drop at its end,
// so the final state is always dropped.
type dropCoalescer struct {
	drop   func(ctx context.Context, entityID uuid.UUID)
	window time.Duration
	now    func() time.Time
	after  func(d time.Duration, f func())

	mu      sync.Mutex
	last    map[uuid.UUID]time.Time
	pending map[uuid.UUID]bool
}

func newDropCoalescer(drop func(context.Context, uuid.UUID), window time.Duration) *dropCoalescer {
	return &dropCoalescer{
		drop: drop, window: window, now: time.Now,
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		last:  map[uuid.UUID]time.Time{}, pending: map[uuid.UUID]bool{},
	}
}

func (c *dropCoalescer) Drop(ctx context.Context, id uuid.UUID) {
	c.mu.Lock()
	now := c.now()
	if len(c.last) > 1024 {
		for k, t := range c.last {
			if now.Sub(t) >= c.window && !c.pending[k] {
				delete(c.last, k)
			}
		}
	}
	if t, ok := c.last[id]; ok && now.Sub(t) < c.window {
		if !c.pending[id] {
			c.pending[id] = true
			c.after(c.window-now.Sub(t), func() { c.trailing(id) })
		}
		c.mu.Unlock()
		return
	}
	c.last[id] = now
	c.mu.Unlock()
	c.drop(ctx, id)
}

func (c *dropCoalescer) trailing(id uuid.UUID) {
	c.mu.Lock()
	delete(c.pending, id)
	c.last[id] = c.now()
	c.mu.Unlock()
	c.drop(context.Background(), id)
}
