package render

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// extFake is an ExtSourceFunc that records its batches and answers from a map
// keyed by source id (value sources) or "<source>" for items.
type extFake struct {
	mu      sync.Mutex
	batches []ExtBatch
	values  map[string]string
	items   map[string][]RelatedItem
	fail    bool
}

func (f *extFake) fn(_ context.Context, b ExtBatch) map[string]ExtResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, b)
	if f.fail {
		return nil
	}
	out := map[string]ExtResult{}
	for _, r := range b.Requests {
		if it, ok := f.items[r.Source]; ok {
			out[r.ID] = ExtResult{Items: it, OK: true}
		} else if v, ok := f.values[r.Source]; ok {
			out[r.ID] = ExtResult{Value: v, OK: true}
		}
	}
	return out
}

func (f *extFake) calls() int { return len(f.batches) }

func extSetup(t *testing.T) {
	t.Helper()
	builder.SetExtSources([]builder.ExtSource{
		{Plugin: "weather", Name: "temp", Label: "Temp", Kind: builder.ExtValue, Class: builder.BindText},
		{Plugin: "weather", Name: "link", Label: "Link", Kind: builder.ExtValue, Class: builder.BindURL},
		{Plugin: "weather", Name: "forecast", Label: "Forecast", Kind: builder.ExtItems},
	})
	t.Cleanup(func() { builder.SetExtSources(nil) })
}

func extRender(t *testing.T, f *extFake, refs RefSource, d *builder.Doc, canvas bool) (string, int64) {
	t.Helper()
	opts := Options{Refs: refs}
	if f != nil {
		opts.ExtSources = f.fn
	}
	e, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, scope, done := BindDegradeScope(context.Background())
	defer done()
	v := minimalView()
	v.Ctx, v.Doc, v.Canvas = ctx, d, canvas
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String(), scope.Count()
}

func extItems(n int) []RelatedItem {
	out := make([]RelatedItem, n)
	for i := range out {
		out[i] = RelatedItem{Title: fmt.Sprintf("Day %d", i+1), URL: fmt.Sprintf("/f/%d", i+1), Attrs: map[string]any{"temp": fmt.Sprintf("%d C", 10+i)}}
	}
	return out
}

func sourceLoop(limit int) *builder.Node {
	kids := []*builder.Node{
		{ID: "lh", T: "heading", B: map[string]string{"text": "loop.item.title"}},
		{ID: "lt", T: "text", B: map[string]string{"text": "loop.item.attributes.temp"}},
		{ID: "lb", T: "button", P: map[string]any{"text": "Open"}, B: map[string]string{"href": "loop.item.url"}},
		{ID: "le", T: "loopempty", C: []*builder.Node{{ID: "let", T: "text", P: map[string]any{"text": "No forecast"}}}},
	}
	return loopNode(map[string]any{"from": "source", "source": "ext.weather.forecast", "limit": limit}, kids...)
}

func TestExtOneCallForBindingsAndLoop(t *testing.T) {
	extSetup(t)
	f := &extFake{values: map[string]string{"ext.weather.temp": "<b>21</b>", "ext.weather.link": "javascript:alert(1)"}, items: map[string][]RelatedItem{"ext.weather.forecast": extItems(12)}}
	var nodes []*builder.Node
	for i := 0; i < 5; i++ {
		nodes = append(nodes, &builder.Node{ID: fmt.Sprintf("h%d", i), T: "heading", B: map[string]string{"text": "ext.weather.temp"}})
	}
	nodes = append(nodes, &builder.Node{ID: "bl", T: "button", P: map[string]any{"text": "Docs"}, B: map[string]string{"href": "ext.weather.link"}}, sourceLoop(12))
	out, degraded := extRender(t, f, nil, docOf(nodes...), false)
	if f.calls() != 1 {
		t.Fatalf("calls = %d, want 1", f.calls())
	}
	if n := len(f.batches[0].Requests); n != 3 {
		t.Fatalf("requests = %d, want 3 distinct (temp, link, forecast): %+v", n, f.batches[0].Requests)
	}
	if degraded != 0 {
		t.Fatalf("degraded = %d", degraded)
	}
	if strings.Count(out, "&lt;b&gt;21&lt;/b&gt;") != 5 || strings.Contains(out, "<b>21") {
		t.Errorf("value not escaped five times:\n%s", out)
	}
	if strings.Contains(out, "javascript:") {
		t.Errorf("javascript: url rendered")
	}
	if strings.Count(out, "Day ") != 12 || !strings.Contains(out, `href="/f/12"`) || !strings.Contains(out, "21 C") && !strings.Contains(out, "20 C") {
		t.Errorf("loop items missing:\n%s", out)
	}
}

func TestExtNoExtNoCall(t *testing.T) {
	extSetup(t)
	f := &extFake{}
	extRender(t, f, nil, docOf(&builder.Node{ID: "h", T: "heading", B: map[string]string{"text": "entity.name"}}, sourceLoopQuery()), false)
	if f.calls() != 0 {
		t.Fatalf("calls = %d, want 0 for a page without ext sources", f.calls())
	}
}

func sourceLoopQuery() *builder.Node { return loopNode(map[string]any{"from": "archive"}) }

func TestExtUnregisteredIsEmptyAndNotDegraded(t *testing.T) {
	extSetup(t)
	f := &extFake{values: map[string]string{"ext.gone.thing": "x"}}
	out, degraded := extRender(t, f, nil, docOf(
		&builder.Node{ID: "h", T: "heading", P: map[string]any{"text": "keep"}, B: map[string]string{"text": "ext.gone.thing"}},
		loopNode(map[string]any{"from": "source", "source": "ext.gone.list"}, &builder.Node{ID: "le", T: "loopempty", C: []*builder.Node{{ID: "let", T: "text", P: map[string]any{"text": "EMPTY-STATE"}}}}),
	), false)
	if f.calls() != 0 || degraded != 0 {
		t.Fatalf("calls=%d degraded=%d", f.calls(), degraded)
	}
	if strings.Contains(out, ">keep<") || !strings.Contains(out, "EMPTY-STATE") {
		t.Errorf("unregistered source must empty the bound prop and show the loop's empty state:\n%s", out)
	}
}

func TestExtFailureDegrades(t *testing.T) {
	extSetup(t)
	f := &extFake{fail: true}
	out, degraded := extRender(t, f, nil, docOf(
		&builder.Node{ID: "h", T: "heading", P: map[string]any{"text": "fallback"}, B: map[string]string{"text": "ext.weather.temp"}},
		sourceLoop(5),
	), false)
	if degraded == 0 {
		t.Fatal("render not marked degraded: a failed page would be cached")
	}
	if strings.Contains(out, "fallback") || strings.Contains(out, "Day ") || !strings.Contains(out, "No forecast") {
		t.Errorf("failure must give empty props and the loop empty state:\n%s", out)
	}
}

func TestExtCanvasPlaceholder(t *testing.T) {
	extSetup(t)
	f := &extFake{fail: true}
	out, _ := extRender(t, f, nil, docOf(&builder.Node{ID: "h", T: "heading", B: map[string]string{"text": "ext.weather.temp"}}), true)
	if !strings.Contains(out, "‹ext.weather.temp›") {
		t.Errorf("canvas placeholder missing:\n%s", out)
	}
}

func TestExtRefsShareThePageBatch(t *testing.T) {
	extSetup(t)
	// the ref's content is expanded before the batch: one call covers page + block
	f := &extFake{values: map[string]string{"ext.weather.temp": "T", "ext.weather.link": "https://x.test/"}}
	refs := fakeRefs{blkA: docOf(&builder.Node{ID: "bh", T: "heading", B: map[string]string{"text": "ext.weather.temp"}})}
	out, degraded := extRender(t, f, refs, docOf(refNode("r1", blkA), &builder.Node{ID: "b", T: "button", P: map[string]any{"text": "d"}, B: map[string]string{"href": "ext.weather.link"}}), false)
	if f.calls() != 1 || degraded != 0 || !strings.Contains(out, ">T<") {
		t.Fatalf("calls=%d degraded=%d\n%s", f.calls(), degraded, out)
	}
}

func TestExtPartsSecondCallAtMostTwo(t *testing.T) {
	extSetup(t)
	f := &extFake{values: map[string]string{"ext.weather.temp": "PAGE-T", "ext.weather.link": "https://x.test/"}}
	parts := standardParts()
	parts.parts[partHeader] = partDef{builder.SlotHeader, docOf(&builder.Node{ID: "hd", T: "heading", B: map[string]string{"text": "ext.weather.link"}})}
	parts.parts[partFooter] = partDef{builder.SlotFooter, docOf(&builder.Node{ID: "ft", T: "heading", B: map[string]string{"text": "ext.weather.link"}})}
	e := partsEngine(t, parts, themeOptIn(`["header","footer"]`))
	e.opts.ExtSources = f.fn
	v := assigned(minimalView(), map[string]string{builder.SlotHeader: partHeader, builder.SlotFooter: partFooter})
	v.Doc = docOf(&builder.Node{ID: "h", T: "heading", B: map[string]string{"text": "ext.weather.temp"}})
	out := page(t, e, v)
	if f.calls() != 2 {
		t.Fatalf("calls = %d, want 2 (page, then both parts together)", f.calls())
	}
	if !strings.Contains(out, "PAGE-T") || strings.Count(out, "https://x.test/") < 2 {
		t.Errorf("values missing:\n%s", out)
	}
}

func TestExtDuplicateBindingsShareOneRequest(t *testing.T) {
	extSetup(t)
	f := &extFake{values: map[string]string{"ext.weather.temp": "T"}}
	var nodes []*builder.Node
	for i := 0; i < maxExtRequests+4; i++ {
		nodes = append(nodes, &builder.Node{ID: fmt.Sprintf("h%d", i), T: "heading", B: map[string]string{"text": "ext.weather.temp"}})
	}
	_, degraded := extRender(t, f, nil, docOf(nodes...), false)
	if degraded != 0 || len(f.batches[0].Requests) != 1 {
		t.Fatalf("duplicate bindings must share one request: degraded=%d reqs=%+v", degraded, f.batches[0].Requests)
	}
}

func TestExtRequestCapDegrades(t *testing.T) {
	extSetup(t)
	f := &extFake{items: map[string][]RelatedItem{"ext.weather.forecast": extItems(3)}}
	var nodes []*builder.Node
	for i := 1; i <= maxExtRequests+4; i++ { // a distinct limit makes a distinct request
		n := sourceLoop(i)
		n.ID = fmt.Sprintf("lp%d", i)
		nodes = append(nodes, n)
	}
	_, degraded := extRender(t, f, nil, docOf(nodes...), false)
	if len(f.batches[0].Requests) != maxExtRequests || degraded == 0 {
		t.Fatalf("requests=%d degraded=%d, want %d and >0", len(f.batches[0].Requests), degraded, maxExtRequests)
	}
}

// The canvas renders top nodes one by one on one view: PrefetchExt makes the
// whole doc cost one call, so a third section with another source is not left
// empty after the two-call cap.
func TestExtPrefetchOnceForCanvasTopNodes(t *testing.T) {
	extSetup(t)
	f := &extFake{values: map[string]string{"ext.weather.temp": "21 C", "ext.weather.link": "/w"}, items: map[string][]RelatedItem{"ext.weather.forecast": extItems(3)}}
	e, err := New(Options{ExtSources: f.fn})
	if err != nil {
		t.Fatal(err)
	}
	ctx, scope, done := BindDegradeScope(context.Background())
	defer done()
	doc := docOf(
		&builder.Node{ID: "h", T: "heading", P: map[string]any{"text": "x"}, B: map[string]string{"text": "ext.weather.temp"}},
		&builder.Node{ID: "bl", T: "button", P: map[string]any{"text": "Docs"}, B: map[string]string{"href": "ext.weather.link"}},
		sourceLoop(3),
	)
	v := minimalView()
	v.Ctx, v.Doc, v.Canvas = ctx, doc, true
	e.PrefetchExt(v, doc.Nodes)
	var all strings.Builder
	for _, n := range doc.Nodes {
		fr, ok, err := e.RenderTop(v, n)
		if err != nil || !ok {
			t.Fatalf("RenderTop %s: ok=%v err=%v", n.ID, ok, err)
		}
		all.WriteString(fr.HTML)
	}
	if f.calls() != 1 || scope.Count() != 0 {
		t.Fatalf("calls = %d, degraded = %d; want 1 and 0", f.calls(), scope.Count())
	}
	for _, want := range []string{"21 C", `href="/w"`, "Day 1", "Day 3"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("canvas output lacks %q:\n%s", want, all.String())
		}
	}
}
