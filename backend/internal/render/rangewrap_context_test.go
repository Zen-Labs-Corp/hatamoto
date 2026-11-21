package render

import (
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// A theme may call a plugin partial in an attribute or a script. The
// {{if $hm-budget.Run}} wrap of range templates must not change what the
// escaper makes of the page.
func TestPluginRangeWrapKeepsThemeContexts(t *testing.T) { //nolint:paralleltest // process-wide plugin script gate and breaker
	b, _ := swapRangeBreaker(t)
	SetPluginScriptGate(func(p string) bool { return p == "acme-scr" })
	t.Cleanup(func() { SetPluginScriptGate(nil) })

	reg := newPluginRegistry()
	mustRegister(t, reg, "acme-ctx", map[string]string{
		"path": `/t/{{range $i := 3}}{{$i}}{{end}}`,
		"word": `{{range $i := 2}}w{{$i}}{{end}}`,
	}, nil)
	mustRegister(t, reg, "acme-scr", map[string]string{
		"block": `<script>var a=1;{{range $i := 2}}a+={{$i}};{{end}}</script>`,
		"open":  `<script>var b=[{{range $i := 2}}{{$i}},{{end}}`,
	}, nil)

	themeDir := t.TempDir()
	writeFile(t, themeDir, "sections/note.html", `{{define "section-note"}}`+
		`<a href="{{template "plugin/acme_ctx/path" .}}?q={{.Content.q}}">l</a>`+
		`<script>var u = "{{template "plugin/acme_ctx/word" .}}";</script>`+
		`{{template "plugin/acme_scr/block" .}}`+
		`{{template "plugin/acme_scr/open" .}}0];</script>`+
		`{{end}}`)
	writeFile(t, themeDir, "schemas/note.json", `{"x-hm-section":{"title":"Note"},"type":"object","properties":{}}`)
	e, err := newEngine(Options{Layers: []fs.FS{os.DirFS(themeDir)}, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}, reg)
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Sections = []Section{{ID: "n1", Type: "note", Content: map[string]any{"q": "a b"}}}
	out := renderToString(t, e, v)
	for _, want := range []string{
		`<a href="/t/012?q=a%20b">l</a>`,
		`var u = "w0w1";`,
		`var a=1;a+= 0 ;a+= 1 ;</script>`,
		`var b=[ 0 , 1 ,0];</script>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, mainInner(t, out))
		}
	}
	if strings.Contains(out, "ZgotmplZ") || strings.Contains(out, SectionFailedMarker) {
		t.Errorf("page did not escape cleanly:\n%s", mainInner(t, out))
	}

	// A paused template that is not wrapped (it opens a <script> another
	// template closes) still stops its loops; the wrapped ones render nothing.
	trip(t, b, "acme-scr", "plugin/acme_scr/open")
	trip(t, b, "acme-ctx", "plugin/acme_ctx/path")
	out = renderToString(t, e, v)
	if !strings.Contains(out, `<script>var b=[0];</script>`) && !strings.Contains(out, `var b=[0];</script>`) {
		t.Errorf("paused unwrapped template still ran its range:\n%s", mainInner(t, out))
	}
	if !strings.Contains(out, `<a href="?q=a%20b">`) {
		t.Errorf("paused wrapped template still rendered:\n%s", mainInner(t, out))
	}
}
