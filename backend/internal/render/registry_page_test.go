package render

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func registryPluginView() *PageView {
	v := minimalView()
	v.Kind = KindRegistry
	v.Page = PageMeta{Title: "Example plugin"}
	v.Registry = &RegistryPageView{Type: "plugin", ListName: "Plugins", ListURL: "/plugins", Plugin: &PluginCard{
		Name: "example", Title: "Example", Version: "v0.2.0", Tier: "community", TierLabel: "Community",
		Author: "ada", Description: "Does a thing.", Source: "https://example.test/src", PermCount: 2,
		PermLines: []PermLine{
			{Name: "settings", Text: "Read and save its own settings"},
			{Name: "site.scripts", Text: "Add scripts to every public page", Warn: true},
		},
		About:    []string{"First paragraph.", "Second <script>alert(1)</script> paragraph."},
		Features: []string{"Fast", "<b>Bold</b>"},
	}}
	return v
}

func renderRegistry(t *testing.T, e *Engine, v *PageView) string {
	t.Helper()
	var b strings.Builder
	if err := e.RenderPage(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRegistryPagePlugin(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := renderRegistry(t, e, registryPluginView())
	for _, want := range []string{
		`data-hm-directory`, `<h1 class="hm-registry__title">Example</h1>`, `v0.2.0`, `Community`,
		`href="https://example.test/src"`, `Does a thing.`, `<h2 class="hm-registry__h">About</h2>`,
		`<p>First paragraph.</p>`, `<h2 class="hm-registry__h">Features</h2>`, `<li>Fast</li>`,
		`Permissions (2)`, `Read and save its own settings`,
		`<strong class="hm-registry__warn">Warning:</strong> Add scripts to every public page`,
		`<dt>Author</dt><dd>ada</dd>`, `href="/plugins"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, mainInner(t, out))
		}
	}
	if strings.Contains(out, "<script>alert") || strings.Contains(out, "<b>Bold") {
		t.Errorf("untrusted about/features were not escaped:\n%s", mainInner(t, out))
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("escaped about text missing")
	}

	v := registryPluginView()
	p := v.Registry.Plugin
	p.Author, p.About, p.Features, p.PermLines = "", nil, nil, nil
	out = renderRegistry(t, e, v)
	for _, absent := range []string{"<dt>Author</dt>", ">About</h2>", ">Features</h2>", "Permissions ("} {
		if strings.Contains(out, absent) {
			t.Errorf("%q should be omitted when empty", absent)
		}
	}
}

func TestRegistryPageTheme(t *testing.T) {
	e, _ := New(Options{})
	v := minimalView()
	v.Kind = KindRegistry
	v.Registry = &RegistryPageView{Type: "theme", ListName: "Themes", ListURL: "/themes", Theme: &ThemeCard{
		Name: "boreal", Version: "v1.0.0", TierLabel: "Community", Screenshot: "https://example.test/s.png",
		Source: "https://example.test/src", About: []string{"About text."},
	}}
	out := renderRegistry(t, e, v)
	shot, about := strings.Index(out, `class="hm-registry__shot"`), strings.Index(out, ">About</h2>")
	if shot < 0 || about < 0 || shot > about {
		t.Errorf("screenshot must come before About (shot=%d about=%d)", shot, about)
	}
	if strings.Contains(out, "Permissions") {
		t.Error("theme page must not show a Permissions box")
	}
	if !strings.Contains(out, `<h1 class="hm-registry__title">boreal</h1>`) {
		t.Error("title should fall back to the name")
	}
}

// A custom theme that ships no layouts/registry.html falls back to the
// embedded default layout through the chain, and passes the smoke render.
func TestRegistryPageCustomThemeFallsBack(t *testing.T) {
	custom := fstest.MapFS{"theme.json": mapFile(`{"name":"custom","version":"1.0.0"}`)}
	e, err := New(Options{Layers: []fs.FS{custom}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CheckTheme(custom); err != nil {
		t.Fatalf("smoke render of a custom theme: %v", err)
	}
	out := renderRegistry(t, e, registryPluginView())
	if !strings.Contains(out, `<h1 class="hm-registry__title">Example</h1>`) {
		t.Errorf("custom theme did not fall back to the default registry layout:\n%s", out)
	}
}

// A custom theme that ships layouts/page.html but no registry.html must still
// get a registry layout: its page.html cannot show .Registry.
func TestRegistryPageIgnoresCustomPageLayout(t *testing.T) {
	custom := fstest.MapFS{
		"theme.json":        mapFile(`{"name":"custom","version":"1.0.0"}`),
		"layouts/page.html": mapFile(`{{define "content"}}CUSTOMPAGE{{end}}`),
	}
	e, err := New(Options{Layers: []fs.FS{custom}})
	if err != nil {
		t.Fatal(err)
	}
	out := renderRegistry(t, e, registryPluginView())
	if !strings.Contains(out, `<h1 class="hm-registry__title">Example</h1>`) || strings.Contains(out, "CUSTOMPAGE") {
		t.Errorf("custom page.html hijacked the registry layout:\n%s", out)
	}
}
