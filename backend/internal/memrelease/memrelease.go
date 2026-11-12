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
// the previous release.
func Run(ctx context.Context, c Config) {
	c.fill()
	r := &releaser{c: c, baseline: c.Sample().InUse}
	t := time.NewTicker(c.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.step(c.Now())
		}
	}
}

// step takes one sample and releases when the conditions hold.
func (r *releaser) step(now time.Time) {
	s := r.c.Sample()
	if s.FreeUnreleased < r.c.MinReclaim && s.InUse < r.baseline+r.c.MinReclaim {
		return
	}
	if r.c.LastActivity != nil && now.Sub(r.c.LastActivity()) < r.c.Idle {
		return
	}
	if r.freed && now.Sub(r.lastFree) < r.c.MinInterval {
		return
	}
	r.c.Free()
	r.baseline = r.c.Sample().InUse
	r.lastFree = now
	r.freed = true
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
