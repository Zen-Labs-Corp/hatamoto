package packages

import (
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/internal/idle"
)

// idleRig is a gate on a fake clock whose waits the test releases by hand.
type idleRig struct {
	now   time.Time
	gate  *idle.Gate
	waits chan time.Duration
	fire  chan time.Time
}

func newIdleRig() *idleRig {
	r := &idleRig{now: time.Unix(1_000, 0), waits: make(chan time.Duration, 8), fire: make(chan time.Time, 8)}
	r.gate = idle.New(time.Minute, 5*time.Minute, func() time.Time { return r.now })
	r.gate.SetTimer(func(d time.Duration) (<-chan time.Time, func()) {
		r.waits <- d
		return r.fire, func() {}
	})
	return r
}

func TestRunReconcilesOnBeatWhileIdleAndOnWake(t *testing.T) {
	r := newIdleRig()
	m := &Manager{Options: Options{Idle: r.gate}}
	reconciled := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.run(ctx, time.Second, func(context.Context) { reconciled <- struct{}{} }, func(context.Context) {})
		close(done)
	}()

	// Active: the normal one-second cadence. (The developer loop is parked on
	// its own first-read timer and does not wait on the gate yet.)
	if d := <-r.waits; d != time.Second {
		t.Fatalf("active wait = %v, want 1s", d)
	}
	r.now = r.now.Add(2 * time.Minute) // idle
	r.fire <- r.now
	<-reconciled
	if d := <-r.waits; d <= time.Second || d > 5*time.Minute {
		t.Fatalf("idle wait = %v, want up to the 5m beat", d)
	}
	if len(reconciled) != 0 {
		t.Fatal("reconciled before the beat while idle")
	}
	r.gate.Touch() // first request after idle
	<-reconciled
	if d := <-r.waits; d != time.Second {
		t.Fatalf("wait after wake = %v, want 1s", d)
	}
	cancel()
	<-done
}

func TestDeveloperLoopRefreshesOncePerPeriodEvenWhenWoken(t *testing.T) {
	r := newIdleRig()
	m := &Manager{Options: Options{Idle: r.gate}, now: func() time.Time { return r.now }}
	refreshed := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.developerLoop(ctx, 0, func(context.Context) { refreshed <- struct{}{} })
		close(done)
	}()

	<-refreshed // first read
	<-r.waits
	r.now = r.now.Add(10 * time.Minute) // a beat comes round long before 6h
	r.fire <- r.now
	<-r.waits // loop is waiting again: the beat did not refresh
	if len(refreshed) != 0 {
		t.Fatal("refreshed before DeveloperEvery elapsed")
	}
	r.now = r.now.Add(DeveloperEvery)
	r.fire <- r.now
	<-refreshed
	cancel()
	<-done
}
