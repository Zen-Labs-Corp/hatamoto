package server

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/render"
)

func TestFormViewNewFieldProps(t *testing.T) {
	off := false
	f := &form.Form{Slug: "c", Name: "C", Fields: []form.FormField{
		{ID: "b", Type: "text", Name: "b", Position: 2, Width: "half",
			Logic: &form.FieldLogic{FieldID: "a", Op: "is", Value: "x"}},
		{ID: "a", Type: "text", Name: "a", Position: 1, InEmail: &off, BlockFreeEmail: true},
		{ID: "c", Type: "text", Name: "c", Position: 3,
			Logic: &form.FieldLogic{FieldID: "gone", Op: "filled"}},
		{ID: "h", Type: "hidden", Name: "h", Position: 4, Hidden: &form.HiddenSpec{Source: "param", Param: "utm"}},
		{ID: "s", Type: "hidden", Name: "s", Position: 5, Hidden: &form.HiddenSpec{Source: "static", Value: "v"}},
		{ID: "t", Type: "heading", Position: 6, Text: "Hi"},
		{ID: "k", Type: "consent", Name: "k", Position: 7, Link: "/privacy"},
	}}
	v := formView(f)
	if len(v.Fields) != 7 || v.Fields[0].Name != "a" || v.Fields[1].Name != "b" {
		t.Fatalf("order/len: %+v", v.Fields)
	}
	if a := v.Fields[0]; a.Width != "full" {
		t.Errorf("default width = %q", a.Width)
	}
	b := v.Fields[1]
	if b.Width != "half" || b.LogicField != "a" || b.LogicOp != "is" || b.LogicValue != "x" {
		t.Errorf("b = %+v", b)
	}
	if c := v.Fields[2]; c.LogicField != "" || c.LogicOp != "" {
		t.Errorf("unknown source kept logic: %+v", c)
	}
	if h := v.Fields[3]; !h.Hidden || h.HiddenValue != "" {
		t.Errorf("param hidden leaked: %+v", h)
	}
	if s := v.Fields[4]; !s.Hidden || s.HiddenValue != "v" {
		t.Errorf("static hidden: %+v", s)
	}
	if v.Fields[5].Text != "Hi" || v.Fields[6].Link != "/privacy" {
		t.Errorf("text/link: %+v %+v", v.Fields[5], v.Fields[6])
	}
}

func TestFormViewConsentIsAlwaysRequired(t *testing.T) {
	f := &form.Form{Slug: "c", Fields: []form.FormField{
		{ID: "k", Type: form.FieldTypeConsent, Name: "k", Label: "Terms", Required: false},
		{ID: "n", Type: form.FieldTypeText, Name: "n", Label: "Name", Position: 1},
	}}
	v := formView(f)
	if !v.Fields[0].Required {
		t.Error("a consent field stored with required=false renders as optional, but the server requires it")
	}
	if v.Fields[1].Required {
		t.Error("a text field became required")
	}
}

func TestFormSectionsLoadShortcodeForms(t *testing.T) {
	t.Parallel()
	doc := &builder.Doc{V: 2, Nodes: []*builder.Node{
		{ID: "c", T: "container", C: []*builder.Node{
			{ID: "r", T: "richtext", P: map[string]any{"html": `<p>[form slug="contact"]</p>`}},
			{ID: "f", T: "form", P: map[string]any{"slug": "contact"}},
		}},
	}}
	secs := []render.Section{{ID: "s", Type: "content", Content: map[string]any{"body": []any{`[form slug='quote']`, `[form slug="contact"]`}}}}
	got := map[string]int{}
	for _, s := range formSections(secs, doc) {
		if s.Type == "contact_form" {
			got[contactFormSlug(s.Content)]++
		}
	}
	if len(got) != 2 || got["contact"] != 1 || got["quote"] != 1 {
		t.Fatalf("contact_form slugs = %v, want contact and quote once each", got)
	}
}

func TestShortcodeFormRendersThroughBuiltForms(t *testing.T) {
	t.Parallel()
	srv, _ := newTypesServer(t, &form.Form{Name: "Contact", Fields: []form.FormField{
		{ID: "n", Type: form.FieldTypeText, Name: "name", Label: "Your name"},
	}})
	doc := &builder.Doc{V: 2, Nodes: []*builder.Node{
		{ID: "r", T: "richtext", P: map[string]any{"html": `<p>Before</p><p>[form slug="t"]</p>`}},
	}}
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := &render.PageView{BaseURL: "https://x.io", Path: "/", Doc: doc}
	v.Forms = srv.formsForSections(context.Background(), formSections(nil, doc))
	var buf bytes.Buffer
	if err := engine.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Your name") || strings.Contains(out, "[form") {
		t.Errorf("shortcode not expanded to the form: %s", out)
	}

	// A paused form is not loaded, so the shortcode renders nothing.
	srv2, repo := newTypesServer(t, &form.Form{Name: "Contact"})
	repo.forms["t"].Status = form.StatusInactive
	v.Forms = srv2.formsForSections(context.Background(), formSections(nil, doc))
	buf.Reset()
	if err := engine.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<form") || strings.Contains(buf.String(), "[form") {
		t.Errorf("paused form leaked: %s", buf.String())
	}
}
