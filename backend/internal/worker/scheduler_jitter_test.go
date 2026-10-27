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
)

func jitterTestScheduler(t *testing.T) *Scheduler {
	t.Helper()
	old := minJitterInterval
	minJitterInterval = 10 * time.Millisecond
	t.Cleanup(func() { minJitterInterval = old })
	return NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestScheduleJitterValidation(t *testing.T) {
	s := NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	nop := func(context.Context) error { return nil }
	if err := s.ScheduleJitter("u", time.Millisecond, 0, 0, nop); err == nil {
		t.Fatal("interval under the minimum must be refused")
	}
	if err := s.ScheduleJitter("u", time.Minute, time.Minute, 0, nop); err == nil {
		t.Fatal("jitter >= interval must be refused")
	}
	if err := s.ScheduleJitter("u", time.Minute, -time.Second, 0, nop); err == nil {
		t.Fatal("negative jitter must be refused")
	}
}

func TestScheduleJitterFirstDelayAndErrors(t *testing.T) {
	s := jitterTestScheduler(t)
	var n atomic.Int32
	var first atomic.Int64
	start := time.Now()
	err := s.ScheduleJitter("u", 20*time.Millisecond, 5*time.Millisecond, 150*time.Millisecond, func(context.Context) error {
		if n.Add(1) == 1 {
			first.Store(int64(time.Since(start)))
		}
		return errors.New("boom") // must not stop the loop
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start(t.Context())
	defer s.Stop()
	waitFor(t, func() bool { return n.Load() >= 3 })
	if d := time.Duration(first.Load()); d < 140*time.Millisecond {
		t.Fatalf("first run after %s, want >= 150ms", d)
	}
}

func TestScheduleJitterBounds(t *testing.T) {
	s := jitterTestScheduler(t)
	var mu sync.Mutex
	var asked []int64
	old := jitterRand
	jitterRand = func(n int64) int64 { mu.Lock(); asked = append(asked, n); mu.Unlock(); return 0 } // lowest offset
	t.Cleanup(func() { jitterRand = old })

	var stamps []time.Time
	var runs atomic.Int32
	err := s.ScheduleJitter("u", 100*time.Millisecond, 40*time.Millisecond, 0, func(context.Context) error {
		mu.Lock()
		stamps = append(stamps, time.Now())
		mu.Unlock()
		runs.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start(t.Context())
	defer s.Stop()
	waitFor(t, func() bool { return runs.Load() >= 3 })
	mu.Lock()
	defer mu.Unlock()
	for _, n := range asked {
		if n != int64(80*time.Millisecond)+1 {
			t.Fatalf("rand asked for %d, want the 2*jitter+1 range", n)
		}
	}
	// With offset 0 -> wait is every - jitter = 60ms.
	if gap := stamps[1].Sub(stamps[0]); gap < 55*time.Millisecond || gap > 95*time.Millisecond {
		t.Fatalf("gap %s outside the jittered lower bound", gap)
	}
}

func TestScheduleJitterReplaceAndUnschedule(t *testing.T) {
	s := jitterTestScheduler(t)
	var a, b atomic.Int32
	s.Start(t.Context())
	defer s.Stop()
	if err := s.ScheduleJitter("update:x", 10*time.Millisecond, 0, 0, func(context.Context) error { a.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return a.Load() >= 2 })
	if err := s.ScheduleJitter("update:x", 10*time.Millisecond, 0, 0, func(context.Context) error { b.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return b.Load() >= 2 })
	time.Sleep(30 * time.Millisecond)
	frozen := a.Load()
	time.Sleep(50 * time.Millisecond)
	if a.Load() != frozen {
		t.Fatal("replaced job still runs")
	}
	s.Unschedule("update:")
	time.Sleep(30 * time.Millisecond)
	frozenB := b.Load()
	time.Sleep(50 * time.Millisecond)
	if b.Load() != frozenB {
		t.Fatal("job still runs after Unschedule")
	}
}
