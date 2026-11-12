package memrelease

import (
	"context"
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
	if c.Tick != 2*time.Second || c.Idle != 10*time.Second || c.MinInterval != 30*time.Second || c.MinReclaim != 8*mib {
		t.Fatalf("defaults = %+v", c)
	}
	if c.Now == nil || c.Sample == nil || c.Free == nil {
		t.Fatal("func defaults missing")
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
