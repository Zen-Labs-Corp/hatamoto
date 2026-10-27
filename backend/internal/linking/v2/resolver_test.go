package v2

import (
	"errors"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/urlpattern"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		schemaSlug string
		entitySlug string
		parentSlug string
		wantURL    string
		wantErrIs  error
	}{
		{name: "post", schemaSlug: "post", entitySlug: "shopify-seo-guide", wantURL: "/blog/shopify-seo-guide"},
		{name: "blog alias", schemaSlug: "blog", entitySlug: "shopify-seo-guide", wantURL: "/blog/shopify-seo-guide"},
		{name: "page", schemaSlug: "page", entitySlug: "about", wantURL: "/about"},

		// Excluded schemas — never linkable.
		{name: "faq excluded", schemaSlug: "faq", entitySlug: "x", wantErrIs: ErrSchemaNotLinkable},
		{name: "location excluded", schemaSlug: "location", entitySlug: "x", wantErrIs: ErrSchemaNotLinkable},
		{name: "organization excluded", schemaSlug: "organization", entitySlug: "x", wantErrIs: ErrSchemaNotLinkable},

		// Error paths.
		{name: "empty slug", schemaSlug: "post", entitySlug: "", wantErrIs: ErrEmptyEntitySlug},
		{name: "unknown schema", schemaSlug: "homepage", entitySlug: "x", wantErrIs: ErrUnknownSchema},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(entity.DefaultURLSnapshot(), tt.schemaSlug, tt.entitySlug, tt.parentSlug)
			if tt.wantErrIs != nil {
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("Resolve() err = %v, want errors.Is %v", err, tt.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() unexpected err = %v", err)
			}
			if got != tt.wantURL {
				t.Errorf("Resolve() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

func TestResolve_SchemaPatterns(t *testing.T) {
	t.Parallel()
	settings := func(slug, s string) *entity.EntitySchema {
		return &entity.EntitySchema{Slug: slug, Name: slug, Settings: []byte(s)}
	}
	snap, err := entity.BuildURLSnapshot([]*entity.EntitySchema{
		settings("page", ""),
		settings("doc", `{"url_pattern":"/docs/{slug}","overrides":[{"attribute":"kind","equals":"report","url_pattern":"/reports/{slug}"}]}`),
		settings("person", `{"url_pattern":"/people/{parent}/{slug}"}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		schema    string
		parent    string
		attrs     map[string]string
		want      string
		wantErrIs error
	}{
		{name: "pattern", schema: "doc", want: "/docs/x"},
		{name: "override", schema: "doc", attrs: map[string]string{"kind": "report"}, want: "/reports/x"},
		{name: "parent pattern", schema: "person", parent: "team", want: "/people/team/x"},
		{name: "parent omitted", schema: "person", want: "/people/x"},
		{name: "unknown schema", schema: "nope", wantErrIs: ErrUnknownSchema},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveWithAttrs(snap, tt.schema, urlpattern.Vars{Slug: "x", Parent: tt.parent}, tt.attrs)
			if tt.wantErrIs != nil {
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("err = %v, want %v", err, tt.wantErrIs)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q err=%v, want %q", got, err, tt.want)
			}
		})
	}
}
