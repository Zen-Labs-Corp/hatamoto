package pluginhost

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"hatamoto.dev/core/internal/idle"
)

// While the install is idle the recovery pass waits for the idle beat, not
// for its own interval, and a kick runs it early.
func TestRecoverLoopWaitsOnIdleGate(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	g := idle.New(2*time.Minute, 10*time.Minute, func() time.Time { return now })
	asked := make(chan time.Duration, 4)
	g.SetTimer(func(d time.Duration) (<-chan time.Time, func()) {
		asked <- d
		return make(chan time.Time), func() {} // never fires
	})
	now = now.Add(3 * time.Minute)

	ran := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		recoverLoop(ctx, g, time.Minute, func(context.Context) error { ran <- struct{}{}; return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	if d := <-asked; d <= time.Minute {
		t.Fatalf("idle wait = %s, want the idle beat, longer than the 1m interval", d)
	}
	g.Kick()
	<-ran
	<-asked
	cancel()
	<-done
}

func TestRecoverLoopNilGateUsesInterval(t *testing.T) {
	ran := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		recoverLoop(ctx, nil, time.Millisecond, func(context.Context) error {
			select {
			case ran <- struct{}{}:
			default:
			}
			return nil
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	<-ran
	cancel()
	<-done
}
