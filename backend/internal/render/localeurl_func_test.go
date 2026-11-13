package render

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

const localeURLLayout = `L:{{localeURL . "/contact"}}|{{localeURL . "https://x.io"}}|{{localeURL . "//evil.io"}}|` +
	`{{template "partials/p" dict "Segment" .Segment}}|{{range .Blocks}}[{{template "section-note" .}}]{{end}}`

func localeURLTheme() fstest.MapFS {
	return fstest.MapFS{
		"theme.json":         mapFile(`{"name":"lu","version":"1.0.0"}`),
		"layouts/base.html":  mapFile(localeURLLayout),
		"partials/p.html":    mapFile(`{{define "partials/p"}}P:{{localeURL . "/contact"}}{{end}}`),
		"sections/note.html": mapFile(`{{define "section-note"}}S:{{localeURL . "/contact"}},{{localeURL $.Page "/a?x=1"}}{{end}}`),
	}
}

func renderLocaleURL(t *testing.T, v *PageView) string {
	t.Helper()
	e, err := New(Options{Layers: []fs.FS{localeURLTheme()}})
	if err != nil {
		t.Fatal(err)
	}
	v.Sections = []Section{{ID: "s1", Type: "note", Content: map[string]any{}}}
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestLocaleURLFunc(t *testing.T) {
	t.Parallel()
	fr := minimalView()
	fr.Segment = "fr"
	fr.Locales = []LocaleSummary{{Tag: "en"}, {Tag: "fr", Segment: "fr"}, {Tag: "es", Segment: "es"}}
	out := renderLocaleURL(t, fr)
	for _, want := range []string{
		"L:/fr/contact|https://x.io|#|",
		"P:/fr/contact",
		"S:/fr/contact,/fr/a?x=1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fr page missing %q in %q", want, out)
		}
	}

	one := renderLocaleURL(t, minimalView())
	for _, want := range []string{"L:/contact|", "P:/contact", "S:/contact,/a?x=1"} {
		if !strings.Contains(one, want) {
			t.Errorf("one-locale page missing %q in %q", want, one)
		}
	}
}

func TestLocaleURLFuncFailsOpen(t *testing.T) {
	t.Parallel()
	if got := localeURLFunc(nil, "/contact"); got != "/contact" {
		t.Errorf("unknown dot = %q", got)
	}
	if got := localeURLFunc(map[string]any{"Segment": "fr"}, "/contact"); got != "/fr/contact" {
		t.Errorf("dict = %q", got)
	}
	if got := localeURLFunc(map[string]any{"Segment": "fr"}, 5); got != "" && got != "#" {
		t.Errorf("non-string = %q", got)
	}
}
