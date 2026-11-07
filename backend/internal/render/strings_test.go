package render

import (
	"bytes"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

const stringsLayout = `{{t . "greeting"}}|{{t . "count" 3}}|{{t . "nope"}}|{{t . "markup"}}|` +
	`{{template "partials/p" dict "Strings" .Strings}}|{{template "partials/q" .}}|` +
	`{{range .Blocks}}[{{t . "greeting"}}]{{end}}`

func stringsTheme(decl string) fstest.MapFS {
	return fstest.MapFS{
		"theme.json":         mapFile(`{"name":"st","version":"1.0.0","strings":` + decl + `}`),
		"layouts/base.html":  mapFile(stringsLayout),
		"partials/p.html":    mapFile(`{{define "partials/p"}}P:{{t . "greeting"}}{{end}}`),
		"partials/q.html":    mapFile(`{{define "partials/q"}}Q:{{t . "greeting"}}{{end}}`),
		"sections/note.html": mapFile(`{{define "section-note"}}n{{end}}`),
	}
}

const stringsDecl = `{"greeting":"Hello","count":"{n} items","markup":"a \"b\" 'c'"}`

func renderStrings(t *testing.T, layers []fstest.MapFS, ov map[string]string) string {
	t.Helper()
	var fsl []fs.FS
	for _, l := range layers {
		fsl = append(fsl, l)
	}
	e, err := New(Options{Layers: fsl})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.StringOverrides = ov
	v.Sections = []Section{{ID: "s1", Type: "note", Content: map[string]any{}}}
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestTFuncDefaultsAndInterpolation(t *testing.T) {
	out := renderStrings(t, []fstest.MapFS{stringsTheme(stringsDecl)}, nil)
	// `markup` is escaped by html/template; the rest is verbatim.
	if !strings.HasPrefix(out, "Hello|3 items|nope|a &#34;b&#34; &#39;c&#39;|P:Hello|Q:Hello|") {
		t.Fatalf("defaults: %q", out)
	}
	if !strings.Contains(out, "[Hello]") {
		t.Fatalf("section strings missing: %q", out)
	}
}

func TestTFuncOverrides(t *testing.T) {
	ov := map[string]string{"greeting": "Bonjour <b>", "count": "{n} choses", "undeclared": "x"}
	out := renderStrings(t, []fstest.MapFS{stringsTheme(stringsDecl)}, ov)
	if !strings.Contains(out, "Bonjour &lt;b&gt;|3 choses|nope|") {
		t.Fatalf("override: %q", out)
	}
	if strings.Contains(out, "<b>") || !strings.Contains(out, "P:Bonjour &lt;b&gt;") || !strings.Contains(out, "[Bonjour &lt;b&gt;]") {
		t.Fatalf("escaping / dict / section: %q", out)
	}
}

func TestTFuncAttributeEscaping(t *testing.T) {
	th := stringsTheme(stringsDecl)
	th["layouts/base.html"] = mapFile(`<input aria-label="{{t . "greeting"}}" placeholder='{{t . "greeting"}}'>`)
	out := renderStrings(t, []fstest.MapFS{th}, map[string]string{"greeting": `x" onfocus="y<`})
	if strings.Contains(out, `" onfocus="`) || !strings.Contains(out, `aria-label="x&#34; onfocus=&#34;y&lt;"`) {
		t.Fatalf("attribute: %q", out)
	}
}

func TestTFuncUnknownDotReturnsKey(t *testing.T) {
	for _, v := range []any{nil, 5, "x", &PageView{}, map[string]any{}, map[string]any{"Strings": 3}, pageData{}, &sectionPage{}} {
		if got := tFunc(v, "k", 1); got != "k" {
			t.Errorf("tFunc(%T) = %q", v, got)
		}
	}
}

func TestChildThemeInheritsDefaultStrings(t *testing.T) {
	child := stringsTheme(`{"greeting":"Child hello"}`)
	base := stringsTheme(stringsDecl)
	out := renderStrings(t, []fstest.MapFS{child, base}, nil)
	// the child's declaration wins per key; keys it omits come from below
	if !strings.HasPrefix(out, "Child hello|3 items|") {
		t.Fatalf("chain: %q", out)
	}
}

func TestLoadThemeDropsInvalidStrings(t *testing.T) {
	th, err := LoadTheme(Chain{stringsTheme(`{"ok":"Fine","Bad Key":"x","html":"<b>","amp":"a & b","long":"` + strings.Repeat("x", 301) + `","empty":""}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Strings) != 1 || th.Strings["ok"] != "Fine" {
		t.Fatalf("strings = %v", th.Strings)
	}
	th, err = LoadTheme(Chain{stringsTheme(`[1]`)})
	if err != nil || th.Strings != nil {
		t.Fatalf("malformed: %v %v", th.Strings, err)
	}
}

func TestPluginSectionMayCallT(t *testing.T) {
	const plugin = "strings-test"
	t.Cleanup(func() { RemovePlugin(plugin) })
	if err := RegisterPluginSection(plugin, "stringsprobe", `{{define "section-stringsprobe"}}<p>{{t . "greeting"}}</p>{{end}}`); err != nil {
		t.Fatal(err)
	}
	th := stringsTheme(stringsDecl)
	th["layouts/base.html"] = mapFile(`{{range .Blocks}}{{template "section-stringsprobe" .}}{{end}}`)
	e, err := New(Options{Layers: []fs.FS{th}})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Sections = []Section{{ID: "s1", Type: "stringsprobe", Content: map[string]any{}}}
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<p>Hello</p>") {
		t.Fatalf("plugin section: %q", buf.String())
	}
}

func TestSmokeRenderWithT(t *testing.T) {
	th := stringsTheme(stringsDecl)
	th["layouts/base.html"] = mapFile(`{{t . "greeting"}}{{t . "count" 2}}`)
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.smokeRender(Chain{th, DefaultFS()}); err != nil {
		t.Fatal(err)
	}
}

// A child theme that calls the default partials the pre-"t" way (no
// "Strings" in the dict) still renders the theme's English text, not keys.
func TestDefaultPartialsOldCallShapesRenderText(t *testing.T) {
	child := fstest.MapFS{
		"theme.json": mapFile(`{"name":"child","version":"1.0.0"}`),
		"layouts/base.html": mapFile(`{{template "nav" .Site.Nav}}|` +
			`{{template "nav" dict "Items" .Site.Nav "Path" .Path}}|` +
			`{{template "entry-meta" dict "Words" "` + strings.Repeat("word ", 400) + `"}}`),
	}
	e, err := New(Options{Layers: []fs.FS{child}})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Site.Nav = []NavItem{{Label: "Home", Href: "/"}}
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, key := range []string{"nav_primary", "nav_menu", "min_read"} {
		if strings.Contains(out, key) {
			t.Errorf("raw key %q in %q", key, out)
		}
	}
	if !strings.Contains(out, "min read") || !strings.Contains(out, "Menu") {
		t.Fatalf("default text missing: %q", out)
	}
}

// Component nodes of one view share one resolved map (no clone per node).
func TestPrepareBlockSharesResolvedStrings(t *testing.T) {
	e, err := New(Options{Layers: []fs.FS{stringsTheme(stringsDecl)}})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.StringOverrides = map[string]string{"greeting": "Hi"}
	sec := Section{ID: "s1", Type: "note", Content: map[string]any{}}
	a, ok1 := prepareBlock(e.current(), v, sec, false, nil)
	b, ok2 := prepareBlock(e.current(), v, sec, false, nil)
	if !ok1 || !ok2 {
		t.Fatal("section not prepared")
	}
	if a.Page.Strings["greeting"] != "Hi" {
		t.Fatalf("override lost: %v", a.Page.Strings)
	}
	if reflect.ValueOf(a.Page.Strings).Pointer() != reflect.ValueOf(b.Page.Strings).Pointer() {
		t.Error("each prepareBlock cloned the strings map")
	}
}
