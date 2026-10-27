package render

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/builder"
)

// The schema side (builder) and the view side (render) of widgets are two
// directory trees that must list the same kinds.
func TestWidgetKindSetsEqual(t *testing.T) {
	var schema, view []string
	for _, k := range builder.Kinds() {
		if k.Group != "" {
			schema = append(schema, k.Kind)
		}
	}
	view = append(view, widgets().kinds...)
	sort.Strings(schema)
	sort.Strings(view)
	if strings.Join(schema, ",") != strings.Join(view, ",") {
		t.Fatalf("builder widget kinds %v != render widget dirs %v", schema, view)
	}
}

func hasChildKind(n *builder.Node, kind string) bool {
	for _, c := range n.C {
		if c.T == kind {
			return true
		}
	}
	return false
}

// Each widget kind's schema accepts a fresh seeded tree and rejects an unknown prop.
func TestWidgetSchemasAcceptSeedRejectUnknown(t *testing.T) {
	for _, k := range builder.Kinds() {
		if k.Group == "" {
			continue
		}
		n, err := builder.NewTree(k.Kind)
		if err != nil && k.OnlyIn != "" { // a kind that only sits inside a parent is built there
			n, err = builder.NewTree(k.OnlyIn)
			if err == nil && !hasChildKind(&n, k.Kind) {
				var c *builder.Node
				if c, err = builder.NewNode(k.Kind, "inner"); err == nil {
					n.C = append(n.C, c)
				}
			}
		}
		if err != nil {
			t.Errorf("%s: NewTree: %v", k.Kind, err)
			continue
		}
		doc := func() *builder.Doc { return &builder.Doc{V: 2, Nodes: []*builder.Node{&n}} }
		if err := doc().Validate(); err != nil {
			t.Errorf("%s: seeded tree invalid: %v", k.Kind, err)
		}
		target := &n
		if k.OnlyIn != "" {
			for _, c := range n.C {
				if c.T == k.Kind {
					target = c
				}
			}
		}
		target.P = map[string]any{"no_such_prop": "x"}
		if err := doc().Validate(); err == nil {
			t.Errorf("%s: unknown prop accepted", k.Kind)
		}
	}
}

func TestEmbeddedWidgetFilesPresent(t *testing.T) {
	for _, k := range widgets().kinds {
		for _, f := range []string{"widget.html", "widget.css"} {
			if _, err := fs.Stat(widgetFS, "widgets/"+k+"/"+f); err != nil {
				t.Errorf("%s: %v", k, err)
			}
		}
		if widgets().css[k] == "" {
			t.Errorf("%s: empty css", k)
		}
	}
}

func TestMinifyCSS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/* c */ .a {\n  color: red ;\n  margin: 0  auto;\n}\n", ".a{color:red;margin:0 auto}"},
		{".a , .b > .c { x:y }", ".a,.b>.c{x:y}"},
		{".a :hover{x:1}", ".a :hover{x:1}"},
		{`.a::after { content: "a  {  b" ; }`, `.a::after{content:"a  {  b"}`},
		{"@media (max-width: 640px) {\n .a { gap: calc(1rem + 2px) }\n}", "@media (max-width:640px){.a{gap:calc(1rem + 2px)}}"},
		{".a{x:var(--hm-y, 1rem)}", ".a{x:var(--hm-y,1rem)}"},
		{"", ""},
	}
	for _, c := range cases {
		if got := minifyCSS(c.in); got != c.want {
			t.Errorf("minifyCSS(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func styleBody(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<style id="hm-builder-css">`)
	if i < 0 {
		t.Fatalf("no builder css in %.300s", page)
	}
	i += len(`<style id="hm-builder-css">`)
	return page[i : i+strings.Index(page[i:], "</style>")]
}

func statDoc() *builder.Doc {
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "s", T: "stat", P: map[string]any{"value": "7"}}}}
}

func textDoc() *builder.Doc {
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "t", T: "text", P: map[string]any{"text": "hi"}, S: map[string]map[string]string{"base": {"ta": "center"}}}}}
}

// Kind CSS is emitted for the kinds on the page only; a page of old kinds has
// the same head CSS as before widgets existed.
func TestKindCSSOnlyForPresentKinds(t *testing.T) {
	stat := widgets().css["stat"]
	with := styleBody(t, renderDoc(t, nil, statDoc(), false))
	if !strings.HasPrefix(with, BaseCSS+stat) {
		t.Errorf("stat page css = %.200s", with)
	}
	if strings.Count(with, ".hm-stat{") != 1 {
		t.Errorf("stat css count in %q", with)
	}
	without := styleBody(t, renderDoc(t, nil, textDoc(), false))
	if want := BaseCSS + builder.NodeCSS(textDoc().Nodes[0]); without != want {
		t.Errorf("old-kind page css changed:\n got %s\nwant %s", without, want)
	}
	// Nested widgets are found.
	nested := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "r", T: "container", C: []*builder.Node{{ID: "s", T: "stat"}}}}}
	if !strings.Contains(styleBody(t, renderDoc(t, nil, nested, false)), ".hm-stat{") {
		t.Error("nested stat css missing")
	}
}

// The canvas base carries every widget's CSS: a patch can add a widget the page did not have.
func TestCanvasBaseHasAllKindCSS(t *testing.T) {
	out := renderDoc(t, nil, textDoc(), true)
	i := strings.Index(out, `<style id="hm-builder-base">`)
	if i < 0 || !strings.Contains(out[i:i+strings.Index(out[i:], "</style>")], ".hm-stat{") {
		t.Error("canvas base lacks widget css")
	}
}

func TestWidgetRenderUnknownAndEscaping(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "s", T: "stat", P: map[string]any{"value": "<i>1</i>", "label": `"x"`}}}}
	out := mainInner(t, renderDoc(t, nil, d, false))
	if strings.Contains(out, "<i>") || !strings.Contains(out, "&lt;i&gt;1&lt;/i&gt;") {
		t.Errorf("value not escaped: %s", out)
	}
	hidden := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "s", T: "stat", P: map[string]any{"hidden": true}}}}
	if strings.Contains(mainInner(t, renderDoc(t, nil, hidden, false)), "hm-stat") {
		t.Error("hidden widget rendered")
	}
}

// withIslandSet swaps in a widget set where stat has an island script.
func withIslandSet(t *testing.T) widgetIsland {
	t.Helper()
	old := widgets()
	files := fstest.MapFS{}
	for _, f := range []string{"widget.html", "widget.css"} {
		b, _ := fs.ReadFile(widgetFS, "widgets/stat/"+f)
		files["widgets/stat/"+f] = &fstest.MapFile{Data: b}
	}
	files["widgets/stat/island.js"] = &fstest.MapFile{Data: []byte("export default 1;\n")}
	ws, err := loadWidgetSet(files, widgetPreps)
	if err != nil {
		t.Fatal(err)
	}
	defaultWidgets = ws
	t.Cleanup(func() { defaultWidgets = old })
	return ws.island["stat"]
}

func TestIslandNameIsContentHash(t *testing.T) {
	a, b := newWidgetIsland("tabs", []byte("a")), newWidgetIsland("tabs", []byte("b"))
	if a.name == b.name || !strings.HasPrefix(a.name, "tabs.") || !strings.HasSuffix(a.name, ".js") || len(a.name) != len("tabs.")+8+len(".js") {
		t.Errorf("names %q %q", a.name, b.name)
	}
	if a.name != newWidgetIsland("tabs", []byte("a")).name {
		t.Error("name not stable")
	}
}

func TestIslandsPublicOnly(t *testing.T) {
	is := withIslandSet(t)
	tag := `<script type="module" src="/theme/assets/builder/` + is.name + `"></script>`

	pub := renderDoc(t, nil, statDoc(), false)
	if strings.Count(pub, tag) != 1 {
		t.Errorf("public page has %d island tags", strings.Count(pub, tag))
	}
	if strings.Count(pub, "<script") != strings.Count(pub, `src=`)+strings.Count(pub, "<script type=\"application/ld+json\"") {
		t.Logf("inline scripts present (theme): fine if from the theme")
	}
	if got := renderDoc(t, nil, statDoc(), true); strings.Contains(got, "/theme/assets/builder/") {
		t.Error("canvas page has an island")
	}
	if got := renderDoc(t, nil, textDoc(), false); strings.Contains(got, "/theme/assets/builder/") {
		t.Error("page without the kind has an island")
	}
	// A theme that never prints plugin-body still gets the island before </body>.
	bare := renderDoc(t, []fs.FS{os.DirFS("testdata/themes/no-plugin-head")}, statDoc(), false)
	i, j := strings.Index(bare, tag), strings.LastIndex(bare, "</body>")
	if strings.Count(bare, tag) != 1 || i < 0 || j < i {
		t.Errorf("fallback splice failed: %s", bare)
	}
}

func TestIslandServedImmutable(t *testing.T) {
	is := withIslandSet(t)
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := e.AssetHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+IslandPrefix+is.name, nil))
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), is.body) {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("content-type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("cache-control %q", cc)
	}
	for _, bad := range []string{"stat.00000000.js", "nope.js", ""} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+IslandPrefix+bad, nil))
		if rec.Code != 404 {
			t.Errorf("%q: status %d", bad, rec.Code)
		}
	}
}

// pageStat72 is pageDoc72's shape with every leaf a stat widget: 72 nodes.
func pageStat72() *builder.Doc {
	d := pageDoc72()
	var walk func(ns []*builder.Node)
	walk = func(ns []*builder.Node) {
		for _, n := range ns {
			if len(n.C) > 0 {
				walk(n.C)
				continue
			}
			n.T, n.P = "stat", map[string]any{"value": "42", "label": "Label " + n.ID}
		}
	}
	walk(d.Nodes)
	return d
}

func BenchmarkRenderPageStat72(b *testing.B) {
	e, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	v := minimalView()
	v.Doc = pageStat72()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.RenderPage(io.Discard, v); err != nil {
			b.Fatal(err)
		}
	}
}

// Wall-clock backstop outside make budgets: template-per-widget stays inside a
// ceiling >= 10x the dev-machine measurement (650 us per op).
func TestWidgetRenderBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing")
	}
	if n := pageStat72().Count(); n != 72 {
		t.Fatalf("fixture has %d nodes, want 72", n)
	}
	if ns := testing.Benchmark(BenchmarkRenderPageStat72).NsPerOp(); ns > 10_000_000 {
		t.Errorf("72-node widget page %d ns/op, budget 10000000", ns)
	}
}

// A widget's fallback CSS must never out-specify a per-node rule
// (.hm-n-<id>, one class). In a kind with children, a selector whose subject is
// a bare element (".hm-gallery img") can match a child node and would beat
// "Hide on Tablet" or a width set on that node; the element must be in :where().
func TestWidgetCSSKeepsNodeRulesWinning(t *testing.T) {
	withChildren := map[string]bool{}
	for _, k := range builder.Kinds() {
		if k.Children {
			withChildren[k.Kind] = true
		}
	}
	whereRe := regexp.MustCompile(`:where\([^)]*\)`)
	bareSubject := regexp.MustCompile(`(^|[\s>+~])[a-z][a-z0-9]*$`)
	for _, k := range widgets().kinds {
		b, err := fs.ReadFile(widgetFS, "widgets/"+k+"/widget.css")
		if err != nil || !withChildren[k] {
			continue
		}
		for _, rule := range strings.Split(minifyCSS(string(b)), "}") {
			sel, _, ok := strings.Cut(rule, "{")
			if !ok || strings.HasPrefix(strings.TrimSpace(sel), "@") {
				continue
			}
			for _, s := range strings.Split(sel, ",") {
				if bareSubject.MatchString(whereRe.ReplaceAllString(strings.TrimSpace(s), "")) {
					t.Errorf("%s: selector %q can out-specify a per-node rule; wrap the element in :where()", k, s)
				}
			}
		}
	}
}

// KindInfo.Display (what the editor writes when it un-hides a node) must be the
// display the kind's rendered root gives. The test renders each kind through the
// engine, reads the class list of the root element and resolves its display from
// BaseCSS and every widget.css; a root with no display rule is block (table for a
// <table>). A new kind is rendered here too, so none can be skipped.
func TestKindDisplayMatchesRenderedRoot(t *testing.T) {
	css := BaseCSS
	ents, err := fs.ReadDir(widgetFS, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ents {
		if b, err := fs.ReadFile(widgetFS, "widgets/"+d.Name()+"/widget.css"); err == nil {
			css += "\n" + string(b)
		}
	}
	displayOf := func(class string) string { return cssDisplay(css, class) }
	rootRe := regexp.MustCompile(`^\s*<([a-z0-9]+)[^>]*?class="([^"]*)"`)
	e := newTestEngine(t, Options{})
	v := minimalView()
	for _, k := range builder.Kinds() {
		n, err := builder.NewTree(k.Kind)
		root := &n                        // the node whose own root element is checked
		if err != nil && k.OnlyIn != "" { // a kind that only sits inside a parent
			if n, err = builder.NewTree(k.OnlyIn); err == nil && !hasChildKind(&n, k.Kind) {
				c, cerr := builder.NewNode(k.Kind, "inner")
				if cerr != nil {
					t.Fatalf("%s: %v", k.Kind, cerr)
				}
				n.C = append(n.C, c)
			}
			for _, c := range n.C {
				if c.T == k.Kind {
					root = c
					break
				}
			}
		}
		switch k.Kind {
		case "component": // a component is its theme section's template: no wrapper of ours
			if k.Display != "block" {
				t.Errorf("component: Display = %q, want block", k.Display)
			}
			continue
		case "ref": // the ref node is expanded before render; an empty one still has its wrapper
			n, err = builder.Node{ID: "r1", T: "ref", P: map[string]any{"tag": "div"}}, nil
			root = &n
		}
		if err != nil {
			t.Errorf("%s: cannot build a node: %v", k.Kind, err)
			continue
		}
		html := renderBound(t, v, e, &n, true) // canvas: data widgets show a placeholder instead of nothing
		if k.OnlyIn != "" {
			// the parent's element comes first; start at the child's own root (its id class)
			if i := strings.Index(html, "hm-n-"+root.ID); i >= 0 {
				html = html[strings.LastIndex(html[:i], "<"):]
			}
		}
		m := rootRe.FindStringSubmatch(html)
		if m == nil {
			// a slot writes no wrapper of its own (it is its contents)
			if k.Kind != "slot" {
				t.Errorf("%s: renders no root element: %q", k.Kind, html)
			} else if k.Display != "block" {
				t.Errorf("%s: no root element, Display = %q, want block", k.Kind, k.Display)
			}
			continue
		}
		want := "block"
		if m[1] == "table" {
			want = "table"
		}
		for _, class := range strings.Fields(m[2]) {
			if d := displayOf(class); d != "" {
				want = d
			}
		}
		if k.Display != want {
			t.Errorf("%s: Display = %q, rendered root <%s class=%q> resolves to %q", k.Kind, k.Display, m[1], m[2], want)
		}
	}
}

// cssDisplay resolves the display a single class sets: every rule whose
// selector list contains ".class" is considered, and the last one wins.
func cssDisplay(css, class string) string {
	got := ""
	for _, rule := range strings.Split(minifyCSS(css), "}") {
		sel, body, ok := strings.Cut(rule, "{")
		if !ok {
			continue
		}
		match := false
		for _, s := range strings.Split(sel, ",") {
			if strings.TrimSpace(s) == "."+class {
				match = true
			}
		}
		if !match {
			continue
		}
		for _, d := range strings.Split(body, ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(d), "display:"); ok {
				got = strings.TrimSpace(v)
			}
		}
	}
	return got
}

func TestCSSDisplayGroupedAndCascade(t *testing.T) {
	css := ".a, .b { display: grid; } .c{color:red} .b{display:flex} .a{margin:0}"
	for class, want := range map[string]string{"a": "grid", "b": "flex", "c": "", "d": ""} {
		if got := cssDisplay(css, class); got != want {
			t.Errorf("cssDisplay(.%s) = %q, want %q", class, got, want)
		}
	}
}
