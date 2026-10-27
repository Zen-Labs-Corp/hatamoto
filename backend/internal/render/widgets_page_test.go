package render

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/internal/builder"
)

// A2/A3 on the page that uses every kind: scripts, CSS budget, kind CSS once.

const pageCSSBudget = 8 << 10 // gzip bytes of <style id="hm-builder-css">

// A theme that prints no scripts of its own.
func jsLessLayers() []fs.FS { return []fs.FS{os.DirFS("testdata/themes/no-plugin-head")} }

func gzipLen(t *testing.T, s string) int {
	t.Helper()
	var b bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return b.Len()
}

// withoutKinds returns a copy of the doc minus every node of the given kinds.
func withoutKinds(nodes []*builder.Node, drop ...string) []*builder.Node {
	var out []*builder.Node
	for _, n := range nodes {
		skip := false
		for _, k := range drop {
			skip = skip || n.T == k
		}
		if skip {
			continue
		}
		cp := *n
		cp.C = withoutKinds(n.C, drop...)
		out = append(out, &cp)
	}
	return out
}

func kindsIn(nodes []*builder.Node, into map[string]bool) {
	for _, n := range nodes {
		into[n.T] = true
		kindsIn(n.C, into)
	}
}

func styledNodes(nodes []*builder.Node) int {
	c := 0
	for _, n := range nodes {
		if len(n.S) > 0 {
			c++
		}
		c += styledNodes(n.C)
	}
	return c
}

// scriptCount is the number of executable <script> tags; JSON-LD is data.
func scriptCount(page string) int {
	return strings.Count(page, "<script") - strings.Count(page, `<script type="application/ld+json"`)
}

var islandScript = regexp.MustCompile(`<script type="module" src="/theme/assets/builder/(tabs|video)\.[0-9a-f]{8}\.js"></script>`)

func TestAllWidgetsFixtureCoversEveryKind(t *testing.T) {
	d := loadWidgetDoc(t, "all")
	have := map[string]bool{}
	kindsIn(d.Nodes, have)
	for _, k := range builder.Kinds() {
		if k.Hidden {
			continue // ref and slot are not widgets; refs_test.go covers them
		}
		if !have[k.Kind] {
			t.Errorf("all.json lacks kind %s", k.Kind)
		}
	}
	if n := styledNodes(d.Nodes); n < 40 {
		t.Errorf("only %d styled nodes, want >= 40", n)
	}
}

// ISC-12: per-page builder CSS of the all-widgets page stays within budget.
func TestBudgetAllWidgetsPageCSS(t *testing.T) {
	d := loadWidgetDoc(t, "all")
	css := styleBody(t, renderDoc(t, nil, d, false))
	gz := gzipLen(t, css)
	t.Logf("all-widgets page css: %d bytes, %d gzip (budget %d)", len(css), gz, pageCSSBudget)
	if gz > pageCSSBudget {
		t.Errorf("builder css %d B gzip > %d", gz, pageCSSBudget)
	}
	budgetfile.Check(t, "render", "page_css_allwidgets_gzip", int64(gz), budgetfile.Metric{Unit: "bytes", Cap: pageCSSBudget, TolAbs: 1024})
	// ISC-13: each present kind's CSS once, in the head only.
	for kind, kcss := range widgets().css {
		if got := strings.Count(css, kcss); got != 1 {
			t.Errorf("kind %s css appears %d times", kind, got)
		}
	}
}

// ISC-9, ISC-10: no script without tabs/video; exactly their islands with them.
func TestAllWidgetsPageScripts(t *testing.T) {
	d := loadWidgetDoc(t, "all")

	noIslands := &builder.Doc{V: 2, Nodes: withoutKinds(d.Nodes, "tabs", "video")}
	page := renderDoc(t, jsLessLayers(), noIslands, false)
	if n := scriptCount(page); n != 0 {
		t.Errorf("page without tabs/video has %d scripts", n)
	}

	page = renderDoc(t, jsLessLayers(), d, false)
	if got := scriptCount(page); got != 2 {
		t.Errorf("page with tabs and video has %d scripts, want 2", got)
	}
	tags := islandScript.FindAllStringSubmatch(page, -1)
	if len(tags) != 2 || tags[0][1] == tags[1][1] {
		t.Errorf("island tags = %v", tags)
	}

	only := &builder.Doc{V: 2, Nodes: withoutKinds(d.Nodes, "video")}
	page = renderDoc(t, jsLessLayers(), only, false)
	if tags := islandScript.FindAllStringSubmatch(page, -1); scriptCount(page) != 1 || len(tags) != 1 || tags[0][1] != "tabs" {
		t.Errorf("tabs-only page scripts: %v", tags)
	}

	if canvas := renderDoc(t, jsLessLayers(), d, true); strings.Contains(canvas, "/theme/assets/builder/") {
		t.Error("canvas has an island")
	}
}

// A disabled (not rendered) tabs or video widget ships neither its CSS nor its island.
func TestDisabledWidgetsShipNoCSSOrIsland(t *testing.T) {
	d := loadWidgetDoc(t, "all")
	var disable func(ns []*builder.Node)
	disable = func(ns []*builder.Node) {
		for _, n := range ns {
			if n.T == "tabs" || n.T == "video" {
				if n.P == nil {
					n.P = map[string]any{}
				}
				n.P["hidden"] = true
			}
			disable(n.C)
		}
	}
	disable(d.Nodes)
	page := renderDoc(t, jsLessLayers(), d, false)
	if n := scriptCount(page); n != 0 {
		t.Errorf("page with only disabled tabs/video has %d scripts", n)
	}
	css := styleBody(t, page)
	for _, k := range []string{"tabs", "tabpanel", "video"} {
		if strings.Contains(css, widgets().css[k]) {
			t.Errorf("disabled %s still ships its css", k)
		}
	}
}

// ISC-11, A-3: with the default theme only the theme's own deferred hm.js, no
// builder island and no inline script, when tabs and video are absent.
func TestAllWidgetsPageDefaultTheme(t *testing.T) {
	d := loadWidgetDoc(t, "all")
	noIslands := &builder.Doc{V: 2, Nodes: withoutKinds(d.Nodes, "tabs", "video")}
	page := renderDoc(t, nil, noIslands, false)
	if strings.Contains(page, "/theme/assets/builder/") || strings.Contains(page, `type="module"`) {
		t.Error("builder island on a page without tabs/video")
	}
	for _, m := range regexp.MustCompile(`<script[^>]*>`).FindAllString(page, -1) {
		if !strings.Contains(m, " src=") && !strings.Contains(m, "application/ld+json") {
			t.Errorf("inline script %s", m)
		}
	}
	full := renderDoc(t, nil, d, false)
	if got := len(islandScript.FindAllString(full, -1)); got != 2 {
		t.Errorf("default theme page has %d island tags, want 2", got)
	}
	// Builder markers only: the v1 component's own data-hm-field is theme markup.
	if m := mainInner(t, full); strings.Contains(m, "data-hm-node") || strings.Contains(m, "data-hm-t=") {
		t.Error("public page carries builder markers")
	}
}

// ISC-14: no builder-generated node has style=; the html widget (sanitised author
// markup) is the only exception and the fixture's html body here has none.
func TestAllWidgetsPageNoInlineStyle(t *testing.T) {
	page := mainInner(t, renderDoc(t, nil, loadWidgetDoc(t, "all"), false))
	if strings.Contains(page, " style=") {
		t.Error("inline style on the public all-widgets page")
	}
}

// ISC-18: a page of old kinds has exactly BaseCSS plus its per-node rules.
func TestOldKindsPageHeadCSSUnchanged(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: nativeDoc().Nodes}
	want := BaseCSS
	for _, n := range []*builder.Node{d.Nodes[0]} {
		want += builder.NodeCSS(n)
		for _, c := range n.C {
			want += builder.NodeCSS(c)
		}
	}
	if got := styleBody(t, renderDoc(t, nil, d, false)); got != want {
		t.Errorf("old-kind head css changed:\n got %s\nwant %s", got, want)
	}
}
