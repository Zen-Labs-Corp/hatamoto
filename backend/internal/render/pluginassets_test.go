package render

import (
	"strings"
	"testing"
)

// setupPluginScript registers a granted plugin stylesheet and script. The
// registry and the gate are process-global, so callers must not run parallel.
func setupPluginScript(t *testing.T) {
	t.Helper()
	SetPluginAssets("atk", "/p/atk/assets/plugin.css", "/p/atk/assets/plugin.js")
	SetPluginScriptGate(func(string) bool { return true })
	t.Cleanup(func() {
		SetPluginAssets("atk", "", "")
		SetPluginScriptGate(nil)
	})
}

// scriptSrcFilter is a plugin head filter adding an external script tag and
// a JSON-LD block.
func scriptSrcFilter() HeadFilter {
	return hostileFilter(
		HeadTag{Kind: "script-src", Attrs: map[string]string{"src": "https://cdn.example.com/atk.js"}},
		HeadTag{Kind: "jsonld", JSONLD: []byte(`{"@type":"Thing","name":"x"}`)},
	)
}

func editorView(preview, canvas bool) *PageView {
	v := minimalView()
	v.Doc = imageDoc(map[string]any{"lightbox": true}) // ships the lightbox island
	v.Preview, v.Canvas = preview, canvas
	return v
}

// Editor frames run no page scripts: no plugin script, island, hm.js or head
// filter script tag, while the plugin stylesheet and JSON-LD data stay.
func TestEditorViewsEmitNoPageScripts(t *testing.T) { //nolint:paralleltest // sets the global plugin assets and script gate
	setupPluginScript(t)
	e := newTestEngine(t, Options{HeadFilter: scriptSrcFilter()})

	for name, c := range map[string]struct{ preview, canvas bool }{
		"preview":        {true, false},
		"canvas":         {false, true},
		"preview+canvas": {true, true},
	} {
		v := editorView(c.preview, c.canvas)
		out := renderToString(t, e, v)
		for _, bad := range []string{"/p/atk/assets/plugin.js", "hm.js", "builder/lightbox.", "cdn.example.com/atk.js"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s: editor render contains %q", name, bad)
			}
		}
		for _, want := range []string{`href="/p/atk/assets/plugin.css"`, `application/ld+json`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: editor render lacks %q", name, want)
			}
		}
		if v.Preview != c.preview || v.Canvas != c.canvas {
			t.Errorf("%s: executeWith mutated the view", name)
		}
	}
}

// The public render is untouched: plugin script, hm.js, island and head tag stay.
func TestPublicViewKeepsPageScripts(t *testing.T) { //nolint:paralleltest // sets the global plugin assets and script gate
	setupPluginScript(t)
	e := newTestEngine(t, Options{HeadFilter: scriptSrcFilter()})
	out := renderToString(t, e, editorView(false, false))
	for _, want := range []string{`<script src="/p/atk/assets/plugin.js" defer>`, `hm.js`, `builder/lightbox.`, `cdn.example.com/atk.js`, `href="/p/atk/assets/plugin.css"`} {
		if !strings.Contains(out, want) {
			t.Errorf("public render lacks %q", want)
		}
	}
}
