package statictrigger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeClock is the injected timer seam: timers only fire when a test says so.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (f *fakeTimer) Stop() bool { was := !f.stopped; f.stopped = true; return was }

func (c *fakeClock) afterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, ft)
	return ft
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// live is the timers that were armed and not stopped.
func (c *fakeClock) live() []*fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*fakeTimer
	for _, ft := range c.timers {
		if !ft.stopped {
			out = append(out, ft)
		}
	}
	return out
}

// fireLast fires the newest live timer, as the real clock would.
func (c *fakeClock) fireLast(t *testing.T) {
	t.Helper()
	l := c.live()
	if len(l) == 0 {
		t.Fatal("no armed timer")
	}
	ft := l[len(l)-1]
	ft.stopped = true
	ft.f()
}

type call struct {
	full   bool
	reason string
}

type rig struct {
	tr      *Trigger
	clock   *fakeClock
	static  bool
	started chan call     // a run began
	release chan error    // lets a blocked run return this error
	block   bool          // runs wait for release
	logs    *recordedLogs // records
	mu      sync.Mutex
}

type recordedLogs struct {
	mu    sync.Mutex
	warns int
}

func (l *recordedLogs) Enabled(context.Context, slog.Level) bool { return true }
func (l *recordedLogs) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		l.mu.Lock()
		l.warns++
		l.mu.Unlock()
	}
	return nil
}
func (l *recordedLogs) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *recordedLogs) WithGroup(string) slog.Handler      { return l }

func newRig(t *testing.T, block bool) *rig {
	t.Helper()
	r := &rig{
		clock:   &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)},
		static:  true,
		started: make(chan call, 16),
		release: make(chan error, 16),
		block:   block,
		logs:    &recordedLogs{},
	}
	r.tr = New(nil, func(_ context.Context, full bool, reason string) error {
		r.started <- call{full, reason}
		if r.block {
			return <-r.release
		}
		return nil
	}, func(context.Context) (bool, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.static, nil
	}, slog.New(r.logs))
	r.tr.afterFunc, r.tr.now = r.clock.afterFunc, r.clock.Now
	t.Cleanup(r.tr.Close)
	return r
}

func (r *rig) setStatic(on bool) { r.mu.Lock(); r.static = on; r.mu.Unlock() }

func (r *rig) noRun(t *testing.T) {
	t.Helper()
	r.tr.wait()
	select {
	case c := <-r.started:
		t.Fatalf("unexpected run %+v", c)
	default:
	}
}

func TestBurstMakesOneRun(t *testing.T) {
	r := newRig(t, false)
	for i := 0; i < 5; i++ {
		r.tr.Notify("change", false)
	}
	if n := len(r.clock.live()); n != 1 {
		t.Fatalf("%d armed timers, want 1", n)
	}
	if d := r.clock.live()[0].d; d != Window {
		t.Errorf("window = %v, want %v", d, Window)
	}
	r.noRun(t)
	r.clock.fireLast(t)
	if c := <-r.started; c.full || c.reason != "change" {
		t.Errorf("run = %+v", c)
	}
	r.noRun(t)
}

func TestNotifyDuringRunFollowsOnce(t *testing.T) {
	r := newRig(t, true)
	r.tr.Notify("a", false)
	r.clock.fireLast(t)
	<-r.started // run 1 is blocked

	r.tr.Notify("b", false)
	r.tr.Notify("b", false)
	r.clock.fireLast(t) // the window closes while run 1 is still going
	r.release <- nil    // run 1 ends
	if c := <-r.started; c.reason != "b" {
		t.Errorf("follow-up = %+v", c)
	}
	r.release <- nil
	r.noRun(t)
}

func TestNotifyDuringRunFiredAfterItEnds(t *testing.T) {
	r := newRig(t, true)
	r.tr.Notify("a", false)
	r.clock.fireLast(t)
	<-r.started
	r.tr.Notify("b", false)
	r.release <- nil
	r.tr.wait()
	r.noRun(t) // the window of "b" has not closed yet
	r.clock.fireLast(t)
	if c := <-r.started; c.reason != "b" {
		t.Errorf("follow-up = %+v", c)
	}
	r.release <- nil
	r.noRun(t)
}

func TestFullWinsTheWindow(t *testing.T) {
	r := newRig(t, false)
	r.tr.Notify("entity", false)
	r.tr.Notify("theme", true)
	r.tr.Notify("entity", false)
	r.clock.fireLast(t)
	if c := <-r.started; !c.full || c.reason != "entity,theme" {
		t.Errorf("run = %+v", c)
	}
	// The next window starts incremental again.
	r.tr.wait()
	r.tr.Notify("entity", false)
	r.clock.fireLast(t)
	if c := <-r.started; c.full {
		t.Errorf("second run = %+v, want incremental", c)
	}
}

func TestServedArmsNothing(t *testing.T) {
	r := newRig(t, false)
	r.setStatic(false)
	for i := 0; i < 3; i++ {
		r.tr.Notify("change", true)
	}
	if n := len(r.clock.live()); n != 0 {
		t.Fatalf("%d timers armed in served mode", n)
	}
	r.noRun(t)
}

func TestModeFlipBeforeFireDropsTheRun(t *testing.T) {
	r := newRig(t, false)
	r.tr.Notify("change", false)
	r.setStatic(false)
	r.clock.fireLast(t)
	r.noRun(t)
	// Static again: a later change works (the pending one was dropped, not replayed).
	r.setStatic(true)
	r.tr.Refresh()
	r.tr.Notify("later", false)
	r.clock.fireLast(t)
	if c := <-r.started; c.reason != "later" {
		t.Errorf("run = %+v", c)
	}
}

func TestModeIsCachedForAWindowAndRefreshed(t *testing.T) {
	r := newRig(t, false)
	reads := 0
	r.tr.active = func(context.Context) (bool, error) { reads++; return true, nil }
	r.tr.Notify("a", false)
	r.tr.Notify("b", false)
	if reads != 1 {
		t.Fatalf("%d reads for two notifications, want 1", reads)
	}
	r.clock.advance(Window)
	r.tr.Notify("c", false)
	if reads != 2 {
		t.Errorf("%d reads after the TTL, want 2", reads)
	}
	r.tr.Refresh()
	r.tr.Notify("d", false)
	if reads != 3 {
		t.Errorf("%d reads after Refresh, want 3", reads)
	}
}

func TestCapStartsARunDespiteConstantEdits(t *testing.T) {
	r := newRig(t, false)
	limit := r.clock.Now().Add(5 * Window)
	for i := 0; i < 12; i++ { // six windows of edits every half window
		r.tr.Notify("edit", false)
		l := r.clock.live()
		if due := r.clock.Now().Add(l[len(l)-1].d); due.After(limit) {
			t.Fatalf("edit %d: the run is due at %v, after the cap %v", i, due, limit)
		}
		if due := r.clock.Now().Add(l[len(l)-1].d); !due.After(r.clock.Now().Add(Window / 2)) {
			break // the timer fires before the next edit
		}
		r.clock.advance(Window / 2)
	}
	if got := r.clock.Now().Sub(limit.Add(-5 * Window)); got > 5*Window {
		t.Fatalf("edits went on %v without a run", got)
	}
	r.clock.fireLast(t)
	<-r.started
}

func TestNow(t *testing.T) {
	r := newRig(t, true)
	if err := r.tr.Now("mode", true); err != nil {
		t.Fatal(err)
	}
	if c := <-r.started; !c.full || c.reason != "mode" {
		t.Errorf("run = %+v", c)
	}
	if err := r.tr.Now("rebuild", false); !errors.Is(err, ErrQueued) {
		t.Fatalf("Now during a run = %v, want ErrQueued", err)
	}
	r.release <- nil
	if c := <-r.started; c.reason != "rebuild" {
		t.Errorf("follow-up = %+v", c)
	}
	r.release <- nil
	r.noRun(t)
}

func TestRetryRearmsOneWindow(t *testing.T) {
	r := newRig(t, true)
	r.tr.Notify("theme", true)
	r.clock.fireLast(t)
	<-r.started
	r.release <- fmt.Errorf("export: %w", ErrRetry)
	r.tr.wait()
	l := r.clock.live()
	if len(l) != 1 || l[0].d != Window {
		t.Fatalf("live timers after a busy answer: %d", len(l))
	}
	r.clock.fireLast(t)
	if c := <-r.started; !c.full || c.reason != "theme" {
		t.Errorf("retry run = %+v", c)
	}
	r.release <- nil
	r.noRun(t)
	if r.logs.warns != 0 {
		t.Errorf("%d warnings for a busy answer", r.logs.warns)
	}
}

func TestFailureIsLoggedOnce(t *testing.T) {
	r := newRig(t, true)
	r.tr.Notify("a", false)
	r.clock.fireLast(t)
	<-r.started
	r.release <- errors.New("boom")
	r.noRun(t)
	if r.logs.warns != 1 {
		t.Errorf("%d warnings, want 1", r.logs.warns)
	}
}

func TestNilTrigger(t *testing.T) {
	var tr *Trigger
	tr.Notify("x", true)
	tr.Refresh()
	tr.Close()
	if err := tr.Now("x", true); err != nil {
		t.Errorf("Now on nil = %v", err)
	}
}

func TestCloseStopsThePendingWindow(t *testing.T) {
	r := newRig(t, false)
	r.tr.Notify("a", false)
	r.tr.Close()
	if n := len(r.clock.live()); n != 0 {
		t.Errorf("%d timers live after Close", n)
	}
	r.tr.Notify("b", false)
	if err := r.tr.Now("c", false); err != nil {
		t.Fatal(err)
	}
	r.noRun(t)
}
