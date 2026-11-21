// Package memrelease gives heap memory back to the OS once a process has
// been quiet for a while. The Go scavenger returns it slowly on its own; on a
// small host an idle process should shrink promptly after a burst of work.
package memrelease

import (
	"context"
	"runtime/debug"
	"runtime/metrics"
	"time"
)

// Sample is a point-in-time heap reading.
type Sample struct {
	// FreeUnreleased is heap memory that is free but still held from the OS.
	FreeUnreleased uint64
	// InUse is heap memory holding objects, plus the unused tails of spans.
	InUse uint64
}

// Config tunes Run. Zero fields take the documented defaults.
type Config struct {
	Tick        time.Duration // sampling period, default 2s
	Idle        time.Duration // required quiet time before a release, default 10s
	MinInterval time.Duration // minimum time between releases, default 30s
	MinReclaim  uint64        // free bytes, or heap growth, worth a release; default 8 MiB
	Park        time.Duration // longest sleep while idle with nothing to release, default 30s

	Now          func() time.Time // default time.Now
	LastActivity func() time.Time // nil means always idle
	Sample       func() Sample    // default reads runtime/metrics (no stop-the-world)
	Free         func()           // default debug.FreeOSMemory
}

const defaultMinReclaim = 8 << 20

type releaser struct {
	c        Config
	baseline uint64
	lastFree time.Time
	freed    bool
}

func (c *Config) fill() {
	if c.Tick <= 0 {
		c.Tick = 2 * time.Second
	}
	if c.Idle <= 0 {
		c.Idle = 10 * time.Second
	}
	if c.MinInterval <= 0 {
		c.MinInterval = 30 * time.Second
	}
	if c.Park <= 0 {
		c.Park = 30 * time.Second
	}
	if c.Park < c.Tick {
		c.Park = c.Tick
	}
	if c.MinReclaim == 0 {
		c.MinReclaim = defaultMinReclaim
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Sample == nil {
		c.Sample = runtimeSample
	}
	if c.Free == nil {
		c.Free = debug.FreeOSMemory
	}
}

// Run releases memory until ctx ends. It releases when the heap holds
// MinReclaim of free memory or has grown by that much since the last release,
// the process has had no activity for Idle, and MinInterval has passed since
// the previous release. It samples only when a release could be due: at the
// end of the activity's Idle window, at the end of MinInterval, or every
// Park while idle with nothing to release (Tick without LastActivity).
func Run(ctx context.Context, c Config) {
	c.fill()
	r := &releaser{c: c, baseline: c.Sample().InUse}
	// First look after Tick, not next: a process already idle at start would
	// otherwise wait a whole Park before its first release.
	t := time.NewTimer(c.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := c.Now()
			t.Reset(r.next(now, r.step(now)))
		}
	}
}

// outcome says what stopped a step, or that it released.
type outcome int

const (
	belowThreshold outcome = iota // not enough to reclaim
	busy                          // activity within Idle
	tooSoon                       // released less than MinInterval ago
	released
)

// step takes one sample and releases when the conditions hold.
func (r *releaser) step(now time.Time) outcome {
	s := r.c.Sample()
	if s.FreeUnreleased < r.c.MinReclaim && s.InUse < r.baseline+r.c.MinReclaim {
		return belowThreshold
	}
	if r.c.LastActivity != nil && now.Sub(r.c.LastActivity()) < r.c.Idle {
		return busy
	}
	if r.freed && now.Sub(r.lastFree) < r.c.MinInterval {
		return tooSoon
	}
	r.c.Free()
	r.baseline = r.c.Sample().InUse
	r.lastFree = now
	r.freed = true
	return released
}

// next is how long Run sleeps after a step with outcome o. A release needs
// the process idle for Idle, so a busy process is next sampled when that can
// first be true; an idle one with nothing to do is sampled every Park.
// Without LastActivity it keeps the plain Tick. Never below Tick, never above
// Park (a wall clock stepped back must not stall releases).
func (r *releaser) next(now time.Time, o outcome) time.Duration {
	c := r.c
	if c.LastActivity == nil {
		return c.Tick
	}
	var d time.Duration
	switch idleAt := c.LastActivity().Add(c.Idle); {
	case now.Before(idleAt):
		d = idleAt.Sub(now) // busy, or below threshold while busy
	case o == tooSoon:
		d = r.lastFree.Add(c.MinInterval).Sub(now)
	default:
		d = c.Park // idle: released, or nothing to reclaim
	}
	return min(max(d, c.Tick), c.Park)
}

var metricNames = []string{
	"/memory/classes/heap/free:bytes",
	"/memory/classes/heap/objects:bytes",
	"/memory/classes/heap/unused:bytes",
}

func runtimeSample() Sample {
	samples := make([]metrics.Sample, len(metricNames))
	for i, n := range metricNames {
		samples[i].Name = n
	}
	metrics.Read(samples)
	val := func(i int) uint64 {
		if samples[i].Value.Kind() != metrics.KindUint64 {
			return 0
		}
		return samples[i].Value.Uint64()
	}
	return Sample{FreeUnreleased: val(0), InUse: val(1) + val(2)}
}
