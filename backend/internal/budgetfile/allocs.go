package budgetfile

import "testing"

// CheckAllocs runs bench three times (testing.Benchmark) and checks the
// minimum allocs/op and bytes/op as <name>_allocs and <name>_bytes. Allocation
// counts are deterministic where wall clock is not. It skips under -short and
// -race (the race detector changes allocation behaviour).
func CheckAllocs(t *testing.T, file, name string, bench func(*testing.B)) {
	t.Helper()
	if testing.Short() {
		t.Skip("allocation budgets run in the full suite")
	}
	if raceEnabled {
		t.Skip("allocs differ under -race")
	}
	var allocs, bytes int64 = -1, -1
	for range 3 {
		r := testing.Benchmark(bench)
		if r.N == 0 {
			t.Fatalf("benchmark %s did not run", name)
		}
		if a := r.AllocsPerOp(); allocs < 0 || a < allocs {
			allocs = a
		}
		if b := r.AllocedBytesPerOp(); bytes < 0 || b < bytes {
			bytes = b
		}
	}
	Check(t, file, name+"_allocs", allocs, Metric{Unit: "allocs", TolPct: 10, TolAbs: 2})
	Check(t, file, name+"_bytes", bytes, Metric{Unit: "bytes", TolPct: 10, TolAbs: 64})
}
