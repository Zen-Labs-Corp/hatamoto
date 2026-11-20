package render

import (
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"
)

func TestRangeBudgetSpendsAndStops(t *testing.T) {
	t.Parallel()
	h := standaloneRangeHandle("acme", "x")
	for i := 0; i < pluginRangeBudget; i++ {
		if h.Spend() {
			t.Fatalf("spent at iteration %d, want %d allowed", i, pluginRangeBudget)
		}
	}
	for i := 0; i < 2; i++ {
		if !h.Spend() {
			t.Fatalf("handle kept allowing iterations after the budget (extra call %d)", i+1)
		}
	}
}

func TestRangeFunnelBindsLazilyAndReleases(t *testing.T) { //nolint:paralleltest // mutates the process-wide funnel state
	f := enterRangeFunnel()
	if rangeBudgets.active.Load() != 0 {
		t.Fatal("entering a funnel must not bind a budget")
	}
	a, b := boundRangeBudget(), boundRangeBudget()
	if a != b {
		t.Fatal("two binds in one render returned different budgets")
	}
	f.reset()
	if rangeBudgets.active.Load() != 0 {
		t.Fatal("reset left a budget bound")
	}
	if c := boundRangeBudget(); c == a {
		t.Fatal("bind after reset returned the old budget")
	}
	f.exit()
	if rangeBudgets.active.Load() != 0 || rangeFunnels.Load() != 0 {
		t.Fatalf("exit left state: active=%d funnels=%d", rangeBudgets.active.Load(), rangeFunnels.Load())
	}
}

func TestRangeFunnelNestedReusesBinding(t *testing.T) { //nolint:paralleltest // mutates the process-wide funnel state
	outer := enterRangeFunnel()
	a := boundRangeBudget()
	inner := enterRangeFunnel()
	if inner.owner {
		t.Fatal("a nested funnel must not own the binding")
	}
	if boundRangeBudget() != a {
		t.Fatal("nested render did not reuse the binding")
	}
	inner.exit()
	if boundRangeBudget() != a {
		t.Fatal("nested exit dropped the outer binding")
	}
	outer.exit()
	if rangeBudgets.active.Load() != 0 {
		t.Fatal("outer exit left a budget bound")
	}
}

func TestRangeWarnLimiter(t *testing.T) {
	t.Parallel()
	l := &warnLimiter{last: map[string]time.Time{}}
	now := time.Now()
	if !l.allow("acme|x", now) {
		t.Fatal("first warning refused")
	}
	if l.allow("acme|x", now.Add(time.Second)) {
		t.Fatal("repeat within the window allowed")
	}
	if !l.allow("acme|x", now.Add(rangeWarnWindow)) {
		t.Fatal("warning after the window refused")
	}
	for i := 0; i < 3*rangeWarnKeys; i++ {
		l.allow("k"+strconv.Itoa(i), now)
	}
	if len(l.last) > rangeWarnKeys {
		t.Fatalf("limiter holds %d keys, cap %d", len(l.last), rangeWarnKeys)
	}
}

func TestRangeBudgetNotesDegradedOnce(t *testing.T) { //nolint:paralleltest // reads the process-wide degraded counter
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := &rangeBudget{}
	h1 := &rangeHandle{b: b, log: log, plugin: "acme", tmpl: "once-1"}
	h2 := &rangeHandle{b: b, log: log, plugin: "acme", tmpl: "once-2"}
	before := DegradedCount()
	h1.Spend()
	h2.Spend()
	h1.Spend()
	if d := DegradedCount() - before; d != 1 {
		t.Fatalf("degraded delta = %d, want 1", d)
	}
}
