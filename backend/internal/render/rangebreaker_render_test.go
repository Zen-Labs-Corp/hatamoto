package render

import (
	"strings"
	"testing"
	"time"
)

// swapRangeBreaker installs a breaker on a fake clock for the test and
// restores the process-wide one (and the warn limiter) afterwards.
func swapRangeBreaker(t *testing.T) (*rangeBreaker, *time.Time) {
	t.Helper()
	oldB, oldW := rangeBreaks, rangeWarns
	b, now := clockedBreaker()
	rangeBreaks, rangeWarns = b, &warnLimiter{last: map[string]time.Time{}}
	t.Cleanup(func() { rangeBreaks, rangeWarns = oldB, oldW })
	return b, now
}

// countView renders section typ with .Content.n as the loop size.
func countView(n int, types ...string) *PageView {
	v := minimalView()
	for _, typ := range types {
		v.Sections = append(v.Sections, Section{ID: "id-" + typ, Type: typ, Content: map[string]any{"n": n}})
	}
	return v
}

const (
	hugeN  = 1000000000
	smallN = 2
)

// breakerEngine registers acme-brk sections "loop" (a counted range), "other"
// (a second range section of the same plugin) and "plain" (no range), plus a
// section of a second plugin.
func breakerEngine(t *testing.T) (*Engine, func(string) int) {
	t.Helper()
	reg := newPluginRegistry()
	mustRegister(t, reg, "acme-brk", nil, map[string]string{
		"loop":  rangeSection("loop", `<i>kept</i>{{range $i := .Content.n}}`+mark+`{{end}}`),
		"other": rangeSection("other", `{{range $i := .Content.n}}o{{end}}`),
		"plain": rangeSection("plain", `plain`),
	})
	mustRegister(t, reg, "acme-two", nil, map[string]string{
		"twin": rangeSection("twin", `{{range $i := .Content.n}}t{{end}}`),
	})
	log, count := newWarnRecorder()
	e, err := newEngine(Options{Logger: log}, reg)
	if err != nil {
		t.Fatal(err)
	}
	return e, count
}

func tripLoop(t *testing.T, e *Engine) {
	t.Helper()
	for i := 0; i < rangeBreakLimit; i++ {
		renderToString(t, e, countView(hugeN, "loop"))
	}
}

func TestPluginRangeBreakerSkipsAfterThreeBreaches(t *testing.T) { //nolint:paralleltest // process-wide degraded counter and breaker
	swapRangeBreaker(t)
	e, _ := breakerEngine(t)
	for i := 0; i < rangeBreakLimit; i++ {
		out := renderToString(t, e, countView(hugeN, "loop"))
		if !strings.Contains(out, `<p id="loop">`) || !strings.Contains(out, mark) {
			t.Fatalf("render %d did not run the section", i+1)
		}
	}
	before := DegradedCount()
	out := renderToString(t, e, countView(hugeN, "loop"))
	if strings.Contains(out, mark) || strings.Contains(out, `<p id="loop">`) || strings.Contains(out, "kept") {
		t.Errorf("paused section rendered:\n%s", mainInner(t, out))
	}
	if strings.Contains(out, SectionFailedMarker) {
		t.Error("a paused section must not become a failed section")
	}
	if DegradedCount() == before {
		t.Error("the skipped render was not noted as degraded")
	}
}

func TestPluginRangeBreakerCountsTrialAndPageOnce(t *testing.T) { //nolint:paralleltest // process-wide degraded counter and breaker
	b, _ := swapRangeBreaker(t)
	e, _ := breakerEngine(t)
	for i := 0; i < rangeBreakLimit-1; i++ {
		renderToString(t, e, countView(hugeN, "loop"))
	}
	if n := b.open.Load(); n != 0 {
		t.Fatalf("%d renders (trial and page each) tripped the breaker", rangeBreakLimit-1)
	}
	if out := renderToString(t, e, countView(hugeN, "loop")); !strings.Contains(out, mark) {
		t.Fatal("section did not run before the limit")
	}
	if n := b.open.Load(); n != 1 {
		t.Fatalf("open = %d after %d renders, want 1", n, rangeBreakLimit)
	}
}

func TestPluginRangeBreakerRetriesAfterPause(t *testing.T) { //nolint:paralleltest // process-wide degraded counter and breaker
	b, now := swapRangeBreaker(t)
	e, _ := breakerEngine(t)
	tripLoop(t, e)
	if out := renderToString(t, e, countView(smallN, "loop")); strings.Contains(out, "kept") {
		t.Fatal("paused section ran")
	}

	// A clean probe closes the breaker.
	*now = now.Add(rangeBreakPause)
	if out := renderToString(t, e, countView(smallN, "loop")); !strings.Contains(out, "kept") {
		t.Fatal("probe render did not run")
	}
	if b.open.Load() != 0 || len(b.keys) != 0 {
		t.Fatalf("clean probe left state: open=%d keys=%d", b.open.Load(), len(b.keys))
	}
	if out := renderToString(t, e, countView(hugeN, "loop")); !strings.Contains(out, mark) {
		t.Fatal("section was still skipped after a clean probe")
	}

	// A breaching probe re-opens at once.
	tripLoop(t, e)
	*now = now.Add(rangeBreakPause)
	if out := renderToString(t, e, countView(hugeN, "loop")); !strings.Contains(out, mark) {
		t.Fatal("probe render did not run")
	}
	if out := renderToString(t, e, countView(smallN, "loop")); strings.Contains(out, "kept") {
		t.Fatal("section ran after a breaching probe")
	}
	if got := PluginTemplatesPaused("acme-brk"); len(got) != 1 || got[0].Kind != "section" || got[0].Name != "loop" {
		t.Fatalf("paused = %v", got)
	}
}

func TestPluginRangeBreakerIsolatesTemplates(t *testing.T) { //nolint:paralleltest // process-wide degraded counter and breaker
	swapRangeBreaker(t)
	e, _ := breakerEngine(t)
	tripLoop(t, e)
	out := renderToString(t, e, countView(3, "loop", "other", "plain", "twin"))
	for _, want := range []string{`<p id="other">ooo</p>`, `<p id="plain">plain</p>`, `<p id="twin">ttt</p>`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in\n%s", want, mainInner(t, out))
		}
	}
	if strings.Contains(out, `<p id="loop">`) {
		t.Error("the paused section rendered")
	}
}

func TestPluginRangeBreakerOneWarnPerTrip(t *testing.T) { //nolint:paralleltest // process-wide degraded counter and breaker
	_, now := swapRangeBreaker(t)
	e, count := breakerEngine(t)
	tripLoop(t, e)
	for i := 0; i < 3; i++ {
		renderToString(t, e, countView(hugeN, "loop"))
	}
	if n := count("paused after repeated range budget breaches plugin=acme-brk template=section-loop"); n != 1 {
		t.Fatalf("%d trip warnings, want 1", n)
	}
	// The first two breaches warn as before (once a minute); the tripping one only logs the pause.
	if n := count("plugin template range budget exceeded"); n != 1 {
		t.Fatalf("%d per-minute warnings, want 1 from the breaches before the trip", n)
	}
	*now = now.Add(rangeBreakPause)
	renderToString(t, e, countView(hugeN, "loop")) // breaching probe: a trip again
	renderToString(t, e, countView(hugeN, "loop"))
	if n := count("paused after repeated"); n != 2 {
		t.Fatalf("%d trip warnings after the re-open, want 2", n)
	}
}

func TestSampleRenderIgnoresRangeBreaker(t *testing.T) { //nolint:paralleltest // process-wide breaker
	b, _ := swapRangeBreaker(t)
	trip(t, b, "", "sample")
	trip(t, b, "acme", "sample")
	src := `{{range $i := 3}}{{$i}}{{end}}`
	for i := 0; i < 2*rangeBreakLimit; i++ {
		if _, err := sampleRender(src, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.keys) != 2 || b.open.Load() != 2 {
		t.Fatalf("sample render changed the breaker: keys=%d open=%d", len(b.keys), b.open.Load())
	}
	for k, st := range b.keys {
		if len(st.breaches) != 0 || !st.probeAt.IsZero() {
			t.Errorf("sample render touched %s: %+v", k, st)
		}
	}
}

func TestRemovePluginClearsRangeBreaker(t *testing.T) { //nolint:paralleltest // process-wide breaker
	b, _ := swapRangeBreaker(t)
	trip(t, b, "acme-gone", "section-loop")
	if got := PluginTemplatesPaused("acme-gone"); len(got) != 1 {
		t.Fatalf("paused = %v, want one", got)
	}
	RemovePlugin("acme-gone")
	if got := PluginTemplatesPaused("acme-gone"); got != nil {
		t.Fatalf("paused after RemovePlugin = %v", got)
	}
}

// The wrap in {{if $hm-budget.Run}} must not change what the escaper makes of
// attribute and URL contexts inside a range.
func TestPluginRangeRewriteWrapKeepsEscaping(t *testing.T) { //nolint:paralleltest // process-wide breaker
	swapRangeBreaker(t)
	reg := newPluginRegistry()
	mustRegister(t, reg, "acme-esc", nil, map[string]string{
		"links": rangeSection("links", `{{range $i, $x := .Content.items}}<a href="/q?x={{$x}}" title="{{$x}}">{{$x}}</a>{{end}}`),
	})
	log, count := newWarnRecorder()
	e, err := newEngine(Options{Logger: log}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if n := count("rejected") + count("skipping"); n != 0 {
		t.Fatalf("section was rejected: %d warnings", n)
	}
	v := minimalView()
	v.Sections = []Section{{ID: "id-links", Type: "links", Content: map[string]any{"items": []any{"a b", `"<c>`}}}}
	out := renderToString(t, e, v)
	want := `<p id="links"><a href="/q?x=a%20b" title="a b">a b</a><a href="/q?x=%22%3cc%3e" title="&#34;&lt;c&gt;">&#34;&lt;c&gt;</a></p>`
	if !strings.Contains(out, want) {
		t.Errorf("want %s in\n%s", want, mainInner(t, out))
	}
}
