//go:build integration

package server

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// A person is public only with attributes.public = true: the hidden one is
// absent from every public read (single, sitemap, listing, search, theme query).
func TestPersonVisibility_Integration(t *testing.T) {
	r := newNestedRig(t)
	t.Cleanup(func() {
		r.exec(`DELETE FROM hatamoto_entities WHERE slug IN ('pv-shown', 'pv-hidden')`)
	})
	for slug, public := range map[string]string{"pv-shown": "true", "pv-hidden": "false"} {
		r.exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
			SELECT id, 'Zebrafish ' || $1::text, $1, 'published', jsonb_build_object('public', $2::boolean, 'short_bio', 'zebrafish'), now()
			FROM hatamoto_entity_schemas WHERE slug = 'person'`, slug, public)
	}
	repo := entity.NewPostgresPublicRepository(r.db)
	person := []string{"person"}

	for slug, want := range map[string]bool{"pv-shown": true, "pv-hidden": false} {
		e, err := repo.GetPublishedBySlugAndSchemas(r.ctx, slug, person)
		if got := err == nil && e != nil; got != want {
			t.Errorf("GetPublishedBySlugAndSchemas(%s) found = %v, want %v (%v)", slug, got, want, err)
		}
	}

	slugs := func(items []*entity.Entity) (m map[string]bool) {
		m = map[string]bool{}
		for _, e := range items {
			m[e.Slug] = true
		}
		return m
	}
	check := func(what string, got map[string]bool) {
		t.Helper()
		if !got["pv-shown"] || got["pv-hidden"] {
			t.Errorf("%s = %v, want pv-shown only", what, got)
		}
	}

	items, _, err := repo.ListPublishedFiltered(r.ctx, entity.ListFilter{SchemaSlugs: person}, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	check("query (ListPublishedFiltered)", slugs(items))
	for _, e := range items {
		if e.Slug == "pv-shown" {
			if byID, err := repo.GetPublishedByID(r.ctx, e.ID); err != nil || byID == nil {
				t.Errorf("public person by id: %v", err)
			}
		}
	}

	items, _, err = repo.ListPublishedFiltered(r.ctx, entity.ListFilter{Search: "zebrafish"}, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	check("search", slugs(items))

	page, err := repo.ListPublished(r.ctx, "person", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	check("ListPublished", slugs(page.Items))

	entries, err := repo.ListAllPublished(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Slug] = true
	}
	check("sitemap / llms.txt entries", got)

	res := newRenderData(r.srv).Query(render.QuerySpec{Schema: "person", Limit: 50})
	got = map[string]bool{}
	for _, it := range res.Items {
		got[it.Title] = true
	}
	if !got["Zebrafish pv-shown"] || got["Zebrafish pv-hidden"] {
		t.Errorf("theme query = %v", got)
	}
}

// The Article author points at who the post credits: a public person by their
// Person document, a hidden person by name only, the organisation by @id.
func TestArticleAuthorJSONLD_Integration(t *testing.T) {
	r := newNestedRig(t)
	post, err := r.svc.GetSchemaBySlug(r.ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: post.ID, Name: "Credited", Slug: "jsonld-author-it",
		Attributes: json.RawMessage(`{"author":"Ada Byline"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		r.exec(`DELETE FROM hatamoto_entities WHERE id = $1 OR slug = 'ada-byline'`, e.ID)
		r.exec(`DELETE FROM hatamoto_authors WHERE slug = 'ada-byline'`)
	})
	if _, err := r.svc.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	author := func() string {
		t.Helper()
		ent, err := entity.NewPostgresPublicRepository(r.db).GetPublishedByID(r.ctx, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		r.srv.applyAuthorName(r.ctx, ent)
		doc, _ := entity.GenerateJSONLD(ent, "http://example.test")
		r.srv.bylinesOf(r.ctx, e.ID).applyJSONLD(r.ctx, e.ID, "http://example.test", doc)
		b, _ := json.Marshal(doc["author"])
		return string(b)
	}
	if got := author(); got != `{"@type":"Person","name":"Ada Byline"}` {
		t.Errorf("hidden person author = %s", got)
	}
	r.exec(`UPDATE hatamoto_entities SET attributes = '{"public":true}' WHERE id = $1`, *e.BylinePersonID)
	if got := author(); got != `{"@id":"http://example.test/author/ada-byline#person","@type":"Person","name":"Ada Byline","url":"http://example.test/author/ada-byline"}` {
		t.Errorf("public person author = %s", got)
	}
	r.exec(`UPDATE hatamoto_entities SET byline_person_id = NULL, byline_org = true WHERE id = $1`, e.ID)
	if got := author(); got != `{"@id":"http://example.test#organization"}` {
		t.Errorf("organisation author = %s", got)
	}
}

// A person made public through a save is "live" like any published entity: the
// automatic redirect an old rename left at their path is retired, so the page
// answers instead of 301ing to someone else.
func TestPersonGoingPublicRetiresAutoRedirect_Integration(t *testing.T) {
	r := newNestedRig(t)
	sch, err := r.svc.GetSchemaBySlug(r.ctx, "person")
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: sch.ID, Name: "Live X", Slug: "live-x-it",
		Attributes: json.RawMessage(`{"public":false}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_entities WHERE id = $1`, p.ID) })
	r.exec(`INSERT INTO hatamoto_redirects (source_path, target_url, status_code, source_tag) VALUES ('/author/live-x-it', '/author/other', 301, 'entity-move')`)
	r.srv.redirectService.InvalidateCache()
	r.expect("/author/live-x-it", 301)

	public := json.RawMessage(`{"public":true}`)
	if _, err := r.svc.UpdateEntity(r.ctx, p.ID, nil, &entity.UpdateEntityInput{Attributes: &public}); err != nil {
		t.Fatal(err)
	}
	r.expect("/author/live-x-it", 200)
}

// A person who was never public and is renamed while going public has no old
// public path: no redirect is written from the draft slug.
func TestPersonFirstPublicSaveLeavesNoRedirect_Integration(t *testing.T) {
	r := newNestedRig(t)
	sch, err := r.svc.GetSchemaBySlug(r.ctx, "person")
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: sch.ID, Name: "First Y", Slug: "first-y-draft-it",
		Attributes: json.RawMessage(`{"public":false}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_entities WHERE id = $1`, p.ID) })
	slug, public := "first-y-it", json.RawMessage(`{"public":true}`)
	if _, err := r.svc.UpdateEntity(r.ctx, p.ID, nil, &entity.UpdateEntityInput{Slug: &slug, Attributes: &public}); err != nil {
		t.Fatal(err)
	}
	r.expect("/author/first-y-it", 200)
	r.expect("/author/first-y-draft-it", 404)
}
