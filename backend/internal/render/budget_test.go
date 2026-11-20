package render

import (
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/internal/builder"
)

// The all-widgets page without tabs/video ships no script at all (exact, not a tolerance).
func TestBudgetPublicBuilderJS(t *testing.T) {
	d := loadWidgetDoc(t, "all")
	noIslands := &builder.Doc{V: 2, Nodes: withoutKinds(d.Nodes, "tabs", "video", "form")}
	n := scriptCount(renderDoc(t, jsLessLayers(), noIslands, false))
	if n != 0 {
		t.Errorf("page without tabs/video has %d scripts, want 0", n)
	}
	budgetfile.Check(t, "render", "public_builder_js_scripts", int64(n), budgetfile.Metric{Unit: "count"})
}

// The default theme's public CSS (concatenated, gzip -9) and hm.js, as served.
func TestBudgetDefaultThemeAssets(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	css, js, _ := buildBundle(e.current().theme)
	budgetfile.Check(t, "render", "theme_default_css_gzip", int64(gzipLen(t, string(css))), budgetfile.Metric{Unit: "bytes", TolAbs: 1024})
	budgetfile.Check(t, "render", "theme_default_js_gzip", int64(gzipLen(t, string(js))), budgetfile.Metric{Unit: "bytes", TolAbs: 512})
	budgetfile.Check(t, "render", "canvas_bridge_gzip", int64(gzipLen(t, canvasBridgeJS)), budgetfile.Metric{Unit: "bytes", TolAbs: 512})
}

// Output size of the committed goldens (what they pin) and of the all-widgets
// page: growth over 5 % fails, even if someone regenerated the goldens.
func TestBudgetGoldenSizes(t *testing.T) {
	for _, name := range []string{"page-shell", "hero", "features", "faq", "pricing", "cta", "tpl-home", "tpl-single", "tpl-archive"} {
		b, err := os.ReadFile(filepath.Join("testdata", "golden", name+".html"))
		if err != nil {
			t.Fatal(err)
		}
		budgetfile.Check(t, "render", "golden_"+name+"_bytes", int64(len(b)), budgetfile.Metric{Unit: "bytes", TolPct: 5})
	}
	page := renderDoc(t, nil, loadWidgetDoc(t, "all"), false)
	budgetfile.Check(t, "render", "page_allwidgets_html_bytes", int64(len(page)), budgetfile.Metric{Unit: "bytes", TolPct: 5})
	// the element-settings page (anchors, links, figure, lightbox): the feature's markup and CSS growth is pinned too
	settings := renderDoc(t, nil, loadWidgetDoc(t, "settings"), false)
	budgetfile.Check(t, "render", "page_settings_html_bytes", int64(len(settings)), budgetfile.Metric{Unit: "bytes", TolPct: 5})
}

// Allocations per operation of the render benchmarks (deterministic; no timing).
func TestBudgetAllocs(t *testing.T) {
	for name, b := range map[string]func(*testing.B){
		"alloc_RenderNode10":     BenchmarkRenderNode10,
		"alloc_RenderTop10":      BenchmarkRenderTop10,
		"alloc_RenderPage72":     BenchmarkRenderPage72,
		"alloc_RenderPageStat72": BenchmarkRenderPageStat72,
		"alloc_Loop12":           BenchmarkLoop12,
	} {
		t.Run(name, func(t *testing.T) { budgetfile.CheckAllocs(t, "render", name, b) })
	}
}

// comments50View is a post with an open comment form and 50 top-level
// comments, each with a reply chain of 1 to 4 levels below it (190 items).
func comments50View() *PageView {
	at := time.Date(2031, 5, 30, 8, 0, 0, 0, time.UTC)
	const maxDepth = 5
	var chain func(id string, depth, levels int) CommentItem
	chain = func(id string, depth, levels int) CommentItem {
		it := CommentItem{
			ID: id, Author: "Commenter " + id, Body: template.HTML("<p>A comment body of ordinary length.</p>"),
			CreatedAt: at, Depth: depth,
		}
		if depth < maxDepth {
			it.ReplyURL = "/blog/hello?replyto=" + id + "#respond"
		}
		if levels > 1 {
			it.Replies = []CommentItem{chain(id+"r", depth+1, levels-1)}
		}
		return it
	}
	th := &CommentThread{Total: 190, Page: 1, Pages: 1, Newest: true}
	for i := 0; i < 50; i++ {
		th.Items = append(th.Items, chain(fmt.Sprintf("c%d", i), 1, min(maxDepth, 2+i%5)))
	}
	form := CommentFormPlaceholders
	form.Action, form.MaxBody = goldenCommentAction, 5000
	v := commentSingleView()
	v.Comments, v.CommentsOpen, v.CommentForm = th, true, &form
	return v
}

func BenchmarkRenderComments50(b *testing.B) {
	e, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	v := comments50View()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.RenderPage(io.Discard, v); err != nil {
			b.Fatal(err)
		}
	}
}

// A page with 50 top-level comments and about 190 items in all: its size and
// allocations are pinned (no timing).
func TestBudgetComments50(t *testing.T) {
	nowFunc = func() time.Time { return time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = time.Now }()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := e.RenderPage(&b, comments50View()); err != nil {
		t.Fatal(err)
	}
	budgetfile.Check(t, "render", "comments_page50_bytes", int64(b.Len()), budgetfile.Metric{Unit: "bytes", TolPct: 5})
	budgetfile.CheckAllocs(t, "render", "alloc_comments_page50", BenchmarkRenderComments50)
}
