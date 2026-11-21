package memrelease

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

const mib = 1 << 20

type fixture struct {
	now      time.Time
	activity time.Time
	sample   Sample
	frees    int
	r        *releaser
}

func newFixture() *fixture {
	f := &fixture{now: time.Unix(1000, 0), sample: Sample{InUse: 40 * mib}}
	f.activity = f.now.Add(-time.Hour)
	c := Config{
		LastActivity: func() time.Time { return f.activity },
		Sample:       func() Sample { return f.sample },
		Free: func() {
			f.frees++
			f.sample = Sample{InUse: f.sample.InUse}
		},
	}
	c.fill()
	f.r = &releaser{c: c, baseline: f.sample.InUse}
	return f
}

func (f *fixture) step(d time.Duration) {
	f.now = f.now.Add(d)
	f.r.step(f.now)
}

func TestReleaserBelowThresholdDoesNothing(t *testing.T) {
	f := newFixture()
	f.sample = Sample{FreeUnreleased: 7 * mib, InUse: 40*mib + 7*mib}
	f.step(time.Minute)
	if f.frees != 0 {
		t.Fatalf("frees = %d, want 0", f.frees)
	}
}

func TestReleaserFiresOnFreeMemoryOnce(t *testing.T) {
	f := newFixture()
	f.sample.FreeUnreleased = 9 * mib
	f.step(time.Minute)
	if f.frees != 1 {
		t.Fatalf("frees = %d, want 1", f.frees)
	}
	f.step(time.Minute)
	if f.frees != 1 {
		t.Fatalf("frees after quiet tick = %d, want 1", f.frees)
	}
}

func TestReleaserFiresOnHeapGrowth(t *testing.T) {
	f := newFixture()
	f.sample.InUse = 49 * mib
	f.step(time.Minute)
	if f.frees != 1 {
		t.Fatalf("frees = %d, want 1", f.frees)
	}
	if f.r.baseline != 49*mib {
		t.Fatalf("baseline = %d, want reset to current in-use", f.r.baseline)
	}
}

func TestReleaserWaitsForIdle(t *testing.T) {
	f := newFixture()
	f.sample.FreeUnreleased = 20 * mib
	f.activity = f.now.Add(time.Minute - 3*time.Second)
	f.step(time.Minute)
	if f.frees != 0 {
		t.Fatalf("released while busy: %d", f.frees)
	}
	f.step(10 * time.Second)
	if f.frees != 1 {
		t.Fatalf("frees = %d after idle, want 1", f.frees)
	}
}

func TestReleaserMinInterval(t *testing.T) {
	f := newFixture()
	f.sample.FreeUnreleased = 20 * mib
	f.step(time.Minute)
	f.sample.FreeUnreleased = 20 * mib
	f.step(10 * time.Second)
	if f.frees != 1 {
		t.Fatalf("frees = %d inside MinInterval, want 1", f.frees)
	}
	f.step(30 * time.Second)
	if f.frees != 2 {
		t.Fatalf("frees = %d after MinInterval, want 2", f.frees)
	}
}

func TestReleaserNilActivityIsIdle(t *testing.T) {
	f := newFixture()
	f.r.c.LastActivity = nil
	f.sample.FreeUnreleased = 20 * mib
	f.step(0)
	if f.frees != 1 {
		t.Fatalf("frees = %d, want 1", f.frees)
	}
}

func TestConfigDefaults(t *testing.T) {
	var c Config
	c.fill()
	if c.Tick != 2*time.Second || c.Idle != 10*time.Second || c.MinInterval != 30*time.Second || c.MinReclaim != 8*mib || c.Park != 30*time.Second {
		t.Fatalf("defaults = %+v", c)
	}
	if c.Now == nil || c.Sample == nil || c.Free == nil {
		t.Fatal("func defaults missing")
	}
}

func TestStepOutcomes(t *testing.T) {
	f := newFixture()
	f.sample = Sample{FreeUnreleased: 7 * mib, InUse: 40*mib + 7*mib}
	f.now = f.now.Add(time.Minute)
	if got := f.r.step(f.now); got != belowThreshold {
		t.Fatalf("got %v, want belowThreshold", got)
	}
	f.sample = Sample{FreeUnreleased: 20 * mib, InUse: 40 * mib}
	f.activity = f.now.Add(-3 * time.Second)
	if got := f.r.step(f.now); got != busy {
		t.Fatalf("got %v, want busy", got)
	}
	f.activity = f.now.Add(-time.Hour)
	if got := f.r.step(f.now); got != released {
		t.Fatalf("got %v, want released", got)
	}
	f.now = f.now.Add(10 * time.Second)
	f.sample.FreeUnreleased = 20 * mib
	if got := f.r.step(f.now); got != tooSoon {
		t.Fatalf("got %v, want tooSoon", got)
	}
}

func TestNextSleepsUntilIdleDeadline(t *testing.T) {
	f := newFixture()
	f.activity = f.now.Add(-3 * time.Second)
	if got := f.r.next(f.now, busy); got != 7*time.Second {
		t.Fatalf("next = %v, want 7s", got)
	}
}

func TestNextTooSoonWaitsForMinInterval(t *testing.T) {
	f := newFixture()
	f.sample.FreeUnreleased = 20 * mib
	f.step(time.Minute)
	f.sample.FreeUnreleased = 20 * mib
	f.now = f.now.Add(10 * time.Second)
	o := f.r.step(f.now)
	if o != tooSoon {
		t.Fatalf("outcome = %v, want tooSoon", o)
	}
	if got := f.r.next(f.now, o); got != 20*time.Second {
		t.Fatalf("next = %v, want 20s", got)
	}
}

func TestNextParksWhenIdle(t *testing.T) {
	f := newFixture()
	if got := f.r.next(f.now, belowThreshold); got != 30*time.Second {
		t.Fatalf("next = %v, want Park", got)
	}
	if got := f.r.next(f.now, released); got != 30*time.Second {
		t.Fatalf("next after release = %v, want Park", got)
	}
}

func TestNextNeverBelowTick(t *testing.T) {
	f := newFixture()
	f.activity = f.now.Add(-f.r.c.Idle + time.Millisecond)
	if got := f.r.next(f.now, busy); got != f.r.c.Tick {
		t.Fatalf("next = %v, want Tick", got)
	}
}

func TestNextNilActivityKeepsTick(t *testing.T) {
	f := newFixture()
	f.r.c.LastActivity = nil
	if got := f.r.next(f.now, belowThreshold); got != f.r.c.Tick {
		t.Fatalf("next = %v, want Tick", got)
	}
}

func TestRuntimeSampleReadsHeap(t *testing.T) {
	if s := runtimeSample(); s.InUse == 0 {
		t.Fatalf("InUse = 0: %+v", s)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, Config{Tick: time.Millisecond, Sample: func() Sample { return Sample{} }, Free: func() {}})
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// runCounting runs Run with a fixed LastActivity and counts Sample and Free calls.
func runCounting(t *testing.T, c Config, free uint64) (samples, frees *atomic.Int64, stop func()) {
	t.Helper()
	samples, frees = new(atomic.Int64), new(atomic.Int64)
	c.Sample = func() Sample {
		samples.Add(1)
		if frees.Load() > 0 {
			return Sample{InUse: 40 * mib}
		}
		return Sample{FreeUnreleased: free, InUse: 40 * mib}
	}
	c.Free = func() { frees.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Run(ctx, c); close(done) }()
	return samples, frees, func() {
		cancel()
		<-done
	}
}

func TestRunReleasesAfterIdleDeadline(t *testing.T) {
	start := time.Now()
	_, frees, stop := runCounting(t, Config{
		Tick: time.Millisecond, Idle: 40 * time.Millisecond, MinInterval: time.Hour, Park: time.Hour,
		LastActivity: func() time.Time { return start },
	}, 20*mib)
	defer stop()
	for frees.Load() == 0 {
		if time.Since(start) > time.Second {
			t.Fatal("no release within 1s")
		}
		time.Sleep(time.Millisecond)
	}
	if el := time.Since(start); el < 40*time.Millisecond {
		t.Fatalf("released after %v, before the idle deadline", el)
	}
	time.Sleep(50 * time.Millisecond)
	if n := frees.Load(); n != 1 {
		t.Fatalf("frees = %d, want 1", n)
	}
}

func TestRunParksWhenIdle(t *testing.T) {
	start := time.Now()
	samples, frees, stop := runCounting(t, Config{
		Tick: time.Millisecond, Idle: 40 * time.Millisecond, MinInterval: time.Hour, Park: time.Hour,
		LastActivity: func() time.Time { return start },
	}, 20*mib)
	defer stop()
	for frees.Load() == 0 {
		if time.Since(start) > time.Second {
			t.Fatal("no release within 1s")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	after := samples.Load()
	if after > 5 {
		t.Fatalf("%d samples by the release, polling every Tick", after)
	}
	time.Sleep(100 * time.Millisecond)
	if got := samples.Load(); got != after {
		t.Fatalf("samples grew from %d to %d while parked", after, got)
	}
}

func TestRunBusyDoesNotPollEveryTick(t *testing.T) {
	samples, _, stop := runCounting(t, Config{
		Tick: time.Millisecond, Idle: 50 * time.Millisecond, MinInterval: time.Hour, Park: time.Hour,
		LastActivity: time.Now,
	}, 20*mib)
	time.Sleep(120 * time.Millisecond)
	stop()
	if n := samples.Load(); n > 5 {
		t.Fatalf("%d samples in 120ms while busy, want <= 5", n)
	}
}

func TestNextCappedAtPark(t *testing.T) {
	f := newFixture()
	f.activity = f.now.Add(time.Hour) // wall clock stepped back after the request
	if got := f.r.next(f.now, busy); got != 30*time.Second {
		t.Fatalf("next = %v, want Park", got)
	}
}

func TestRunFirstSampleAtTickWhenIdleAtStart(t *testing.T) {
	start := time.Now()
	_, frees, stop := runCounting(t, Config{
		Tick: time.Millisecond, Idle: 40 * time.Millisecond, MinInterval: time.Hour, Park: time.Hour,
		LastActivity: func() time.Time { return start.Add(-time.Hour) },
	}, 20*mib)
	defer stop()
	for frees.Load() == 0 {
		if time.Since(start) > time.Second {
			t.Fatal("idle process not released within 1s of start")
		}
		time.Sleep(time.Millisecond)
	}
}
