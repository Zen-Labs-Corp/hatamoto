package render

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

const (
	blkA = "aaaaaaaa-0000-4000-8000-000000000001"
	blkB = "bbbbbbbb-0000-4000-8000-000000000002"
	blkC = "cccccccc-0000-4000-8000-000000000003"
	blkD = "dddddddd-0000-4000-8000-000000000004"
)

// fakeRefs is a RefSource over a map; only blocks, no parts.
type fakeRefs map[string]*builder.Doc

func (f fakeRefs) Block(_ context.Context, id string) (*builder.Doc, bool) {
	d, ok := f[id]
	return d, ok
}
func (fakeRefs) Part(context.Context, string, string) (*builder.Doc, bool) { return nil, false }

func refNode(id, block string) *builder.Node {
	return &builder.Node{ID: id, T: "ref", P: map[string]any{"block": block}}
}

func textNode(id, text string) *builder.Node {
	return &builder.Node{ID: id, T: "text", P: map[string]any{"text": text}, S: map[string]map[string]string{"base": {"ta": "center"}}}
}

func docOf(ns ...*builder.Node) *builder.Doc { return &builder.Doc{V: 2, Nodes: ns} }

func refEngine(t *testing.T, src RefSource) *Engine {
	t.Helper()
	e, err := New(Options{Refs: src})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func renderWith(t *testing.T, e *Engine, d *builder.Doc, canvas bool) string {
	t.Helper()
	v := minimalView()
	v.Doc, v.Canvas = d, canvas
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func blockWithHeading() fakeRefs {
	return fakeRefs{blkA: docOf(&builder.Node{ID: "box", T: "container", P: map[string]any{"tag": "section"},
		S: map[string]map[string]string{"base": {"ta": "center"}},
		C: []*builder.Node{{ID: "hd", T: "heading", P: map[string]any{"text": "Footer <CTA>", "level": float64(2)}}}})}
}

func TestRefPublicGolden(t *testing.T) {
	e := refEngine(t, blockWithHeading())
	d := docOf(textNode("intro", "before"), refNode("r1", blkA), textNode("outro", "after"))
	got := renderWith(t, e, d, false)
	compareRefGolden(t, "basic.public", mainInner(t, got))
	if m := mainInner(t, got); strings.Contains(m, "data-hm-") || strings.Contains(m, " style=") {
		t.Error("public output carries canvas markers or inline style")
	}
	if !strings.Contains(got, "Footer &lt;CTA&gt;") {
		t.Error("block text missing or unescaped")
	}
}

func TestRefCanvasIsOneUnit(t *testing.T) {
	e := refEngine(t, blockWithHeading())
	got := renderWith(t, e, docOf(refNode("r1", blkA)), true)
	compareRefGolden(t, "basic.canvas", mainInner(t, got))
	if n := strings.Count(got, "data-hm-node="); n != 1 {
		t.Errorf("canvas has %d node markers, want 1 (the ref)", n)
	}
	if !strings.Contains(got, `data-hm-node="r1" data-hm-t="ref"`) {
		t.Error("ref wrapper not marked as a ref")
	}
	if strings.Contains(got, "hm-ref-miss{") {
		t.Error("placeholder css without a failing ref is not needed")
	}
}

func TestRefBlockUsedTwiceEmitsCSSOnce(t *testing.T) {
	e := refEngine(t, blockWithHeading())
	got := renderWith(t, e, docOf(refNode("r1", blkA), refNode("r2", blkA)), false)
	css := styleBody(t, got)
	prefix := refPrefix(blkA)
	if n := strings.Count(css, ".hm-n-"+prefix+"box"); n != 1 {
		t.Errorf("block node css emitted %d times, want 1:\n%s", n, css)
	}
	if n := strings.Count(got, `hm-n-`+prefix+`box`); n != 3 { // 2 elements + 1 rule
		t.Errorf("class used %d times", n)
	}
}

func TestRefIDsNeverCollideWithPage(t *testing.T) {
	src := fakeRefs{blkA: docOf(textNode("x", "in block"))}
	e := refEngine(t, src)
	got := renderWith(t, e, docOf(textNode("x", "on page"), refNode("r1", blkA)), false)
	css := styleBody(t, got)
	if !strings.Contains(css, ".hm-n-x") || !strings.Contains(css, ".hm-n-"+refPrefix(blkA)+"x") {
		t.Errorf("page and block node both need their rules:\n%s", css)
	}
}

func TestRefFailuresRenderComments(t *testing.T) {
	selfDoc := docOf(refNode("s", blkA))
	cases := []struct {
		name string
		src  RefSource
		want string
	}{
		{"self", fakeRefs{blkA: selfDoc}, "hm-ref: cycle"},
		{"A->B->A", fakeRefs{blkA: docOf(refNode("x", blkB)), blkB: docOf(refNode("y", blkA))}, "hm-ref: cycle"},
		{"depth", fakeRefs{blkA: docOf(refNode("x", blkB)), blkB: docOf(refNode("y", blkC)), blkC: docOf(refNode("z", blkD)), blkD: docOf(textNode("t", "deep"))}, "hm-ref: depth"},
		{"missing", fakeRefs{}, "hm-ref: missing"},
		{"nil source", nil, "hm-ref: missing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var src RefSource = c.src
			if c.src == nil {
				src = nil
			}
			e := refEngine(t, src)
			got := renderWith(t, e, docOf(textNode("before", "kept-before"), refNode("r1", blkA), textNode("after", "kept-after")), false)
			if !strings.Contains(got, "<!-- "+c.want+" -->") {
				t.Errorf("no %q comment in:\n%s", c.want, mainInner(t, got))
			}
			if !strings.Contains(got, "kept-before") || !strings.Contains(got, "kept-after") {
				t.Error("the rest of the page must still render")
			}
			canvas := renderWith(t, e, docOf(refNode("r1", blkA)), true)
			if !strings.Contains(canvas, `hm-ref-miss`) || !strings.Contains(canvas, `data-hm-t="ref"`) || !strings.Contains(canvas, "hm-ref-miss{") {
				t.Errorf("canvas placeholder missing:\n%s", mainInner(t, canvas))
			}
		})
	}
}

func TestRefDepthThreeStillRenders(t *testing.T) {
	src := fakeRefs{blkA: docOf(refNode("x", blkB)), blkB: docOf(refNode("y", blkC)), blkC: docOf(textNode("t", "three deep"))}
	got := renderWith(t, refEngine(t, src), docOf(refNode("r1", blkA)), false)
	if !strings.Contains(got, "three deep") || strings.Contains(got, "hm-ref:") {
		t.Errorf("depth 3 must expand:\n%s", mainInner(t, got))
	}
}

func TestRefNodeLimit(t *testing.T) {
	big := &builder.Node{ID: "big", T: "container"}
	for i := 0; i < 600; i++ {
		big.C = append(big.C, &builder.Node{ID: "n" + itoa(i), T: "divider"})
	}
	e := refEngine(t, fakeRefs{blkA: docOf(big)})
	got := renderWith(t, e, docOf(refNode("r1", blkA), refNode("r2", blkA), textNode("after", "still-here")), false)
	if strings.Count(got, "hm-w-divider") < 600 || !strings.Contains(got, "<!-- hm-ref: limit -->") || !strings.Contains(got, "still-here") {
		t.Errorf("limit: dividers=%d limit=%v", strings.Count(got, "hm-w-divider"), strings.Contains(got, "hm-ref: limit"))
	}
}

func itoa(i int) string {
	const d = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{d[i%10]}, b...)
	}
	return string(b)
}

func TestRefHiddenAndPresetAndStyle(t *testing.T) {
	e := refEngine(t, fakeRefs{blkA: docOf(textNode("t", "inside"))})
	hidden := refNode("r1", blkA)
	hidden.P["hidden"] = true
	if got := renderWith(t, e, docOf(hidden, textNode("o", "other")), false); strings.Contains(got, "inside") || strings.Contains(got, "hm-ref") {
		t.Error("hidden ref rendered")
	}
	styled := refNode("r2", blkA)
	styled.S = map[string]map[string]string{"base": {"mt": "space-4"}}
	got := renderWith(t, e, docOf(styled), false)
	if !strings.Contains(styleBody(t, got), ".hm-n-r2{margin-top:") {
		t.Errorf("outer spacing of the ref lost:\n%s", styleBody(t, got))
	}
}

func TestRefBlockWidgetAddsKindCSSAndIsland(t *testing.T) {
	tabs := loadWidgetDoc(t, "tabs")
	e := refEngine(t, fakeRefs{blkA: tabs})
	plain := renderWith(t, e, docOf(textNode("o", "x")), false)
	got := renderWith(t, e, docOf(refNode("r1", blkA)), false)
	if !islandScript.MatchString(got) || islandScript.MatchString(plain) {
		t.Errorf("tabs island: with ref=%v without=%v", islandScript.MatchString(got), islandScript.MatchString(plain))
	}
	if !strings.Contains(styleBody(t, got), widgets().css["tabs"]) {
		t.Error("tabs css missing from the page")
	}
}

func TestRefNoRefIsSamePointer(t *testing.T) {
	e := refEngine(t, blockWithHeading())
	n := nativeDoc().Nodes[0]
	if e.expandRefs(context.Background(), n, &refState{}) != n {
		t.Error("a subtree without ref must be returned as is")
	}
	// and an engine without a RefSource renders a doc without refs as before
	plain, _ := New(Options{})
	if renderWith(t, plain, nativeDoc(), false) != renderWith(t, e, nativeDoc(), false) {
		t.Error("RefSource changes a doc without refs")
	}
}

// The canvas patch of a ref is the slice of the full canvas render, and its CSS
// carries the block's rules.
func TestRefFragmentsMatchFullRender(t *testing.T) {
	e := refEngine(t, blockWithHeading())
	d := docOf(textNode("a", "x"), refNode("r1", blkA))
	full := renderWith(t, e, d, true)
	v := minimalView()
	v.Doc, v.Canvas = d, true
	f, ok, err := e.RenderTop(v, d.Nodes[1])
	if err != nil || !ok {
		t.Fatalf("RenderTop = %v %v", ok, err)
	}
	if !strings.Contains(full, f.HTML) {
		t.Errorf("fragment is not a slice of the page:\n%s", f.HTML)
	}
	if f.CSS[refPrefix(blkA)+"box"] == "" {
		t.Errorf("fragment css lacks block rules: %v", f.CSS)
	}
	css := e.NodeCSSFor(v, d.Nodes[1], nil)
	if css[refPrefix(blkA)+"box"] == "" || len(e.NodeCSS(d.Nodes[1])) != 0 {
		t.Errorf("NodeCSSFor = %v", css)
	}
	var b bytes.Buffer
	if err := e.RenderNode(&b, v, d.Nodes[1], true); err != nil || b.String() != f.HTML {
		t.Errorf("RenderNode differs from RenderTop: %v", err)
	}
	// the page canvas head carries the same per-node rules
	if !strings.Contains(full, `data-hm-css="`+refPrefix(blkA)+`box"`) {
		t.Error("canvas head lacks the block node css")
	}
}

func compareRefGolden(t *testing.T, name, got string) {
	t.Helper()
	path := "testdata/refs/" + name + ".html"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if got != string(want) {
		t.Fatalf("golden %s mismatch (run with -update to accept)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
