package redirect

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// countingStore counts AddRedirectHits calls on top of fakeStore.
type countingStore struct {
	*fakeStore
	cmu   sync.Mutex
	calls []int
}

func (c *countingStore) AddRedirectHits(ctx context.Context, id uuid.UUID, n int) error {
	c.cmu.Lock()
	c.calls = append(c.calls, n)
	c.cmu.Unlock()
	return c.fakeStore.AddRedirectHits(ctx, id, n)
}

func hitsOf(f *fakeStore, id uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[id]
}

func waitHits(t *testing.T, f *fakeStore, id uuid.UUID, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hitsOf(f, id) != want {
		if time.Now().After(deadline) {
			t.Fatalf("hits = %d, want %d", hitsOf(f, id), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func newHitService(store Store, every time.Duration) *Service {
	s := NewServiceWithStore(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	s.hitEvery = every // read by the hit goroutine only after the first hit is sent
	return s
}

func TestHitFlushNoTimerWhileIdle(t *testing.T) {
	r := &Redirect{SourcePath: "/old", TargetURL: "/new"}
	store := newFakeStore(r)
	s := newHitService(store, 5*time.Millisecond)
	defer s.Close()

	time.Sleep(50 * time.Millisecond)
	if n := s.hitFires.Load(); n != 0 {
		t.Fatalf("timer fired %d times while idle", n)
	}
	if s.CheckRedirect(context.Background(), "/old") == nil {
		t.Fatal("rule must match")
	}
	waitHits(t, store, r.ID, 1) // without Close
	if n := s.hitFires.Load(); n != 1 {
		t.Fatalf("fires = %d, want 1", n)
	}
	time.Sleep(50 * time.Millisecond)
	if n := s.hitFires.Load(); n != 1 {
		t.Fatalf("timer kept firing while idle: %d", n)
	}
}

func TestHitFlushWithinInterval(t *testing.T) {
	const every = 20 * time.Millisecond
	r := &Redirect{SourcePath: "/old", TargetURL: "/new"}
	store := newFakeStore(r)
	s := newHitService(store, every)
	defer s.Close()

	start := time.Now()
	s.CheckRedirect(context.Background(), "/old")
	waitHits(t, store, r.ID, 1)
	if d := time.Since(start); d < every || d >= time.Second {
		t.Fatalf("flushed after %v, want [%v, 1s)", d, every)
	}
}

func TestHitFlushCoalesces(t *testing.T) {
	r := &Redirect{SourcePath: "/old", TargetURL: "/new"}
	store := &countingStore{fakeStore: newFakeStore(r)}
	s := newHitService(store, 30*time.Millisecond)
	defer s.Close()

	for i := 0; i < 3; i++ {
		s.CheckRedirect(context.Background(), "/old")
	}
	waitHits(t, store.fakeStore, r.ID, 3)
	store.cmu.Lock()
	defer store.cmu.Unlock()
	if len(store.calls) != 1 || store.calls[0] != 3 {
		t.Fatalf("AddRedirectHits calls = %v, want [3]", store.calls)
	}
}
