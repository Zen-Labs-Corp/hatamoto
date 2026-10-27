package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
)

// TestDrainMode locks the WORKER_DRAIN env contract. Used by the Path B
// multi-tenancy strip procedure to halt workers before destructive DB
// migrations. False positives (workers stop when they shouldn't) and false
// negatives (workers keep running when they should stop) are both bad —
// the operator decides via env, this just translates it.
func TestDrainMode(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		{"unset", "", false},
		{"explicit false", "false", false},
		{"zero", "0", false},
		{"no", "no", false},
		{"off", "off", false},
		{"random string", "drain", false},
		{"one", "1", true},
		{"true lower", "true", true},
		{"true upper", "TRUE", true},
		{"true mixed", "True", true},
		{"yes", "yes", true},
		{"YES", "YES", true},
		{"on", "on", true},
		{"with whitespace", "  true  ", true},
		{"with whitespace no", "  no  ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WORKER_DRAIN", tt.val)
			if got := DrainMode(); got != tt.want {
				t.Fatalf("WORKER_DRAIN=%q: got %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestSchedulerAgentHitsJobs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewScheduler(nil, logger)
	if s.agentHitsPartitionWorker != nil || s.agentHitsMaintenanceWorker != nil {
		t.Fatal("agent hit workers must be off by default")
	}
	s.SetAgentHitsPartitionWorker(NewAgentHitsPartitionWorker(nil, 2, 3, logger))
	s.SetAgentHitsMaintenanceWorker(NewAgentHitsMaintenanceWorker(nil, 0, logger))
	if s.agentHitsPartitionWorker == nil || s.agentHitsMaintenanceWorker == nil {
		t.Fatal("setters must register the workers")
	}

	// Start registers one daily loop per worker (first run is minutes away);
	// Stop must reap both.
	ctx := t.Context()
	s.Start(ctx)
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestSchedulerAgentHitsDrain(t *testing.T) {
	t.Setenv("WORKER_DRAIN", "true")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewScheduler(nil, logger)
	s.SetAgentHitsMaintenanceWorker(NewAgentHitsMaintenanceWorker(nil, 0, logger))
	s.Start(context.Background())
	// In drain mode no loop is started, so the WaitGroup is empty.
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain mode must not start worker loops")
	}
}

func TestRunEveryRepeatsUntilStopped(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewScheduler(nil, logger)
	var mu sync.Mutex
	runs := 0
	ctx := t.Context()
	s.wg.Add(1)
	go s.runEvery(ctx, time.Millisecond, 5*time.Millisecond, "test", func(context.Context) error {
		mu.Lock()
		runs++
		mu.Unlock()
		return errors.New("errors are logged, never fatal")
	})
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := runs
		mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d runs", n)
		case <-time.After(5 * time.Millisecond):
		}
	}
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runEvery did not stop")
	}
}

func TestSchedulerAgentVerifyJobs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Without workers nothing is registered.
	s := NewScheduler(nil, logger)
	s.Start(context.Background())
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nil workers must register no loops")
	}

	// With workers: two loops, reaped by Stop.
	s = NewScheduler(nil, logger)
	w := agents.NewVerifyWorker(nil, nil, nil, nil, nil, nil, logger)
	s.SetAgentRangesRefreshWorker(w)
	s.SetAgentVerifyWorker(w)
	ctx := t.Context()
	s.Start(ctx)
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestSchedulerAgentVerifyDrain(t *testing.T) {
	t.Setenv("WORKER_DRAIN", "true")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewScheduler(nil, logger)
	w := agents.NewVerifyWorker(nil, nil, nil, nil, nil, nil, logger)
	s.SetAgentRangesRefreshWorker(w)
	s.SetAgentVerifyWorker(w)
	s.Start(context.Background())
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain mode must not start agent verify loops")
	}
}

// pruneCounter counts PruneVersions calls; every other method is nil (never called).
type pruneCounter struct {
	entity.EntityRepository
	calls atomic.Int32
}

func (p *pruneCounter) PruneVersions(context.Context, int) (int64, error) {
	p.calls.Add(1)
	return 0, nil
}

// The public demo prunes old entity versions every few minutes, not daily.
func TestSchedulerEntityVersionInterval(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := &pruneCounter{}
	s := NewScheduler(nil, logger)
	s.SetEntityVersionWorker(NewEntityVersionWorker(repo, 20, logger))
	s.SetEntityVersionInterval(40 * time.Millisecond)
	s.Start(t.Context())
	defer s.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for repo.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := repo.calls.Load(); n < 3 {
		t.Fatalf("the prune ran %d times, want it to repeat", n)
	}
}

func TestSchedulerEntityVersionDailyByDefault(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := &pruneCounter{}
	s := NewScheduler(nil, logger)
	s.SetEntityVersionWorker(NewEntityVersionWorker(repo, 20, logger))
	s.Start(t.Context())
	time.Sleep(100 * time.Millisecond)
	s.Stop()
	if repo.calls.Load() != 0 {
		t.Fatal("without the interval the first run is minutes away")
	}
}
