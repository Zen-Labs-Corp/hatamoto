//go:build integration

package taxonomy

import (
	"testing"

	"github.com/google/uuid"
)

func TestTermsForEntitiesBatch(t *testing.T) {
	f := newOrderFixture(t)
	otherKind := f.kind + "b"
	t.Cleanup(func() { _, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_terms WHERE kind = $1`, otherKind) })

	zed := f.term("Zed", nil)
	alpha := f.term("Alpha", nil)
	other, err := f.svc.CreateTerm(f.ctx, CreateTermInput{Kind: otherKind, Name: "Other", Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}

	var schemaID uuid.UUID
	if err := f.db.Pool().QueryRow(f.ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema) VALUES ('zo', $1, '{"type":"object"}') RETURNING id`, f.kind).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	entity := func(slug string) uuid.UUID {
		var id uuid.UUID
		if err := f.db.Pool().QueryRow(f.ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at) VALUES ($1, $2, $2, 'published', now()) RETURNING id`, schemaID, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b, bare := entity("a"), entity("b"), entity("bare")
	if err := f.svc.ReplaceEntityTerms(f.ctx, a, []uuid.UUID{other.ID, zed.ID, alpha.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ReplaceEntityTerms(f.ctx, b, []uuid.UUID{zed.ID}); err != nil {
		t.Fatal(err)
	}

	got, err := f.svc.TermsForEntities(f.ctx, []uuid.UUID{a, b, bare})
	if err != nil {
		t.Fatal(err)
	}
	want := []EntityTermRef{
		{Kind: f.kind, Slug: "alpha", Name: "Alpha"},
		{Kind: f.kind, Slug: "zed", Name: "Zed"},
		{Kind: otherKind, Slug: "other", Name: "Other"},
	}
	if len(got[a]) != len(want) {
		t.Fatalf("entity a terms = %+v, want %+v", got[a], want)
	}
	for i := range want {
		if got[a][i] != want[i] {
			t.Errorf("entity a terms[%d] = %+v, want %+v", i, got[a][i], want[i])
		}
	}
	if len(got[b]) != 1 || got[b][0].Slug != "zed" {
		t.Errorf("entity b terms = %+v", got[b])
	}
	if _, ok := got[bare]; ok {
		t.Errorf("entity without terms is in the map: %+v", got[bare])
	}
	if empty, err := f.svc.TermsForEntities(f.ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no ids = %v, %v", empty, err)
	}
}
