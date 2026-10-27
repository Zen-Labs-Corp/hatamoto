package render

import (
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// element cuts the element whose start tag contains marker out of html, by
// counting open and close tags of its name.
func element(t *testing.T, html, marker string) string {
	t.Helper()
	at := strings.Index(html, marker)
	if at < 0 {
		t.Fatalf("marker %q not in page", marker)
	}
	start := strings.LastIndex(html[:at], "<")
	name := regexp.MustCompile(`^<([a-z0-9]+)`).FindStringSubmatch(html[start:])
	if name == nil {
		t.Fatalf("no tag at %.40q", html[start:])
	}
	open, shut := "<"+name[1], "</"+name[1]+">"
	depth, i := 0, start
	for i < len(html) {
		switch {
		case strings.HasPrefix(html[i:], shut):
			depth--
			i += len(shut)
			if depth == 0 {
				return html[start:i]
			}
		case strings.HasPrefix(html[i:], open) && i+len(open) < len(html) && strings.ContainsRune(" >", rune(html[i+len(open)])):
			depth++
			i += len(open)
		default:
			i++
		}
	}
	t.Fatalf("unclosed %s", name[1])
	return ""
}

func threeSections() *builder.Doc {
	return builder.FromSections([]builder.Section{
		{ID: "a", Type: "content", SortOrder: 1, Content: map[string]any{"body": "<p>one</p>"}},
		{ID: "b", Type: "content", SortOrder: 2, Content: map[string]any{"body": "<p>two</p>"}},
		{ID: "c", Type: "content", SortOrder: 3, Content: map[string]any{"body": "<p>three</p>"}},
	})
}

func TestRenderTopMatchesFullCanvas(t *testing.T) {
	for name, d := range map[string]*builder.Doc{"converted": threeSections(), "mixed": mixedDoc()} {
		t.Run(name, func(t *testing.T) {
			page := renderDoc(t, nil, d, true)
			e, _ := New(Options{})
			v := minimalView()
			v.Doc, v.Canvas = d, true
			for _, n := range d.Nodes {
				f, ok, err := e.RenderTop(v, n)
				if err != nil || !ok {
					t.Fatalf("%s: ok=%v err=%v", n.ID, ok, err)
				}
				marker := `data-hm-node="` + n.ID + `"`
				if f.Sel == "section" {
					marker = `data-hm-section="` + f.Key + `"`
				}
				if !strings.Contains(page, f.HTML) {
					t.Errorf("%s: fragment is not a substring of the page", n.ID)
				}
				if want := element(t, page, marker); f.HTML != want {
					t.Errorf("%s: fragment differs from page slice\n%s\n---\n%s", n.ID, f.HTML, want)
				}
				if n.ID == "row" && f.CSS["row"] == "" {
					t.Errorf("row fragment lacks CSS: %v", f.CSS)
				}
			}
		})
	}
}

func TestRenderTopKinds(t *testing.T) {
	e, _ := New(Options{})
	v := minimalView()
	v.Doc, v.Canvas = mixedDoc(), true
	f, _, _ := e.RenderTop(v, v.Doc.Nodes[0])
	if f.Sel != "node" || f.Key != "row" || !strings.Contains(f.HTML, `data-hm-t="container"`) || !strings.Contains(f.HTML, `data-hm-t="heading"`) {
		t.Errorf("native fragment: %+v", f)
	}
	f, _, _ = e.RenderTop(v, v.Doc.Nodes[1])
	if f.Sel != "section" || f.Key != "k1" || len(f.CSS) != 0 {
		t.Errorf("v1-shaped fragment: %+v", f)
	}
	hidden := threeSections()
	hidden.Nodes[1].P["hidden"] = true
	if _, ok, _ := e.RenderTop(v, hidden.Nodes[1]); ok {
		t.Error("hidden node rendered")
	}
}

func TestRenderTopUnknownTypeDegrades(t *testing.T) {
	e, _ := New(Options{})
	v := minimalView()
	d := builder.FromSections([]builder.Section{{ID: "x", Type: "no-such-type", SortOrder: 1}})
	v.Doc, v.Canvas = d, true
	f, ok, err := e.RenderTop(v, d.Nodes[0])
	if err != nil || !ok || !strings.HasPrefix(f.HTML, "<!--") {
		t.Errorf("unknown type: %q ok=%v err=%v", f.HTML, ok, err)
	}
}

// A bare top-level component that renders nothing (a blank content section) is
// absent from a full render, with no stray sentinel comment, as RenderTop reports.
func TestBlankTopLevelComponentRendersNothing(t *testing.T) {
	e, _ := New(Options{})
	v := minimalView()
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		{ID: "k1", T: "component", P: map[string]any{"type": "content"}},
		{ID: "row", T: "container"},
	}}
	v.Doc, v.Canvas = d, true
	if _, ok, _ := e.RenderTop(v, d.Nodes[0]); ok {
		t.Fatal("blank component rendered a fragment")
	}
	var out strings.Builder
	if err := e.RenderPage(&out, v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<!-- native -->") || !strings.Contains(out.String(), `data-hm-node="row"`) {
		t.Errorf("full render: %.300q", out.String())
	}
}
