package render

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// builderDirectoryDoc is shaped like hatamoto.dev/plugins: a native container
// with one plugin_directory component, printed without the theme section.
func builderDirectoryDoc() *builder.Doc {
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{
		ID: "plugins-wrap", T: "container", P: map[string]any{"tag": "div"},
		C: []*builder.Node{{ID: "plugin-directory", T: "component", P: map[string]any{
			"type":    "plugin_directory",
			"content": map[string]any{"eyebrow": "Plugins", "heading": "Plugin directory"},
		}}},
	}}}
}

// The directory keeps the homepage section rhythm on both paths: the root
// class carries the padding on the builder path, and is zeroed inside the
// legacy section wrapper so it is not applied twice.
func TestPluginDirectorySpacingBothPaths(t *testing.T) {
	th, err := LoadTheme(Chain{DefaultFS()})
	if err != nil {
		t.Fatal(err)
	}
	css, _, _ := buildBundle(th)
	rule := func(sel string) string {
		m := regexp.MustCompile(regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`).FindSubmatch(css)
		if m == nil {
			t.Fatalf("no CSS rule for %s", sel)
		}
		return string(m[1])
	}
	if !strings.Contains(rule(".hm-container.hm-plugin-directory"), "padding-block: clamp(4rem, 8vw, 7rem)") {
		t.Error("directory root lacks the section padding")
	}
	if !strings.Contains(rule(".hm-section--plugin_directory > .hm-container.hm-plugin-directory"), "padding-block: 0") {
		t.Error("legacy wrapper would double the padding")
	}
	if !strings.Contains(rule(".hm-section"), "padding: clamp(4rem, 8vw, 7rem) 0") {
		t.Error(".hm-section padding changed; the directory rule must match it")
	}

	out := mainInner(t, renderDoc(t, nil, builderDirectoryDoc(), false))
	if strings.Contains(out, "hm-section--plugin_directory") {
		t.Errorf("builder path unexpectedly wrapped:\n%s", out)
	}
	if !strings.Contains(out, `class="hm-container hm-plugin-directory" data-hm-directory`) {
		t.Errorf("builder root lacks the padded class and directory attribute:\n%s", out)
	}

	e, _ := New(Options{})
	v := minimalView()
	v.Sections = []Section{{ID: "s1", Type: "plugin_directory", Content: map[string]any{}}}
	var b strings.Builder
	if err := e.RenderPage(&b, v); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `class="hm-section hm-section--plugin_directory"><div class="hm-container hm-plugin-directory"`) {
		t.Error("legacy path lost the section wrapper")
	}
}

// Golden for the builder (page_doc v2) path: bare native root, no section
// wrapper or section marker, with the directory attribute and padded class.
func TestPluginDirectoryBuilderGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", "plugin_directory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sf sectionFixture
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatal(err)
	}
	doc := builderDirectoryDoc()
	doc.Nodes[0].C[0].P["content"] = sf.Content

	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.PluginDirectory, v.Doc = sf.PluginDirectory, doc
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "plugin_directory_builder", mainInner(t, buf.String()))
}

// The theme directory reuses the plugin directory root class, so its legacy
// wrapper needs the same zeroed padding; the builder path stays bare.
func TestThemeDirectorySpacingAndNoun(t *testing.T) {
	th, err := LoadTheme(Chain{DefaultFS()})
	if err != nil {
		t.Fatal(err)
	}
	css, _, _ := buildBundle(th)
	if !regexp.MustCompile(`\.hm-section--theme_directory > \.hm-container\.hm-plugin-directory\s*\{[^}]*padding-block: 0`).Match(css) {
		t.Error("legacy theme wrapper would double the padding")
	}
	e, _ := New(Options{})
	v := minimalView()
	v.Sections = []Section{{ID: "s1", Type: "theme_directory", Content: map[string]any{}}}
	v.ThemeDirectory = &ThemeDirectoryView{State: PluginDirectoryOff}
	var b strings.Builder
	if err := e.RenderPage(&b, v); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `class="hm-section hm-section--theme_directory"><div class="hm-container hm-plugin-directory hm-theme-directory" data-hm-directory`) {
		t.Error("legacy theme wrapper or root markup changed")
	}
}

// Golden for the theme directory on the builder (page_doc v2) path.
func TestThemeDirectoryBuilderGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", "theme_directory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sf sectionFixture
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatal(err)
	}
	doc := builderDirectoryDoc()
	doc.Nodes[0].C[0].P["type"] = "theme_directory"
	doc.Nodes[0].C[0].P["content"] = sf.Content

	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.ThemeDirectory, v.Doc = sf.ThemeDirectory, doc
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "theme_directory_builder", mainInner(t, buf.String()))
}
