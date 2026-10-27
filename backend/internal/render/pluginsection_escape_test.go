package render

import (
	"strings"
	"testing"
)

// A plugin section the escaper refuses (a tag left open) used to poison the
// shared template set: the page then panicked inside html/template. It is now
// skipped when the engine compiles, and the other sections still render.
func TestPluginSectionTheEscaperRefusesIsSkipped(t *testing.T) {
	reg := newPluginRegistry()
	reg.registerSection("p", "open", `{{define "section-open"}}<b>hi</b><img src=x {{end}}`)
	reg.registerSection("p", "fine", `{{define "section-fine"}}<a href="{{.Content.u}}">x</a><div title="{{.Content.t}}">ok-marker</div>{{end}}`)
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Sections = append(v.Sections,
		Section{ID: "o", Type: "open", Content: map[string]any{}},
		Section{ID: "f", Type: "fine", Content: map[string]any{"u": "/x", "t": "t"}})
	var sb strings.Builder
	if err := e.RenderPage(&sb, v); err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !strings.Contains(sb.String(), "ok-marker") {
		t.Error("the well-formed section did not render")
	}
}

func TestSafeExecuteTurnsAPanicIntoAnError(t *testing.T) {
	if err := safeExecute(&strings.Builder{}, nil, "base", nil); err == nil {
		t.Fatal("want an error")
	}
}
