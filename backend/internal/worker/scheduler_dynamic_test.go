package worker

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduleDynamicJobs(t *testing.T) {
	s := NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.Schedule("plugin:a:x", time.Second, func(context.Context) error { return nil }); err == nil ||
		!strings.Contains(err.Error(), "minimum") {
		t.Fatalf("a job under a minute must be refused, got %v", err)
	}

	old := minDynamicInterval
	minDynamicInterval = 10 * time.Millisecond
	t.Cleanup(func() { minDynamicInterval = old })

	var a, b atomic.Int32
	// Added before Start: begins with it.
	if err := s.Schedule("plugin:a:x", 10*time.Millisecond, func(context.Context) error { a.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	s.Start(t.Context())
	// Added while running: starts at once.
	if err := s.Schedule("plugin:b:y", 10*time.Millisecond, func(context.Context) error { b.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return a.Load() >= 2 && b.Load() >= 2 })

	// Unschedule by prefix stops only matching jobs.
	s.Unschedule("plugin:a:")
	time.Sleep(30 * time.Millisecond)
	frozen := a.Load()
	before := b.Load()
	time.Sleep(60 * time.Millisecond)
	if a.Load() != frozen {
		t.Fatalf("plugin a still runs after Unschedule: %d -> %d", frozen, a.Load())
	}
	if b.Load() <= before {
		t.Fatal("plugin b stopped by an unrelated Unschedule")
	}

	s.Stop()
	if err := s.Schedule("plugin:c:z", 10*time.Millisecond, func(context.Context) error { return nil }); err == nil {
		t.Fatal("Schedule after Stop must fail")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
