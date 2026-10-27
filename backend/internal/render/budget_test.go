package render

import (
	"os"
	"path/filepath"
	"testing"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/internal/builder"
)

// The all-widgets page without tabs/video ships no script at all (exact, not a tolerance).
func TestBudgetPublicBuilderJS(t *testing.T) {
	d := loadWidgetDoc(t, "all")
	noIslands := &builder.Doc{V: 2, Nodes: withoutKinds(d.Nodes, "tabs", "video")}
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
