package analytics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// lockedRepo is a concurrency-safe Repository and TrafficRepository.
type lockedRepo struct {
	mu       sync.Mutex
	failNext int
	adds     int64
	traffic  int
}

func (r *lockedRepo) Add(_ context.Context, _ time.Time, _, _ string, n int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext > 0 {
		r.failNext--
		return errors.New("down")
	}
	r.adds += n
	return nil
}

func (r *lockedRepo) AddTraffic(_ context.Context, rows []TrafficRow) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.traffic += len(rows)
	return nil
}

func (r *lockedRepo) get() (int64, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.adds, r.traffic
}

func newFlushService(repo Repository, every time.Duration) *Service {
	s := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.flushEvery = every
	return s
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFlushLoopNoTimerWhileIdle(t *testing.T) {
	repo := &lockedRepo{}
	svc := newFlushService(repo, 5*time.Millisecond)
	svc.Start()
	defer svc.Close(context.Background())

	time.Sleep(50 * time.Millisecond)
	if n := svc.fires.Load(); n != 0 {
		t.Fatalf("timer fired %d times while idle", n)
	}
	svc.RecordPageViews(2)
	waitFor(t, "flush without Close", func() bool { a, _ := repo.get(); return a == 2 })
	if n := svc.fires.Load(); n != 1 {
		t.Fatalf("fires = %d, want 1", n)
	}
	time.Sleep(50 * time.Millisecond)
	if n := svc.fires.Load(); n != 1 {
		t.Fatalf("timer kept firing while idle: %d", n)
	}
}

func TestFlushLoopWithinInterval(t *testing.T) {
	const every = 20 * time.Millisecond
	repo := &lockedRepo{}
	svc := newFlushService(repo, every)
	svc.Start()
	defer svc.Close(context.Background())

	start := time.Now()
	svc.RecordPageViews(1)
	waitFor(t, "flush", func() bool { a, _ := repo.get(); return a == 1 })
	if d := time.Since(start); d < every || d >= time.Second {
		t.Fatalf("flushed after %v, want [%v, 1s)", d, every)
	}
}

func TestFlushLoopRetriesFailedWrite(t *testing.T) {
	repo := &lockedRepo{failNext: 1}
	svc := newFlushService(repo, 5*time.Millisecond)
	svc.Start()
	defer svc.Close(context.Background())

	svc.RecordPageViews(1)
	waitFor(t, "retried write", func() bool { a, _ := repo.get(); return a == 1 })
	waitFor(t, "second fire", func() bool { return svc.fires.Load() >= 2 })
	time.Sleep(50 * time.Millisecond)
	n := svc.fires.Load()
	time.Sleep(50 * time.Millisecond)
	if got := svc.fires.Load(); got != n || n != 2 {
		t.Fatalf("fires = %d then %d, want idle at 2", n, got)
	}
}

func TestFlushLoopRecordTrafficArms(t *testing.T) {
	repo := &lockedRepo{}
	svc := newFlushService(repo, 5*time.Millisecond)
	svc.SetTrafficRepo(repo)
	svc.Start()
	defer svc.Close(context.Background())

	svc.RecordTraffic(Event{Path: "/p"}, "direct", "")
	waitFor(t, "traffic flush", func() bool { _, n := repo.get(); return n > 0 })
}
