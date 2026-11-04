package render

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func previewChangesView() *PageView {
	v := minimalView()
	v.EntityID = "ent-1"
	v.Doc = &builder.Doc{V: 2, Nodes: []*builder.Node{
		container("keep", nil),
		container("fresh", nil),
	}}
	return v
}

func TestPreviewChangesMarks(t *testing.T) {
	e := newTestEngine(t, Options{})
	plain := renderToString(t, e, previewChangesView())

	// Changes without Preview, and Preview without Changes, render as before.
	v := previewChangesView()
	v.Changes = &PageChanges{Nodes: map[string]string{"fresh": "new"}, Label: "#42"}
	if got := renderToString(t, e, v); got != plain {
		t.Error("Changes without Preview changed the output")
	}
	v = previewChangesView()
	v.Preview = true
	prev := renderToString(t, e, v)
	if strings.Contains(prev, "data-hm-change") || strings.Contains(prev, "hm-preview-banner") {
		t.Error("Preview without Changes emitted marks")
	}

	v = previewChangesView()
	v.Preview = true
	v.Changes = &PageChanges{Page: "changed", Nodes: map[string]string{"fresh": "new", "keep": "changed"}, Label: "#42"}
	out := renderToString(t, e, v)
	for _, want := range []string{
		`data-hm-change="new"`,
		`data-hm-change="changed"`,
		`<strong>Preview of changeset #42, not live.</strong> Nobody else can see this.`,
		`<style data-hm-preview-style>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(out, `hm-n-fresh"`) || !strings.Contains(out, `data-hm-change="new"`) {
		t.Error("node mark missing")
	}
	if strings.Contains(out, "<script") && !strings.Contains(prev, "<script") {
		t.Error("marks added a script tag")
	}
	if strings.Count(out, "<script") != strings.Count(prev, "<script") {
		t.Error("marks changed the script tags")
	}
}

func TestPreviewChangesPageAndSectionMarks(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.EntityID = "ent-1"
	v.Preview = true
	v.Sections = []Section{{ID: "hero", Type: "client_logos", Title: "Clients", Content: map[string]any{"headline": "Our clients"}}}
	v.Changes = &PageChanges{Page: "new", Nodes: map[string]string{"hero": "changed"}, Label: "#7"}
	out := renderToString(t, e, v)
	if !strings.Contains(out, `data-hm-change="new" data-hm-entity="ent-1"`) {
		t.Errorf("main not marked:\n%s", out)
	}
	if !strings.Contains(out, `data-hm-change="changed" data-hm-section="hero"`) {
		t.Errorf("section not marked:\n%s", out)
	}
}

func TestPreviewChangesVisibleMarks(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := previewChangesView()
	v.Preview = true
	v.Changes = &PageChanges{Page: "changed", Nodes: map[string]string{"fresh": "new", "keep": "changed"}, Label: "#9"}
	out := renderToString(t, e, v)
	for _, want := range []string{
		`hm-n-fresh" data-hm-change="new"`,
		`hm-n-keep" data-hm-change="changed"`,
		`data-hm-change="changed" data-hm-entity="ent-1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, want := range []string{
		`[data-hm-change=new]{outline:2px solid #2f6b4f;outline-offset:-2px}`,
		`[data-hm-change=changed]{outline:2px solid #b7791f;outline-offset:-2px}`,
		`main[data-hm-change=changed] h1,main[data-hm-change=changed]>*{outline:2px solid #b7791f;outline-offset:-2px}`,
		`background:#8a5a14`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("style missing %q", want)
		}
	}
	if strings.Contains(out, "outline-offset:2px") {
		t.Error("outward outline offset would be clipped by the frame")
	}

	v = previewChangesView()
	v.Preview = true
	v.Changes = &PageChanges{Page: "new", Label: "#9"}
	if out := renderToString(t, e, v); !strings.Contains(out, `data-hm-change="new" data-hm-entity="ent-1"`) {
		t.Error("created page main not marked new")
	}

	v = previewChangesView()
	v.Preview = true
	if out := renderToString(t, e, v); strings.Contains(out, "data-hm-change") || strings.Contains(out, "hm-preview-style") {
		t.Error("marks emitted without Changes")
	}
}
