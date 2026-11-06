// Package runtimestats reads Go runtime memory and scheduler numbers through
// runtime/metrics. It never calls runtime.ReadMemStats, which stops the world.
package runtimestats

import (
	"math"
	"runtime/metrics"
)

// Snapshot is one reading of the Go runtime.
type Snapshot struct {
	// LiveHeap is the memory occupied by heap objects.
	LiveHeap uint64
	// TotalHeap is the heap the runtime holds from the OS: objects, free
	// and unused spans. Pages already returned to the OS, goroutine stacks and
	// runtime metadata are not included.
	TotalHeap uint64
	// MemLimit is the soft memory limit; meaningful only when LimitSet.
	MemLimit int64
	// LimitSet is false when no limit is configured (math.MaxInt64).
	LimitSet bool
	// GCCPUShare is GC CPU time over total CPU time, 0..1.
	GCCPUShare float64
	Goroutines int
}

const (
	mObjects    = "/memory/classes/heap/objects:bytes"
	mFree       = "/memory/classes/heap/free:bytes"
	mUnused     = "/memory/classes/heap/unused:bytes"
	mLimit      = "/gc/gomemlimit:bytes"
	mGCCPU      = "/cpu/classes/gc/total:cpu-seconds"
	mTotalCPU   = "/cpu/classes/total:cpu-seconds"
	mGoroutines = "/sched/goroutines:goroutines"
)

var names = []string{mObjects, mFree, mUnused, mLimit, mGCCPU, mTotalCPU, mGoroutines}

// Read returns the current runtime snapshot.
func Read() Snapshot { return build(fromMetrics(names, metrics.Read)) }

// fromMetrics reads the named metrics into a name->value map. Metrics the
// runtime does not know (KindBad) or of another kind are left out.
func fromMetrics(names []string, rd func([]metrics.Sample)) map[string]float64 {
	samples := make([]metrics.Sample, len(names))
	for i, n := range names {
		samples[i].Name = n
	}
	rd(samples)
	out := make(map[string]float64, len(samples))
	for _, s := range samples {
		switch s.Value.Kind() {
		case metrics.KindUint64:
			out[s.Name] = float64(s.Value.Uint64())
		case metrics.KindFloat64:
			out[s.Name] = s.Value.Float64()
		}
	}
	return out
}

// build turns metric values into a Snapshot; a missing metric reads as 0.
func build(v map[string]float64) Snapshot {
	s := Snapshot{
		LiveHeap:   uint64(v[mObjects]),
		TotalHeap:  uint64(v[mObjects] + v[mFree] + v[mUnused]),
		Goroutines: int(v[mGoroutines]),
	}
	// MaxInt64 is the runtime's "no limit"; as a float64 it rounds to 2^63.
	if lim := v[mLimit]; lim > 0 && lim < math.MaxInt64 {
		s.MemLimit, s.LimitSet = int64(lim), true
	}
	if total := v[mTotalCPU]; total > 0 {
		s.GCCPUShare = math.Min(math.Max(v[mGCCPU]/total, 0), 1)
	}
	return s
}
