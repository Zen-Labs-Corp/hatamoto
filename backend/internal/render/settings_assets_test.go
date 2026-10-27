package render

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestSettingsReachLayoutsAndSections(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "partials/scripts.html", `{{define "scripts"}}<i>accent={{.Settings.accent}}</i>{{end}}`)
	writeFile(t, dir, "sections/note.html", `{{define "section-note"}}<p>sec={{.Settings.accent}}</p>{{end}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}, Settings: map[string]any{"accent": "teal"}})
	v := minimalView()
	v.Sections = []Section{{ID: "n1", Type: "note", Content: map[string]any{}}}
	out := renderToString(t, e, v)
	for _, want := range []string{"<i>accent=teal</i>", "<p>sec=teal</p>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	e.SetSettings(map[string]any{"accent": "rose"})
	if out := renderToString(t, e, v); !strings.Contains(out, "accent=rose") {
		t.Errorf("SetSettings not applied:\n%s", out)
	}
}

func TestInstalledPluginAssetsFillSlots(t *testing.T) {
	SetPluginScriptGate(func(plugin string) bool { return plugin != "evil" })
	t.Cleanup(func() { SetPluginScriptGate(nil) })
	SetPluginAssets("zeta", "/p/zeta/assets/plugin.css", "/p/zeta/assets/plugin.js")
	SetPluginAssets("evil", `"><script>x</script>`, "//evil.example/x.js")
	t.Cleanup(func() { RemovePlugin("zeta"); RemovePlugin("evil") })
	e := newTestEngine(t, Options{})
	out := renderToString(t, e, minimalView())
	if !strings.Contains(out, `<link rel="stylesheet" href="/p/zeta/assets/plugin.css">`) {
		t.Errorf("stylesheet slot:\n%s", out)
	}
	if !strings.Contains(out, `<script src="/p/zeta/assets/plugin.js" defer></script>`) {
		t.Errorf("script slot:\n%s", out)
	}
	if strings.Contains(out, "evil") {
		t.Errorf("non-local asset URL leaked into the page:\n%s", out)
	}
	RemovePlugin("zeta")
	if out := renderToString(t, e, minimalView()); strings.Contains(out, "/p/zeta/") {
		t.Errorf("removed plugin's assets still linked")
	}
}

func TestPluginScriptNeedsGate(t *testing.T) {
	SetPluginAssets("zeta", "/p/zeta/assets/plugin.css", "/p/zeta/assets/plugin.js")
	t.Cleanup(func() { RemovePlugin("zeta"); SetPluginScriptGate(nil) })
	e := newTestEngine(t, Options{})
	for _, gate := range []func(string) bool{nil, func(string) bool { return false }} {
		SetPluginScriptGate(gate)
		out := renderToString(t, e, minimalView())
		if strings.Contains(out, "/p/zeta/assets/plugin.js") {
			t.Errorf("script linked without the grant:\n%s", out)
		}
		if !strings.Contains(out, "/p/zeta/assets/plugin.css") {
			t.Errorf("stylesheet must not be gated:\n%s", out)
		}
	}
	SetPluginScriptGate(func(p string) bool { return p == "zeta" })
	if out := renderToString(t, e, minimalView()); !strings.Contains(out, "/p/zeta/assets/plugin.js") {
		t.Errorf("script missing with the grant:\n%s", out)
	}
}
