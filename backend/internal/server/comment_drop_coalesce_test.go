package server

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDropCoalescerOneImmediateOneTrailing(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var drops []uuid.UUID
	var timers []func()
	c := newDropCoalescer(func(_ context.Context, id uuid.UUID) { drops = append(drops, id) }, 10*time.Second)
	c.now = func() time.Time { return now }
	c.after = func(_ time.Duration, f func()) { timers = append(timers, f) }

	a, b := uuid.New(), uuid.New()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		c.Drop(ctx, a)
		now = now.Add(400 * time.Millisecond)
	}
	if len(drops) != 1 || len(timers) != 1 {
		t.Fatalf("burst: drops %d timers %d, want 1 and 1", len(drops), len(timers))
	}
	c.Drop(ctx, b) // another entity is independent
	if len(drops) != 2 {
		t.Fatalf("other entity not dropped at once: %d", len(drops))
	}
	now = now.Add(8 * time.Second)
	timers[0]()
	if len(drops) != 3 || drops[2] != a {
		t.Fatalf("trailing drop missing: %v", drops)
	}
	c.Drop(ctx, a) // a new window opened by the trailing drop
	if len(drops) != 3 || len(timers) != 2 {
		t.Fatalf("drop right after the trailing one must wait: drops %d timers %d", len(drops), len(timers))
	}
	now = now.Add(11 * time.Second)
	timers[1]()
	now = now.Add(11 * time.Second)
	c.Drop(ctx, a)
	if len(drops) != 5 {
		t.Fatalf("next window must drop again: %d", len(drops))
	}
}
