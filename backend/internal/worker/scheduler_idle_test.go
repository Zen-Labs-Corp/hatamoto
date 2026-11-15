package worker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/idle"
)

type idleClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *idleClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *idleClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// idleRig is a scheduler whose gate runs on a fake clock and timers the test
// fires by hand.
type idleRig struct {
	s     *Scheduler
	gate  *idle.Gate
	clk   *idleClock
	asked chan time.Duration
	fire  chan time.Time
	ran   chan struct{}
	ctx   context.Context
}

func newIdleRig(t *testing.T) *idleRig {
	t.Helper()
	r := &idleRig{
		clk:   &idleClock{t: time.Now()},
		asked: make(chan time.Duration, 8),
		fire:  make(chan time.Time, 8),
		ran:   make(chan struct{}, 8),
	}
	r.gate = idle.New(2*time.Minute, 5*time.Minute, r.clk.now)
	r.gate.SetTimer(func(d time.Duration) (<-chan time.Time, func()) {
		r.asked <- d
		return r.fire, func() {}
	})
	r.s = NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.s.SetIdle(r.gate)
	ctx, cancel := context.WithCancel(context.Background())
	r.ctx = ctx
	t.Cleanup(func() { cancel(); r.s.wg.Wait() })
	return r
}

func (r *idleRig) fn(context.Context) error { r.ran <- struct{}{}; return nil }

// startEvery runs id through runEvery with an immediate first run.
func (r *idleRig) startEvery(id string, interval time.Duration) {
	r.s.register(id, interval, 0, r.fn)
	r.s.wg.Add(1)
	go r.s.runEvery(r.ctx, 0, interval, id, r.fn)
}

func (r *idleRig) next(id string) time.Time {
	for _, j := range r.s.Jobs() {
		if j.ID == id {
			return j.Next
		}
	}
	return time.Time{}
}

func TestIdleJobActiveKeepsInterval(t *testing.T) {
	r := newIdleRig(t)
	r.s.OnIdle("webhook_deliver", nil)
	r.startEvery("webhook_deliver", 10*time.Second)
	<-r.ran
	if d := <-r.asked; d != 10*time.Second {
		t.Fatalf("active wait = %s, want the configured 10s", d)
	}
	r.fire <- time.Time{}
	<-r.ran
}

func TestIdleJobRunsOnBeatAndShowsNext(t *testing.T) {
	r := newIdleRig(t)
	r.s.OnIdle("webhook_deliver", nil)
	start := r.clk.now()
	r.startEvery("webhook_deliver", 10*time.Second)
	<-r.ran
	<-r.asked
	r.fire <- time.Time{}
	<-r.ran

	r.clk.advance(3 * time.Minute) // idle; the beat is 5m after start
	if d := <-r.asked; d != 10*time.Second {
		// the wait asked before the clock moved is still the active one
		t.Fatalf("unexpected wait %s", d)
	}
	r.fire <- time.Time{}
	<-r.ran
	d := <-r.asked
	if want := 2 * time.Minute; d != want {
		t.Fatalf("idle wait = %s, want %s (to the beat)", d, want)
	}
	if got, want := r.next("webhook_deliver"), start.Add(5*time.Minute); !got.Equal(want) {
		t.Fatalf("Jobs Next = %s, want the planned wake %s", got, want)
	}
	r.fire <- time.Time{}
	<-r.ran
}

func TestIdleJobWakesOnRequest(t *testing.T) {
	r := newIdleRig(t)
	r.s.OnIdle("webhook_deliver", nil)
	r.startEvery("webhook_deliver", 10*time.Second)
	<-r.ran
	<-r.asked
	r.clk.advance(3 * time.Minute)
	r.fire <- time.Time{}
	<-r.ran
	<-r.asked // now waiting idle for the beat
	r.gate.Touch()
	<-r.ran // woken without the timer firing
	if d := <-r.asked; d != 10*time.Second {
		t.Fatalf("after the wake the wait = %s, want the active 10s", d)
	}
}

func TestIdleJobNextDueShortensWait(t *testing.T) {
	r := newIdleRig(t)
	due := r.clk.now().Add(3*time.Minute + 90*time.Second)
	r.s.OnIdle("publish", func(context.Context) (time.Time, bool) { return due, true })
	r.startEvery("publish", time.Minute)
	<-r.ran
	<-r.asked
	r.clk.advance(3 * time.Minute)
	r.fire <- time.Time{}
	<-r.ran
	if d := <-r.asked; d != 90*time.Second {
		t.Fatalf("idle wait = %s, want 90s (the due time, before the beat)", d)
	}
	if got := r.next("publish"); !got.Equal(due) {
		t.Fatalf("Jobs Next = %s, want %s", got, due)
	}
	r.fire <- time.Time{}
	<-r.ran
}

func TestIdleJobJitteredPath(t *testing.T) {
	r := newIdleRig(t)
	old := minJitterInterval
	minJitterInterval = time.Millisecond
	t.Cleanup(func() { minJitterInterval = old })
	oldRand := jitterRand
	jitterRand = func(int64) int64 { return 0 } // lowest offset
	t.Cleanup(func() { jitterRand = oldRand })

	r.s.OnIdle("webhook_deliver", nil)
	r.s.runCtx = r.ctx
	if err := r.s.ScheduleJitter("webhook_deliver", 10*time.Second, 2*time.Second, 0, r.fn); err != nil {
		t.Fatal(err)
	}
	<-r.ran
	if d := <-r.asked; d != 8*time.Second {
		t.Fatalf("active wait = %s, want every-jitter = 8s", d)
	}
}

func TestNonOptedJobKeepsTicker(t *testing.T) {
	r := newIdleRig(t)
	r.startEvery("prune", 5*time.Millisecond)
	for range 3 {
		<-r.ran
	}
	select {
	case d := <-r.asked:
		t.Fatalf("a job that did not opt in waited on the gate (%s)", d)
	default:
	}
}

func TestPluginJobCannotOptIn(t *testing.T) {
	r := newIdleRig(t)
	r.s.OnIdle("plugin:a:x", nil)
	if _, gated := r.s.idleJob("plugin:a:x"); gated {
		t.Fatal("plugin jobs must never wait on the idle gate")
	}
}
