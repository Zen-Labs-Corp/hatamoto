package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestSchedulerSkipsPublishLoopWhenWorkerNil guards the optional-worker
// contract: Start must not spawn the publish loop (and Stop must not panic
// on a nil ticker) when SetPublishWorker was never called. Every other
// worker on this scheduler is optional the same way; a nil deref here would
// take down the whole API process at boot, not just scheduled publishing.
func TestSchedulerSkipsPublishLoopWhenWorkerNil(t *testing.T) {
	t.Setenv("WORKER_DRAIN", "")

	s := NewScheduler(nil, quietLogger())
	if s.publishWorker != nil {
		t.Fatal("publishWorker should be nil before SetPublishWorker")
	}

	s.Start(context.Background())
	// No loop was started, so Stop must return promptly rather than block
	// on a WaitGroup that never drains.
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked with no worker registered")
	}
}

// TestSetPublishWorkerRegisters locks the setter, since Start's decision to
// spawn the loop keys off this field being non-nil.
func TestSetPublishWorkerRegisters(t *testing.T) {
	s := NewScheduler(nil, quietLogger())
	s.SetPublishWorker(NewPublishWorker(nil, quietLogger()))
	if s.publishWorker == nil {
		t.Fatal("SetPublishWorker did not register the worker")
	}
}

// TestDrainModeSuppressesPublishLoop confirms scheduled publishing honours
// the same WORKER_DRAIN kill switch as every other loop. The drain is used
// before destructive migrations; a publish loop that ignored it would write
// to hatamoto_entities mid-ALTER.
func TestDrainModeSuppressesPublishLoop(t *testing.T) {
	t.Setenv("WORKER_DRAIN", "true")

	s := NewScheduler(nil, quietLogger())
	s.SetPublishWorker(NewPublishWorker(nil, quietLogger()))
	s.Start(context.Background())

	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked: a loop started despite WORKER_DRAIN=true")
	}
}
