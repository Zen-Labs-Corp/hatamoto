package render

import (
	"fmt"
	"testing"
	"time"
)

var breakT0 = time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

// clockedBreaker is a breaker on a fake clock the test moves by hand.
func clockedBreaker() (*rangeBreaker, *time.Time) {
	now := breakT0
	return newRangeBreaker(func() time.Time { return now }), &now
}

func trip(t *testing.T, b *rangeBreaker, plugin, tmpl string) {
	t.Helper()
	for i := 0; i < rangeBreakLimit; i++ {
		b.breach(plugin, tmpl, false)
	}
	if b.admit(plugin, tmpl) != verdictSkip {
		t.Fatalf("%s/%s not paused after %d breaches", plugin, tmpl, rangeBreakLimit)
	}
}

func TestRangeBreakerTripsAfterLimitInWindow(t *testing.T) {
	t.Parallel()
	b, _ := clockedBreaker()
	for i := 1; i < rangeBreakLimit; i++ {
		if tripped, _ := b.breach("acme", "section-a", false); tripped {
			t.Fatalf("breach %d tripped", i)
		}
		if v := b.admit("acme", "section-a"); v != verdictRun {
			t.Fatalf("admit after breach %d = %d, want run", i, v)
		}
	}
	tripped, until := b.breach("acme", "section-a", false)
	if !tripped || !until.Equal(breakT0.Add(rangeBreakPause)) {
		t.Fatalf("breach %d: tripped=%v until=%v", rangeBreakLimit, tripped, until)
	}
	if v := b.admit("acme", "section-a"); v != verdictSkip {
		t.Fatalf("admit = %d, want skip", v)
	}
	if v := b.admit("acme", "section-b"); v != verdictRun {
		t.Fatalf("other template admit = %d, want run", v)
	}
}

func TestRangeBreakerWindowForgetsOldBreaches(t *testing.T) {
	t.Parallel()
	b, now := clockedBreaker()
	for i := 0; i < 3*rangeBreakLimit; i++ {
		if tripped, _ := b.breach("acme", "section-a", false); tripped {
			t.Fatalf("breach %d tripped", i)
		}
		*now = now.Add(rangeBreakWindow + time.Minute)
	}
	if v := b.admit("acme", "section-a"); v != verdictRun {
		t.Fatalf("admit = %d, want run", v)
	}
}

func TestRangeBreakerHalfOpenOneProbe(t *testing.T) {
	t.Parallel()
	b, now := clockedBreaker()
	trip(t, b, "acme", "section-a")
	*now = now.Add(rangeBreakPause - time.Second)
	if v := b.admit("acme", "section-a"); v != verdictSkip {
		t.Fatalf("before the pause ends: %d, want skip", v)
	}
	*now = now.Add(time.Second)
	if v := b.admit("acme", "section-a"); v != verdictProbe {
		t.Fatalf("first admit after the pause = %d, want probe", v)
	}
	if v := b.admit("acme", "section-a"); v != verdictSkip {
		t.Fatalf("second admit = %d, want skip", v)
	}
}

func TestRangeBreakerProbeCleanCloses(t *testing.T) {
	t.Parallel()
	b, now := clockedBreaker()
	trip(t, b, "acme", "section-a")
	*now = now.Add(rangeBreakPause)
	if v := b.admit("acme", "section-a"); v != verdictProbe {
		t.Fatalf("admit = %d, want probe", v)
	}
	b.settle("acme", "section-a")
	if v := b.admit("acme", "section-a"); v != verdictRun {
		t.Fatalf("admit after a clean probe = %d, want run", v)
	}
	if n := b.open.Load(); n != 0 || len(b.keys) != 0 {
		t.Fatalf("open=%d keys=%d, want 0 and 0", n, len(b.keys))
	}
	// History is cleared: it takes the full limit to trip again.
	if tripped, _ := b.breach("acme", "section-a", false); tripped {
		t.Fatal("one breach after closing tripped")
	}
}

func TestRangeBreakerProbeBreachReopens(t *testing.T) {
	t.Parallel()
	b, now := clockedBreaker()
	trip(t, b, "acme", "section-a")
	*now = now.Add(rangeBreakPause)
	if v := b.admit("acme", "section-a"); v != verdictProbe {
		t.Fatalf("admit = %d, want probe", v)
	}
	if tripped, _ := b.breach("acme", "section-a", false); tripped {
		t.Fatal("a breach of an ordinary render re-opened the breaker")
	}
	tripped, until := b.breach("acme", "section-a", true)
	if !tripped || !until.Equal(now.Add(rangeBreakPause)) {
		t.Fatalf("probe breach: tripped=%v until=%v", tripped, until)
	}
	if v := b.admit("acme", "section-a"); v != verdictSkip {
		t.Fatalf("admit after re-open = %d, want skip", v)
	}
	if n := b.open.Load(); n != 1 {
		t.Fatalf("open = %d, want 1", n)
	}
}

func TestRangeBreakerProbeLeaseExpires(t *testing.T) {
	t.Parallel()
	b, now := clockedBreaker()
	trip(t, b, "acme", "section-a")
	*now = now.Add(rangeBreakPause)
	if v := b.admit("acme", "section-a"); v != verdictProbe {
		t.Fatalf("admit = %d, want probe", v)
	}
	*now = now.Add(rangeBreakProbeLease - time.Second)
	if v := b.admit("acme", "section-a"); v != verdictSkip {
		t.Fatalf("inside the lease: %d, want skip", v)
	}
	*now = now.Add(time.Second)
	if v := b.admit("acme", "section-a"); v != verdictProbe {
		t.Fatalf("after the lease: %d, want a new probe", v)
	}
}

func TestRangeBreakerForgetPlugin(t *testing.T) {
	t.Parallel()
	b, _ := clockedBreaker()
	trip(t, b, "acme", "section-a")
	trip(t, b, "acme", "section-b")
	trip(t, b, "other", "section-a")
	b.breach("acme", "section-c", false)
	b.forget("acme")
	if n := b.open.Load(); n != 1 {
		t.Fatalf("open = %d, want 1", n)
	}
	if v := b.admit("acme", "section-a"); v != verdictRun {
		t.Fatalf("forgotten plugin admit = %d, want run", v)
	}
	if v := b.admit("other", "section-a"); v != verdictSkip {
		t.Fatalf("other plugin admit = %d, want skip", v)
	}
	if len(b.keys) != 1 {
		t.Fatalf("%d keys left, want 1", len(b.keys))
	}
}

func TestRangeBreakerKeyCap(t *testing.T) {
	t.Parallel()
	b, now := clockedBreaker()
	for i := 0; i < 3*rangeBreakKeys; i++ {
		b.breach("acme", fmt.Sprintf("section-%d", i), false)
		if len(b.keys) > rangeBreakKeys {
			t.Fatalf("%d keys, cap %d", len(b.keys), rangeBreakKeys)
		}
	}
	// Once the old breaches left the window the closed keys make room again.
	*now = now.Add(rangeBreakWindow + time.Minute)
	b.breach("acme", "section-fresh", false)
	if len(b.keys) > rangeBreakKeys || b.keys[breakKey("acme", "section-fresh")] == nil {
		t.Fatalf("%d keys, fresh tracked=%v", len(b.keys), b.keys[breakKey("acme", "section-fresh")] != nil)
	}
}

func TestPluginTemplatesPausedSnapshot(t *testing.T) {
	t.Parallel()
	b, now := clockedBreaker()
	if got := b.paused("acme"); got != nil {
		t.Fatalf("nothing tripped: %v", got)
	}
	trip(t, b, "acme", "section-gallery_grid")
	trip(t, b, "acme", "plugin/acme/card")
	trip(t, b, "acme", "helper")
	trip(t, b, "acme", "section-abc")
	trip(t, b, "other", "section-x")
	got := b.paused("acme")
	want := []PausedTemplate{
		{"partial", "card", breakT0.Add(rangeBreakPause)},
		{"section", "abc", breakT0.Add(rangeBreakPause)},
		{"section", "gallery_grid", breakT0.Add(rangeBreakPause)},
		{"template", "helper", breakT0.Add(rangeBreakPause)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Name != want[i].Name || !got[i].Until.Equal(want[i].Until) || got[i].Until.Location() != time.UTC {
			t.Errorf("entry %d: got %v, want %v", i, got[i], want[i])
		}
	}
	*now = now.Add(rangeBreakPause)
	if got := b.paused("acme"); got != nil {
		t.Fatalf("expired entries listed: %v", got)
	}
}
