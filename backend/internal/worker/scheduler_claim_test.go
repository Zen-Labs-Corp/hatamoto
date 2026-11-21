package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClaimer struct {
	mu     sync.Mutex
	won    bool
	err    error
	ids    []string
	leases []time.Duration
}

func (f *fakeClaimer) Claim(_ context.Context, id string, lease time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
	f.leases = append(f.leases, lease)
	return f.won, f.err
}

func claimTestScheduler(c jobClaimer, log io.Writer) (*Scheduler, *fakeRecorder) {
	s := NewScheduler(nil, slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	rec := &fakeRecorder{}
	s.SetRecorder(rec)
	s.SetClaimer(c)
	return s, rec
}

func countingFn(n *int) func(context.Context) error {
	return func(context.Context) error { *n++; return nil }
}

func TestRunJobLostClaimSkips(t *testing.T) {
	c := &fakeClaimer{}
	s, rec := claimTestScheduler(c, io.Discard)
	var n int
	s.runJob(context.Background(), "prune", countingFn(&n))
	if n != 0 || len(rec.all()) != 0 || len(c.ids) != 1 {
		t.Fatalf("runs = %d, recorded = %d, claims = %v", n, len(rec.all()), c.ids)
	}
}

func TestRunJobWonClaimRuns(t *testing.T) {
	c := &fakeClaimer{won: true}
	s, rec := claimTestScheduler(c, io.Discard)
	var n int
	s.runJob(context.Background(), "prune", countingFn(&n))
	if n != 1 || len(rec.all()) != 1 {
		t.Fatalf("runs = %d, recorded = %d", n, len(rec.all()))
	}
}

func TestRunJobClaimErrorFailsOpen(t *testing.T) {
	var buf bytes.Buffer
	s, _ := claimTestScheduler(&fakeClaimer{err: errors.New("db down")}, &buf)
	var n int
	s.runJob(context.Background(), "prune", countingFn(&n))
	if n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "job claim failed") {
		t.Fatalf("no warning logged: %q", buf.String())
	}
}

func TestRunJobUnclaimedIDsSkipTheClaimer(t *testing.T) {
	c := &fakeClaimer{}
	s, _ := claimTestScheduler(c, io.Discard)
	var n int
	for _, id := range []string{"publish", "core:preload"} {
		s.runJob(context.Background(), id, countingFn(&n))
	}
	if n != 2 || len(c.ids) != 0 {
		t.Fatalf("runs = %d, claims = %v", n, c.ids)
	}
}

func TestRunJobPluginJobUsesLease(t *testing.T) {
	c := &fakeClaimer{won: true}
	s, _ := claimTestScheduler(c, io.Discard)
	const id = "plugin:x:y"
	s.register(id, 10*time.Minute, 0, func(context.Context) error { return nil })
	var n int
	s.runJob(context.Background(), id, countingFn(&n))
	if n != 1 || len(c.ids) != 1 || c.ids[0] != id || c.leases[0] != claimLease(10*time.Minute, 0) {
		t.Fatalf("runs = %d, claims = %v, leases = %v", n, c.ids, c.leases)
	}
}

func TestRunNowIgnoresClaims(t *testing.T) {
	s, rec := startedScheduler(t)
	c := &fakeClaimer{}
	s.SetClaimer(c)
	var n int
	scheduleFar(t, s, "prune", countingFn(&n))
	r, err := s.RunNow(context.Background(), "prune")
	if err != nil || !r.OK || n != 1 || len(c.ids) != 0 || len(rec.all()) != 1 {
		t.Fatalf("run = %+v, %v, runs = %d, claims = %v", r, err, n, c.ids)
	}
}

func TestNewSchedulerWithoutDatabaseHasNoClaimer(t *testing.T) {
	if s := NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))); s.claims != nil {
		t.Fatal("claims set without a database")
	}
}
