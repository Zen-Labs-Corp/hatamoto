package server

import (
	"context"
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

func schemaFixture(slug, settings string) *entity.EntitySchema {
	s := &entity.EntitySchema{Slug: slug, Name: slug}
	if settings != "" {
		s.Settings = json.RawMessage(settings)
	}
	return s
}

func TestBareSlugCanonical(t *testing.T) {
	t.Parallel()
	snap, err := entity.BuildURLSnapshot([]*entity.EntitySchema{
		schemaFixture("page", `{"section_pages":true}`),
		schemaFixture("doc", `{"url_pattern":"/docs/{slug}"}`),
		schemaFixture("post", ""),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		schema, slug, want string
	}{
		{"post", "hello", "/blog/hello"},
		{"blog", "hello", "/blog/hello"}, // alias of the post family
		{"doc", "intro", "/docs/intro"},
		{"page", "about", ""}, // already lives at the bare slug: no hint
		{"page", "docs", ""},  // section landing page already lives at /docs
		{"nope", "x", ""},
	} {
		ent := &entity.Entity{Slug: tt.slug, Schema: schemaFixture(tt.schema, "")}
		got, ok := bareSlugCanonical(ent, snap)
		if got != tt.want || ok != (tt.want != "") {
			t.Errorf("%s/%s = %q,%v want %q", tt.schema, tt.slug, got, ok, tt.want)
		}
	}
}

// publicPaths answers a whole page of entities from one snapshot: no query
// without nested URLs (a nil repository would panic), and nothing for a schema
// the registry does not route.
func TestPublicPathsBatch(t *testing.T) {
	t.Parallel()
	snap, err := entity.BuildURLSnapshot([]*entity.EntitySchema{
		schemaFixture("page", ""),
		schemaFixture("doc", `{"url_pattern":"/docs/{slug}"}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := func(sc *entity.EntitySchema, slugs ...string) []*entity.Entity {
		var out []*entity.Entity
		for _, s := range slugs {
			out = append(out, &entity.Entity{Slug: s, Schema: sc})
		}
		return out
	}
	doc := schemaFixture("doc", "")
	got, ok := publicPaths(context.Background(), snap, nil, doc, rows(doc, "a", "b"))
	if !ok || len(got) != 2 || got[0] != "/docs/a" || got[1] != "/docs/b" {
		t.Errorf("doc paths = %v, %v", got, ok)
	}
	if got, ok := publicPaths(context.Background(), snap, nil, doc, nil); !ok || got != nil {
		t.Errorf("routable probe = %v, %v", got, ok)
	}
	nope := schemaFixture("nope", "")
	if got, ok := publicPaths(context.Background(), snap, nil, nope, rows(nope, "x")); ok || got != nil {
		t.Errorf("unrouted schema = %v, %v, want none", got, ok)
	}
}
