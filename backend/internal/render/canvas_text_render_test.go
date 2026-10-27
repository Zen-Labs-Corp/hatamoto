package render

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCanvasStampsHTMLFieldInEngineRender(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	render := func(canvas bool) string {
		v := minimalView()
		v.Canvas = canvas
		v.Sections = []Section{{ID: "s", Type: "html", Content: map[string]any{
			"body": `<h2>Title</h2><p>Hi <b>there</b></p><pre>keep</pre>`,
		}}}
		var buf bytes.Buffer
		if err := e.RenderPage(&buf, v); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	on, off := render(true), render(false)
	for _, want := range []string{
		`<h2 data-hm-text="body#0">Title</h2>`,
		`<span data-hm-text="body#1">Hi </span>`,
		`<b data-hm-text="body#2">there</b>`,
	} {
		if !strings.Contains(on, want) {
			t.Errorf("canvas render missing %s", want)
		}
	}
	if strings.Contains(on, "keep</pre>") && strings.Contains(on, `data-hm-text="body#3"`) {
		t.Error("pre text was stamped")
	}
	if strings.Contains(off, "data-hm-text") {
		t.Error("non-canvas render carries data-hm-text")
	}
}

func TestCanvasStampsNestedPaths(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"sections/rep.html": mapFile(`{{define "section-rep"}}{{end}}`),
		"schemas/rep.json": mapFile(`{"type":"object","properties":{
			"items":{"type":"array","items":{"type":"object","properties":{
				"b":{"type":"string","x-hm-field":"html"},
				"r":{"type":"string","x-hm-field":"richtext"}}}}}}`),
	}
	th, err := LoadTheme(Chain{fsys})
	if err != nil {
		t.Fatal(err)
	}
	content := map[string]any{"items": []any{
		"not-an-object", // dropped; must not shift later paths
		map[string]any{"b": "<p>one</p>", "r": "<p>rich</p>"},
		map[string]any{"b": "<p>two</p>"},
	}}
	got := prepareSections(th, []Section{{Type: "rep", Content: content}}, true)
	items := got[0].Content["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	first := string(items[0].(map[string]any)["b"].(template.HTML))
	second := string(items[1].(map[string]any)["b"].(template.HTML))
	if first != `<p data-hm-text="items.1.b#0">one</p>` {
		t.Errorf("first = %s", first)
	}
	if second != `<p data-hm-text="items.2.b#0">two</p>` {
		t.Errorf("second = %s", second)
	}
	if rich := string(items[0].(map[string]any)["r"].(template.HTML)); strings.Contains(rich, "data-hm-text") {
		t.Errorf("richtext stamped: %s", rich)
	}
	plain := prepareSections(th, []Section{{Type: "rep", Content: content}}, false)
	if strings.Contains(string(plain[0].Content["items"].([]any)[0].(map[string]any)["b"].(template.HTML)), "data-hm-text") {
		t.Error("non-canvas prepare stamped")
	}
}

func TestCanvasForgedStampIsStripped(t *testing.T) {
	t.Parallel()
	th := pipelineTheme(t)
	got := prepareSections(th, []Section{{Type: "html", Content: map[string]any{
		"body": `<p data-hm-text="body#9">x</p>`,
	}}}, true)
	body := string(got[0].Content["body"].(template.HTML))
	if body != `<p data-hm-text="body#0">x</p>` {
		t.Errorf("forged marker not replaced: %s", body)
	}
}
