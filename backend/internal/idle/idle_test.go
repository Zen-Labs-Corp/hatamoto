package idle

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeTimers hands out timers that fire only when the test says so.
type fakeTimers struct {
	asked chan time.Duration
	fire  chan time.Time
}

func newFakeTimers() *fakeTimers {
	return &fakeTimers{asked: make(chan time.Duration, 8), fire: make(chan time.Time, 8)}
}

func (f *fakeTimers) timer(d time.Duration) (<-chan time.Time, func()) {
	f.asked <- d
	return f.fire, func() {}
}

func testGate(after, beat time.Duration) (*Gate, *fakeClock, *fakeTimers) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	g := New(after, beat, clk.Now)
	ft := newFakeTimers()
	g.SetTimer(ft.timer)
	return g, clk, ft
}

// waitAsync runs Wait and delivers its result.
func waitAsync(g *Gate, normal time.Duration, due func(context.Context) (time.Time, bool)) <-chan bool {
	out := make(chan bool, 1)
	go func() { out <- g.Wait(context.Background(), normal, due) }()
	return out
}

func TestBeatClamp(t *testing.T) {
	for _, c := range []struct{ in, want time.Duration }{
		{time.Minute, 5 * time.Minute},
		{2 * time.Minute, 5 * time.Minute},
		{4 * time.Minute, 8 * time.Minute},
		{30 * time.Minute, 15 * time.Minute},
	} {
		if got := Beat(c.in); got != c.want {
			t.Errorf("Beat(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestWaitActiveUsesNormal(t *testing.T) {
	g, _, ft := testGate(2*time.Minute, 5*time.Minute)
	res := waitAsync(g, 10*time.Second, nil)
	if d := <-ft.asked; d != 10*time.Second {
		t.Fatalf("active wait = %s, want 10s", d)
	}
	ft.fire <- time.Time{}
	if !<-res {
		t.Fatal("Wait returned false without cancel")
	}
}

func TestWaitIdleAlignsToBeat(t *testing.T) {
	g, clk, ft := testGate(2*time.Minute, 5*time.Minute)
	clk.Advance(3 * time.Minute) // idle; beat at +5m
	res := waitAsync(g, 10*time.Second, nil)
	if d := <-ft.asked; d != 2*time.Minute {
		t.Fatalf("idle wait = %s, want 2m (to the beat)", d)
	}
	ft.fire <- time.Time{}
	<-res
	// Right on the beat the next target is the following beat, not zero.
	clk.Advance(2 * time.Minute)
	res = waitAsync(g, 10*time.Second, nil)
	if d := <-ft.asked; d != 5*time.Minute {
		t.Fatalf("wait on the beat = %s, want 5m", d)
	}
	ft.fire <- time.Time{}
	<-res
}

func TestWaitersShareBeat(t *testing.T) {
	g, clk, ft := testGate(2*time.Minute, 5*time.Minute)
	clk.Advance(3*time.Minute + 17*time.Second)
	a := waitAsync(g, 10*time.Second, nil)
	b := waitAsync(g, 30*time.Second, nil)
	da, db := <-ft.asked, <-ft.asked
	if da != db || da != 2*time.Minute-17*time.Second {
		t.Fatalf("waits %s and %s must reach the same beat", da, db)
	}
	ft.fire <- time.Time{}
	ft.fire <- time.Time{}
	<-a
	<-b
}

func TestNextDueEarlierWins(t *testing.T) {
	g, clk, ft := testGate(2*time.Minute, 15*time.Minute)
	clk.Advance(3 * time.Minute)
	due := clk.Now().Add(90 * time.Second)
	res := waitAsync(g, time.Minute, func(context.Context) (time.Time, bool) { return due, true })
	if d := <-ft.asked; d != 90*time.Second {
		t.Fatalf("wait = %s, want 90s (the due time, not the beat)", d)
	}
	ft.fire <- time.Time{}
	<-res
}

func TestNextDueLaterOrMissingKeepsBeat(t *testing.T) {
	g, clk, ft := testGate(2*time.Minute, 5*time.Minute)
	clk.Advance(3 * time.Minute) // beat in 2m
	later := clk.Now().Add(time.Hour)
	for _, due := range []func(context.Context) (time.Time, bool){
		func(context.Context) (time.Time, bool) { return later, true },
		func(context.Context) (time.Time, bool) { return time.Time{}, false },
	} {
		res := waitAsync(g, time.Minute, due)
		if d := <-ft.asked; d != 2*time.Minute {
			t.Fatalf("wait = %s, want the beat (2m)", d)
		}
		ft.fire <- time.Time{}
		<-res
	}
}

func TestOverdueDueDoesNotSpin(t *testing.T) {
	g, clk, ft := testGate(2*time.Minute, 15*time.Minute)
	clk.Advance(3 * time.Minute)
	past := clk.Now().Add(-time.Hour)
	res := waitAsync(g, time.Minute, func(context.Context) (time.Time, bool) { return past, true })
	if d := <-ft.asked; d != time.Minute {
		t.Fatalf("overdue wait = %s, want the normal interval", d)
	}
	ft.fire <- time.Time{}
	<-res
}

func TestTouchWakesIdleWaitersOncePerEdge(t *testing.T) {
	g, clk, ft := testGate(2*time.Minute, 5*time.Minute)
	clk.Advance(3 * time.Minute)
	a := waitAsync(g, time.Second, nil)
	b := waitAsync(g, time.Second, nil)
	<-ft.asked
	<-ft.asked
	g.Touch() // idle -> active
	if !<-a || !<-b {
		t.Fatal("woken waits must report true")
	}
	if g.Idle() {
		t.Fatal("gate must be active after Touch")
	}
	// A Touch while active must not broadcast: its wake channel stays open.
	g.mu.Lock()
	ch := g.wake
	g.mu.Unlock()
	g.Touch()
	select {
	case <-ch:
		t.Fatal("Touch while active closed the wake channel")
	default:
	}
}

func TestActiveWaitIgnoresWake(t *testing.T) {
	g, clk, ft := testGate(2*time.Minute, 5*time.Minute)
	res := waitAsync(g, time.Minute, nil)
	<-ft.asked
	clk.Advance(3 * time.Minute)
	g.Touch() // edge while the active waiter still sleeps
	select {
	case <-res:
		t.Fatal("an active wait must run to its timer")
	default:
	}
	ft.fire <- time.Time{}
	<-res
}

func TestWaitCancel(t *testing.T) {
	g, _, ft := testGate(2*time.Minute, 5*time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan bool, 1)
	go func() { out <- g.Wait(ctx, time.Minute, nil) }()
	<-ft.asked
	cancel()
	if <-out {
		t.Fatal("Wait must return false when ctx ends")
	}
}

func TestPlannedReportsWake(t *testing.T) {
	g, clk, ft := testGate(2*time.Minute, 5*time.Minute)
	clk.Advance(3 * time.Minute)
	got := make(chan time.Time, 1)
	out := make(chan bool, 1)
	go func() {
		out <- g.WaitPlanned(context.Background(), time.Second, nil, func(t time.Time) { got <- t })
	}()
	<-ft.asked
	if want := g.epoch.Add(5 * time.Minute); !(<-got).Equal(want) {
		t.Fatalf("planned wake wrong, want %s", want)
	}
	ft.fire <- time.Time{}
	<-out
}

func TestNilGate(t *testing.T) {
	var g *Gate
	g.Touch()
	if g.Idle() {
		t.Fatal("nil gate is never idle")
	}
	if !g.Wait(context.Background(), time.Nanosecond, nil) {
		t.Fatal("nil gate Wait must behave as a plain timer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if g.Wait(ctx, time.Hour, nil) {
		t.Fatal("nil gate Wait must stop on ctx")
	}
}

func TestZeroAfterNeverIdle(t *testing.T) {
	g, clk, _ := testGate(0, 0)
	clk.Advance(24 * time.Hour)
	if g.Idle() {
		t.Fatal("after <= 0 disables idling")
	}
}
