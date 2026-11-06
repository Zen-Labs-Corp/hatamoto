package runtimestats

import (
	"math"
	"runtime/metrics"
	"testing"
)

func TestReadLive(t *testing.T) {
	s := Read()
	if s.LiveHeap == 0 || s.TotalHeap < s.LiveHeap || s.Goroutines < 1 {
		t.Fatalf("implausible snapshot: %+v", s)
	}
	if s.GCCPUShare < 0 || s.GCCPUShare > 1 {
		t.Fatalf("GCCPUShare out of range: %v", s.GCCPUShare)
	}
}

func TestReadFakeLimitUnset(t *testing.T) {
	s := build(map[string]float64{mLimit: float64(math.MaxInt64)})
	if s.LimitSet || s.MemLimit != 0 {
		t.Fatalf("limit should be unset: %+v", s)
	}
}

func TestReadFakeLimitSet(t *testing.T) {
	s := build(map[string]float64{mLimit: 512 << 20})
	if !s.LimitSet || s.MemLimit != 512<<20 {
		t.Fatalf("limit should be 512 MiB: %+v", s)
	}
}

func TestReadKindBadTolerated(t *testing.T) {
	got := fromMetrics([]string{"/no/such/metric:bytes", mGoroutines}, metrics.Read)
	if _, ok := got["/no/such/metric:bytes"]; ok {
		t.Fatal("unknown metric must be omitted")
	}
	if got[mGoroutines] < 1 {
		t.Fatalf("known metric lost: %v", got)
	}
	if s := build(map[string]float64{}); s != (Snapshot{}) {
		t.Fatalf("empty input should give zero snapshot: %+v", s)
	}
}

func TestShareZeroDenominator(t *testing.T) {
	if s := build(map[string]float64{mGCCPU: 1, mTotalCPU: 0}); s.GCCPUShare != 0 {
		t.Fatalf("share = %v, want 0", s.GCCPUShare)
	}
	if s := build(map[string]float64{mGCCPU: 1, mTotalCPU: 4}); s.GCCPUShare != 0.25 {
		t.Fatalf("share = %v, want 0.25", s.GCCPUShare)
	}
}

func TestHeapTotalSumsClasses(t *testing.T) {
	s := build(map[string]float64{mObjects: 10, mFree: 20, mReleased: 30, mUnused: 40})
	if s.LiveHeap != 10 || s.TotalHeap != 100 {
		t.Fatalf("heap = %d/%d", s.LiveHeap, s.TotalHeap)
	}
}
