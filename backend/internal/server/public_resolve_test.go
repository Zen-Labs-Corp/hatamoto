package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
)

type fakeResolveRepo struct {
	rows  []*entity.Entity
	paths map[uuid.UUID][]entity.BreadcrumbPathItem
}

func (f *fakeResolveRepo) GetPublishedBySlugAndSchemas(_ context.Context, slug string, schemas []string) (*entity.Entity, error) {
	for _, e := range f.rows {
		if e.Slug != slug {
			continue
		}
		for _, s := range schemas {
			if e.Schema.Slug == s {
				return e, nil
			}
		}
	}
	return nil, entity.ErrEntityNotFound
}

func (f *fakeResolveRepo) GetEntityPath(_ context.Context, id uuid.UUID) ([]entity.BreadcrumbPathItem, error) {
	return f.paths[id], nil
}

func fixtureEntity(schema, slug string, attrs string) *entity.Entity {
	e := &entity.Entity{
		ID: uuid.New(), Slug: slug, Name: slug,
		Schema: &entity.EntitySchema{Slug: schema},
	}
	if attrs != "" {
		e.Attributes = json.RawMessage(attrs)
	}
	return e
}

func fixtureSnapshot(t *testing.T, defs map[string]string) *entity.URLSnapshot {
	t.Helper()
	var schemas []*entity.EntitySchema
	for slug, settings := range defs {
		s := &entity.EntitySchema{ID: uuid.New(), Name: slug, Slug: slug}
		if settings != "" {
			s.Settings = json.RawMessage(settings)
		}
		schemas = append(schemas, s)
	}
	snap, err := entity.BuildURLSnapshot(schemas, nil)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestResolveEntity(t *testing.T) {
	t.Parallel()
	snap := fixtureSnapshot(t, map[string]string{
		"page":   `{"section_pages":true}`,
		"post":   ``,
		"doc":    `{"url_pattern":"/docs/{slug}","overrides":[{"attribute":"format","equals":"report","url_pattern":"/reports/{slug}"}]}`,
		"person": `{"url_pattern":"/people/{parent}/{slug}"}`,
	})
	docsIndex := fixtureEntity("page", "docs", "")
	parent := fixtureEntity("page", "team", "")
	child := fixtureEntity("person", "ada", "")
	child.ParentID = &parent.ID
	repo := &fakeResolveRepo{
		rows: []*entity.Entity{
			fixtureEntity("page", "about", ""),
			fixtureEntity("page", "blog", ""), // section page for /blog
			docsIndex,
			fixtureEntity("post", "hello", ""),
			fixtureEntity("doc", "guide", `{"format":"memo"}`),
			fixtureEntity("doc", "annual", `{"format":"report"}`),
			parent, child,
		},
		paths: map[uuid.UUID][]entity.BreadcrumbPathItem{
			child.ID: {{ID: parent.ID, Slug: "team"}, {ID: child.ID, Slug: "ada"}},
		},
	}

	type want struct{ slug, url string }
	tests := []struct {
		name string
		path string
		want *want
	}{
		{"page at root", "/about", &want{"about", "/about"}},
		{"page trailing slash", "/about/", &want{"about", "/about"}},
		{"neutral post", "/blog/hello", &want{"hello", "/blog/hello"}},
		{"post is not at the bare slug", "/hello", nil},
		{"custom section", "/docs/guide", &want{"guide", "/docs/guide"}},
		{"override entity at override url", "/reports/annual", &want{"annual", "/reports/annual"}},
		{"override entity not at base url", "/docs/annual", nil},
		{"non-override entity not at override url", "/reports/guide", nil},
		{"wrong section", "/docs/hello", nil},
		{"section page at root", "/docs", &want{"docs", "/docs"}},
		{"section page for archive prefix", "/blog", &want{"blog", "/blog"}},
		{"parent in url", "/people/team/ada", &want{"ada", "/people/team/ada"}},
		{"parent omitted", "/people/ada", &want{"ada", "/people/ada"}},
		{"wrong parent", "/people/other/ada", nil},
		{"unknown", "/nothing/here/at/all", nil},
		{"unknown prefix is not routed", "/xx/about", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveEntity(context.Background(), repo, snap, tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("expected no match, got %s -> %s", got.Slug, got.URL)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a match")
			}
			if got.Slug != tc.want.slug || got.URL != tc.want.url {
				t.Fatalf("got slug=%s url=%s", got.Slug, got.URL)
			}
		})
	}
}

// With no settings at all the resolver reproduces the core defaults:
// /{slug} for pages and /blog/{slug} (plus aliases) for posts.
func TestResolveEntity_Defaults(t *testing.T) {
	t.Parallel()
	snap := fixtureSnapshot(t, map[string]string{"page": "", "post": ""})
	repo := &fakeResolveRepo{rows: []*entity.Entity{
		fixtureEntity("page", "about", ""),
		fixtureEntity("post", "hello", ""),
	}}
	for path, wantURL := range map[string]string{"/about": "/about", "/blog/hello": "/blog/hello"} {
		got, err := resolveEntity(context.Background(), repo, snap, path)
		if err != nil || got == nil || got.URL != wantURL {
			t.Errorf("%s: %+v %v", path, got, err)
		}
	}
	if got, _ := resolveEntity(context.Background(), repo, snap, "/hello"); got != nil {
		t.Error("bare post slug must not resolve")
	}
}

func TestResolveEntity_Archive(t *testing.T) {
	t.Parallel()
	snap := fixtureSnapshot(t, map[string]string{
		"page":   `{"section_pages":true}`,
		"post":   ``,
		"person": `{"url_pattern":"/team/{slug}","archive_path":"/team"}`,
	})
	repo := &fakeResolveRepo{rows: []*entity.Entity{
		fixtureEntity("page", "blog", ""), // section page wins over the archive
		fixtureEntity("person", "ada", ""),
	}}

	got, err := resolveEntity(context.Background(), repo, snap, "/team")
	if err != nil || got == nil || got.Kind != resolveKindArchive || got.Archive.SchemaSlug != "person" ||
		got.Archive.Path != "/team" || got.URL != "/team" {
		t.Fatalf("/team: %+v %v", got, err)
	}
	if got, _ := resolveEntity(context.Background(), repo, snap, "/team/"); got == nil || got.Kind != resolveKindArchive {
		t.Fatalf("trailing slash: %+v", got)
	}
	if got, _ := resolveEntity(context.Background(), repo, snap, "/team/ada"); got == nil || got.Kind != resolveKindEntity {
		t.Fatalf("entity under archive: %+v", got)
	}
	if got, _ := resolveEntity(context.Background(), repo, snap, "/blog"); got == nil || got.Kind != resolveKindEntity {
		t.Fatalf("section page must win: %+v", got)
	}
	// Without a section page the default post archive resolves.
	snap2 := fixtureSnapshot(t, map[string]string{"post": ""})
	got, _ = resolveEntity(context.Background(), &fakeResolveRepo{}, snap2, "/blog")
	if got == nil || got.Kind != resolveKindArchive || got.Archive.SchemaSlug != "post" {
		t.Fatalf("/blog: %+v", got)
	}
	if got, _ := resolveEntity(context.Background(), repo, snap, "/nope"); got != nil {
		t.Fatalf("unknown: %+v", got)
	}
}

func (f *fakeResolveRepo) GetPublishedByPath(_ context.Context, segments []string, schemas []string) (*entity.Entity, error) {
	return f.GetPublishedBySlugAndSchemas(context.Background(), segments[len(segments)-1], schemas)
}
