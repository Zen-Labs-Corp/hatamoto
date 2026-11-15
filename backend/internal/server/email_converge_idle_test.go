package server

import (
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/internal/idle"
)

func TestEmailConvergenceWaitsForBeatWhileIdleAndWakesOnRequest(t *testing.T) {
	now := time.Unix(1_000, 0)
	g := idle.New(time.Minute, 5*time.Minute, func() time.Time { return now })
	waits := make(chan time.Duration, 4)
	fire := make(chan time.Time, 4)
	g.SetTimer(func(d time.Duration) (<-chan time.Time, func()) {
		waits <- d
		return fire, func() {}
	})
	synced := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		loopEmailConvergence(ctx, g, 30*time.Second, func(context.Context) { synced <- struct{}{} })
		close(done)
	}()

	// Active: the normal interval.
	if d := <-waits; d != 30*time.Second {
		t.Fatalf("active wait = %v, want 30s", d)
	}
	now = now.Add(2 * time.Minute) // idle now
	fire <- now
	<-synced
	// Idle: no sync until the beat; the wait reaches the next beat boundary.
	if d := <-waits; d <= 30*time.Second || d > 5*time.Minute {
		t.Fatalf("idle wait = %v, want up to the 5m beat", d)
	}
	select {
	case <-synced:
		t.Fatal("synced before the beat while idle")
	default:
	}
	g.Touch() // first request after idle wakes the loop
	<-synced
	if d := <-waits; d != 30*time.Second {
		t.Fatalf("wait after wake = %v, want the normal 30s", d)
	}
	cancel()
	<-done
}

func TestEmailConvergenceNilGateKeepsPlainTimer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	loopEmailConvergence(ctx, nil, time.Millisecond, func(context.Context) {
		if n++; n == 3 {
			cancel()
		}
	})
	if n != 3 {
		t.Fatalf("syncs = %d, want 3", n)
	}
}
