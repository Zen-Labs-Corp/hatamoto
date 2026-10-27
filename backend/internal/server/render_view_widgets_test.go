package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/form"
)

type slugFormRepo struct {
	form.Repository
	forms map[string]*form.Form
}

func (r slugFormRepo) GetFormBySlug(_ context.Context, slug string) (*form.Form, error) {
	if f, ok := r.forms[slug]; ok {
		return f, nil
	}
	return nil, form.ErrFormNotFound
}

func nativeFormDoc(t *testing.T) *builder.Doc {
	t.Helper()
	d, err := builder.Parse([]byte(`{"v":2,"nodes":[{"id":"r","t":"container","c":[
		{"id":"f1","t":"form","p":{"slug":"contact"}},
		{"id":"f2","t":"form","p":{"slug":"contact"}},
		{"id":"f3","t":"form","p":{"slug":"gone"}},
		{"id":"f4","t":"form"},
		{"id":"c1","t":"component","p":{"type":"contact_form","content":{"form_slug":"other"}}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A native form node loads its definition like a contact_form section does; the
// same slug twice is one lookup, an unknown or inactive form is left out.
func TestFormsForNativeFormWidgets(t *testing.T) {
	repo := slugFormRepo{forms: map[string]*form.Form{
		"contact": {Slug: "contact", Name: "Contact", Status: form.StatusActive},
		"other":   {Slug: "other", Name: "Other", Status: form.StatusActive},
		"gone":    {Slug: "gone", Name: "Gone", Status: form.StatusInactive},
	}}
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), formService: form.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))}
	doc := nativeFormDoc(t)
	got := s.formsForSections(context.Background(), formSections(nil, doc))
	if len(got) != 2 || got["contact"].Slug != "contact" || got["other"].Slug != "other" {
		t.Errorf("forms = %+v", got)
	}
	if _, ok := got["gone"]; ok {
		t.Error("an inactive form was loaded")
	}
	// viewSections (entity links, ...) never sees the native forms.
	if n := len(viewSections(nil, doc)); n != 1 {
		t.Errorf("viewSections = %d entries, want the one component", n)
	}
}
