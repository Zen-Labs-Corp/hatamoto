package render

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/builder"
)

func nativeDoc() *builder.Doc {
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{
		ID: "row", T: "container",
		P: map[string]any{"tag": "section", "layout": "grid"},
		S: map[string]map[string]string{"base": {"ta": "center"}},
		C: []*builder.Node{
			{ID: "h", T: "heading", P: map[string]any{"text": "Hi <b>", "level": float64(1)}},
			{ID: "t", T: "text", P: map[string]any{"text": "<script>alert(1)</script>"}},
		},
	}}}
}

func renderDoc(t *testing.T, layers []fs.FS, d *builder.Doc, canvas bool) string {
	t.Helper()
	e, err := New(Options{Layers: layers})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Doc, v.Canvas = d, canvas
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// D4: every section fixture renders byte-identically through FromSections + Doc.
func TestDocV1ShapedMatchesGoldens(t *testing.T) {
	nowFunc = func() time.Time { return time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = time.Now }()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "fixtures", "*.json"))
	n := 0
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		if name == "head" || name == "page-shell" {
			continue
		}
		n++
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var sf sectionFixture
			if err := json.Unmarshal(raw, &sf); err != nil {
				t.Fatal(err)
			}
			v := minimalView()
			v.Forms, v.Submitted, v.FormError = sf.Forms, sf.Submitted, sf.FormError
			v.PluginDirectory, v.ThemeDirectory = sf.PluginDirectory, sf.ThemeDirectory
			v.Doc = builder.FromSections([]builder.Section{{ID: sf.ID, Type: sf.Type, Title: sf.Title, Content: sf.Content, SortOrder: sf.SortOrder}})
			var buf bytes.Buffer
			if err := e.RenderPage(&buf, v); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "<style id=\"hm-builder") {
				t.Fatal("v1-only doc emitted builder CSS")
			}
			compareGolden(t, name, mainInner(t, buf.String()))
		})
	}
	if n == 0 {
		t.Fatal("no fixtures")
	}
}

// A v1-only doc must be byte-identical to the same Sections page, whole document.
func TestDocV1OnlyWholePageIdentical(t *testing.T) {
	secs := []builder.Section{
		{ID: "a", Type: "content", Content: map[string]any{"body": "<p>one</p>"}, SortOrder: 2},
		{ID: "b", Type: "content", Content: map[string]any{"body": "<p>two</p>"}, SortOrder: 1},
	}
	e, _ := New(Options{})
	v1, v2 := minimalView(), minimalView()
	for _, s := range secs {
		v1.Sections = append(v1.Sections, Section{ID: s.ID, Type: s.Type, Content: s.Content, SortOrder: s.SortOrder})
	}
	v2.Doc = builder.FromSections(secs)
	var a, b bytes.Buffer
	if err := e.RenderPage(&a, v1); err != nil {
		t.Fatal(err)
	}
	if err := e.RenderPage(&b, v2); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("v1 and doc render differ\n%s\n---\n%s", a.String(), b.String())
	}
}

func mixedDoc() *builder.Doc {
	d := nativeDoc()
	d.Nodes = append(d.Nodes, &builder.Node{ID: "c1", T: "container", P: map[string]any{"tag": "section"}, C: []*builder.Node{
		{ID: "k1", T: "component", P: map[string]any{"type": "content", "content": map[string]any{"body": "<p>v1 body</p>"}}},
	}}, &builder.Node{ID: "nat", T: "container", C: []*builder.Node{
		{ID: "k2", T: "component", P: map[string]any{"type": "content", "content": map[string]any{"body": "<p>nested body</p>"}}},
	}})
	return d
}

func TestNativeNodesBypassSectionWrapper(t *testing.T) {
	override := os.DirFS("testdata/themes/native-override")
	out := renderDoc(t, []fs.FS{override}, mixedDoc(), false)
	if strings.Count(out, `class="custom-wrap"`) != 1 {
		t.Fatalf("want exactly the v1-shaped node wrapped by the theme section:\n%s", mainInner(t, out))
	}
	for _, want := range []string{`hm-n-row`, "nested body", "v1 body"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, `data-theme-section="row"`) || strings.Contains(out, `data-theme-section="nat"`) {
		t.Error("native container wrapped by theme section")
	}
	if !strings.Contains(out, `<section class="hm-n hm-c hm-c--grid hm-n-row">`) {
		t.Error("native tree not printed bare")
	}
	// Same default theme: no <section class="hm-section"> around the native tree.
	def := renderDoc(t, nil, mixedDoc(), false)
	m := mainInner(t, def)
	if !strings.HasPrefix(m, `<section class="hm-n hm-c hm-c--grid hm-n-row">`) {
		t.Errorf("native node is wrapped: %.120s", m)
	}
	if strings.Count(m, `class="hm-section hm-section--content"`) != 1 {
		t.Errorf("only the v1-shaped node should use the theme section:\n%s", m)
	}
}

func TestNativeCSSInjection(t *testing.T) {
	out := renderDoc(t, nil, nativeDoc(), false)
	if strings.Count(out, `id="hm-builder-css"`) != 1 {
		t.Fatalf("want one style tag:\n%s", out)
	}
	if !strings.Contains(out, ".hm-n-row{text-align:center") {
		t.Error("node css missing")
	}
	if strings.Index(out, `id="hm-builder-css"`) > strings.Index(out, "</head>") {
		t.Error("css not in head")
	}
}

func TestNativeCSSFallbackSplice(t *testing.T) {
	out := renderDoc(t, []fs.FS{os.DirFS("testdata/themes/no-plugin-head")}, nativeDoc(), false)
	i, h := strings.Index(out, `id="hm-builder-css"`), strings.Index(out, "</head>")
	if i < 0 || h < 0 || i > h || strings.Count(out, `id="hm-builder-css"`) != 1 {
		t.Fatalf("css not spliced once before </head>:\n%s", out)
	}
}

func TestNativeTextEscaped(t *testing.T) {
	out := renderDoc(t, nil, nativeDoc(), false)
	if strings.Contains(out, "<script>alert") || !strings.Contains(out, "&lt;script&gt;alert(1)") {
		t.Errorf("text not escaped:\n%s", mainInner(t, out))
	}
	if !strings.Contains(out, "Hi &lt;b&gt;") || !strings.Contains(out, "<h1 ") {
		t.Error("heading not escaped or wrong level")
	}
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "b", T: "button", P: map[string]any{"text": "x", "href": "javascript:alert(1)"}}}}
	if o := renderDoc(t, nil, d, false); strings.Contains(o, "javascript:") {
		t.Error("unsafe href emitted")
	}
}

func TestV1OnlyDocEmitsNoStyle(t *testing.T) {
	d := builder.FromSections([]builder.Section{{ID: "a", Type: "content", Content: map[string]any{"body": "<p>x</p>"}}})
	if out := renderDoc(t, nil, d, false); strings.Contains(out, "hm-builder") {
		t.Error("v1-only public doc emitted builder style")
	}
}

func TestNodeMarkerOnlyInCanvas(t *testing.T) {
	pub := renderDoc(t, nil, mixedDoc(), false)
	if strings.Contains(pub, "data-hm-node") || strings.Contains(pub, "data-hm-prop") || strings.Contains(pub, "data-hm-t=") {
		t.Error("public output carries node markers")
	}
	can := renderDoc(t, nil, mixedDoc(), true)
	if !strings.Contains(can, `data-hm-node="row" data-hm-t="container"`) || !strings.Contains(can, `data-hm-css="row"`) || !strings.Contains(can, `id="hm-builder-base"`) {
		t.Errorf("canvas markers missing:\n%s", can)
	}
}

func TestFailingNativeComponentDegrades(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "row", T: "container", C: []*builder.Node{
		{ID: "bad", T: "component", P: map[string]any{"type": "faq", "content": map[string]any{"faqs": "not a list"}}},
		{ID: "ok", T: "text", P: map[string]any{"text": "still here"}},
	}}}}
	out := renderDoc(t, nil, d, false)
	if !strings.Contains(out, "<!-- section render failed: faq -->") || !strings.Contains(out, SectionFailedMarker) {
		t.Errorf("no failure marker:\n%s", mainInner(t, out))
	}
	if !strings.Contains(out, "still here") {
		t.Error("sibling lost")
	}
}

func TestHiddenNodesSkipped(t *testing.T) {
	d := nativeDoc()
	d.Nodes[0].C[0].P["hidden"] = true
	if out := renderDoc(t, nil, d, false); strings.Contains(out, "Hi &lt;b&gt;") {
		t.Error("hidden node rendered")
	}
	d.Nodes[0].P["hidden"] = true
	if out := renderDoc(t, nil, d, false); strings.Contains(out, "hm-n-row") || strings.Contains(out, "hm-builder") {
		t.Error("hidden top-level node rendered or styled")
	}
}

// A renamed v1 section (navlabel on the container and the component) stays v1: same public bytes.
func TestDocV1NavlabelKeepsPublicRender(t *testing.T) {
	secs := []builder.Section{{ID: "a", Type: "content", Content: map[string]any{"body": "<p>one</p>"}}}
	e, _ := New(Options{})
	plain, named := minimalView(), minimalView()
	plain.Doc = builder.FromSections(secs)
	named.Doc = builder.FromSections(secs)
	named.Doc.Nodes[0].P["navlabel"] = "Hero"
	named.Doc.Nodes[0].C[0].P["navlabel"] = "Hero body"
	var a, b bytes.Buffer
	if err := e.RenderPage(&a, plain); err != nil {
		t.Fatal(err)
	}
	if err := e.RenderPage(&b, named); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("navlabel changed the public render\n%s\n---\n%s", a.String(), b.String())
	}
	if strings.Contains(b.String(), "Hero") {
		t.Fatal("navlabel leaked into the page")
	}
}

// Every child-capable kind carries the canvas floor marker, with or without children, so a node emptied
// in the canvas (the bridge only detaches DOM) still matches [data-hm-holds]:empty. Images, dividers and spacers keep their size.
func TestCanvasHoldsMarkerOnEveryContainer(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		{ID: "box", T: "container"},
		{ID: "full", T: "container", C: []*builder.Node{{ID: "tx", T: "text", P: map[string]any{"text": "x"}}}},
		{ID: "gone", T: "container", C: []*builder.Node{{ID: "hid", T: "text", P: map[string]any{"text": "x"}, S: map[string]map[string]string{"base": {"display": "none"}}}}},
		{ID: "sp", T: "spacer"},
		{ID: "dv", T: "divider"},
		{ID: "im", T: "image", P: map[string]any{"src": "/a.png", "alt": "a"}},
	}}
	out := renderDoc(t, nil, d, true)
	if n := strings.Count(out, "data-hm-holds"); n < 3 {
		t.Errorf("want data-hm-holds on box, full and gone, got %d:\n%s", n, mainInner(t, out))
	}
	for _, id := range []string{"box", "full"} {
		if !strings.Contains(out, `data-hm-node="`+id+`" data-hm-t="container" data-hm-holds`) {
			t.Errorf("%s lacks the marker", id)
		}
	}
	for _, id := range []string{"sp", "dv", "im", "tx"} {
		if strings.Contains(out, `data-hm-node="`+id+`"`) && strings.Contains(tagOf(out, id), "data-hm-holds") {
			t.Errorf("%s must not carry the marker", id)
		}
	}
	// the parent of a removed only child renders as the bridge leaves it: no children
	after := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "full", T: "container"}}}
	if got := renderDoc(t, nil, after, true); !strings.Contains(got, `data-hm-holds></`) {
		t.Errorf("emptied container does not match [data-hm-holds]:empty:\n%s", mainInner(t, got))
	}
	if strings.Contains(renderDoc(t, nil, d, false), "data-hm-holds") {
		t.Error("public output carries data-hm-holds")
	}
}

// tagOf returns the opening tag of node id in out.
func tagOf(out, id string) string {
	i := strings.Index(out, `data-hm-node="`+id+`"`)
	if i < 0 {
		return ""
	}
	return out[i : i+strings.Index(out[i:], ">")]
}
