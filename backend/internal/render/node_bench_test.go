package render

import (
	"bytes"
	"io"
	"strconv"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// fragmentDoc returns one container with 9 leaf children: 10 nodes.
func fragmentDoc() *builder.Node {
	root := &builder.Node{ID: "root", T: "container", S: map[string]map[string]string{"base": {"gap": "space-4"}}}
	for i := 0; i < 9; i++ {
		id := "w" + strconv.Itoa(i)
		switch i % 3 {
		case 0:
			root.C = append(root.C, &builder.Node{ID: id, T: "heading", P: map[string]any{"text": "Heading " + id, "level": float64(2)}})
		case 1:
			root.C = append(root.C, &builder.Node{ID: id, T: "text", P: map[string]any{"text": "Some paragraph text for " + id}})
		default:
			root.C = append(root.C, &builder.Node{ID: id, T: "button", P: map[string]any{"text": "Go", "href": "/go"}})
		}
	}
	return root
}

// pageDoc72 returns a doc of exactly 72 nodes: 6 rows of 1 container, 9 leaves, a spacer and a divider.
func pageDoc72() *builder.Doc {
	d := &builder.Doc{V: 2}
	for r := 0; r < 6; r++ {
		row := fragmentDoc()
		row.ID = "r" + strconv.Itoa(r)
		for i, c := range row.C {
			c.ID = row.ID + "-" + strconv.Itoa(i)
		}
		row.C = append(row.C,
			&builder.Node{ID: row.ID + "-s", T: "spacer"},
			&builder.Node{ID: row.ID + "-d", T: "divider"},
		)
		d.Nodes = append(d.Nodes, row)
	}
	return d
}

func BenchmarkRenderNode10(b *testing.B) {
	e, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	v, n := minimalView(), fragmentDoc()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.RenderNode(io.Discard, v, n, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderTop10(b *testing.B) {
	e, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	v, n := minimalView(), fragmentDoc()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := e.RenderTop(v, n); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderPage72(b *testing.B) {
	e, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	v := minimalView()
	v.Doc = pageDoc72()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.RenderPage(io.Discard, v); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRenderNodeFragment(t *testing.T) {
	e, _ := New(Options{})
	var buf bytes.Buffer
	if err := e.RenderNode(&buf, minimalView(), fragmentDoc(), true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if out == "" || out[0] != '<' || bytes.Contains(buf.Bytes(), []byte("<html")) || bytes.Contains(buf.Bytes(), []byte("<style")) {
		t.Fatalf("not a bare fragment: %.200s", out)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`data-hm-node="root"`)) {
		t.Error("canvas fragment lacks node marker")
	}
	buf.Reset()
	_ = e.RenderNode(&buf, minimalView(), fragmentDoc(), false)
	if bytes.Contains(buf.Bytes(), []byte("data-hm-")) {
		t.Error("public fragment has markers")
	}
	if css := e.NodeCSS(fragmentDoc()); len(css) != 1 || css["root"] == "" {
		t.Errorf("NodeCSS = %v", css)
	}
}

// Wall-clock backstop, outside make budgets (the deterministic gate is the
// allocation budget in budget_test.go). Ceilings are >= 10x the dev-machine
// measurement (node 4.6 us, top 42 us, page 240 us per op).
func TestRenderBudgets(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing")
	}
	if n := pageDoc72().Count(); n != 72 {
		t.Fatalf("page fixture has %d nodes, want 72", n)
	}
	node := testing.Benchmark(BenchmarkRenderNode10)
	if ns := node.NsPerOp(); ns > 100_000 {
		t.Errorf("10-node fragment %d ns/op, budget 100000", ns)
	}
	top := testing.Benchmark(BenchmarkRenderTop10)
	if ns := top.NsPerOp(); ns > 500_000 {
		t.Errorf("10-node RenderTop %d ns/op, budget 500000", ns)
	}
	page := testing.Benchmark(BenchmarkRenderPage72)
	if ns := page.NsPerOp(); ns > 5_000_000 {
		t.Errorf("72-node page %d ns/op, budget 5000000", ns)
	}
}

// RenderNode takes nodes that never went through Validate; an id outside the
// grammar must not reach the markup, on the node or on a child.
func TestRenderNodeRejectsUnsafeIDs(t *testing.T) {
	e, _ := New(Options{})
	evil := `x" onmouseover="alert(1)`
	for name, n := range map[string]*builder.Node{
		"node":  {ID: evil, T: "text", P: map[string]any{"text": "hi"}},
		"child": {ID: "ok", T: "container", C: []*builder.Node{{ID: evil, T: "text", P: map[string]any{"text": "hi"}}}},
	} {
		for _, canvas := range []bool{false, true} {
			var buf bytes.Buffer
			if err := e.RenderNode(&buf, minimalView(), n, canvas); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(buf.Bytes(), []byte("onmouseover")) {
				t.Errorf("%s canvas=%v: unsafe id in markup: %s", name, canvas, buf.String())
			}
		}
	}
}

// The error and 404 pages render without the page's doc: a failing component
// must not be rendered again, nor its CSS injected.
func TestErrorPagesDropDoc(t *testing.T) {
	e, _ := New(Options{})
	v := minimalView()
	v.Doc = &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "zz", T: "text", P: map[string]any{"text": "DOC-MARKER"}, S: map[string]map[string]string{"base": {"gap": "space-4"}}}}}
	for name, render := range map[string]func(*bytes.Buffer) error{
		"error":    func(b *bytes.Buffer) error { return e.RenderError(b, v, 500) },
		"notfound": func(b *bytes.Buffer) error { return e.RenderNotFound(b, v) },
	} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b.Bytes(), []byte("DOC-MARKER")) || bytes.Contains(b.Bytes(), []byte("hm-n-zz")) {
			t.Errorf("%s page rendered the doc:\n%s", name, b.String())
		}
	}
}
