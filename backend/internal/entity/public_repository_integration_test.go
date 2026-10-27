//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// seedRow inserts a published entity (variant_key may be empty = base row).
func seedRow(t *testing.T, ctx context.Context, repo *PostgresSchemaRepository, schemaID uuid.UUID, slug, name, variantKey string) uuid.UUID {
	t.Helper()
	conn, err := repo.db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var vk any
	if variantKey != "" {
		vk = variantKey
	}
	id := uuid.New()
	_, err = conn.Exec(ctx, `
		INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, published_at, variant_key)
		VALUES ($1, $2, $3, $4, 'published', now(), $5)`, id, schemaID, name, slug, vk)
	if err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
	t.Cleanup(func() {
		c, err := repo.db.GetConn(ctx)
		if err == nil {
			_, _ = c.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id)
			c.Release()
		}
	})
	return id
}

func TestPublicRepo_BaseRowsOnlyAndSchemaAliases(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	pub := NewPostgresPublicRepository(db)

	mk := func(slug string) *EntitySchema {
		s, err := schemas.Create(ctx, &CreateSchemaInput{Name: slug, Slug: slug, Schema: json.RawMessage(`{"type":"object","properties":{}}`)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = schemas.Delete(ctx, s.ID) })
		return s
	}
	// Cleanup order is LIFO: entities (registered later) are deleted first.
	docA, docB := mk("it-doc-a"), mk("it-doc-b")

	baseID := seedRow(t, ctx, schemas, docA.ID, "shared", "Base", "")
	seedRow(t, ctx, schemas, docA.ID, "shared", "Variant", "variant-1")
	seedRow(t, ctx, schemas, docA.ID, "only-variant", "Only variant", "variant-1")
	seedRow(t, ctx, schemas, docB.ID, "other", "Other", "")

	// GetPublishedBySlug returns the base row, never the variant sibling.
	for i := 0; i < 5; i++ {
		ent, err := pub.GetPublishedBySlug(ctx, "shared")
		if err != nil || ent.ID != baseID {
			t.Fatalf("GetPublishedBySlug = %v, %v; want base row", ent, err)
		}
	}
	if _, err := pub.GetPublishedBySlug(ctx, "only-variant"); err != ErrEntityNotFound {
		t.Fatalf("variant-only slug must not resolve, got %v", err)
	}

	// Schema-scoped lookups, single and alias set.
	if ent, err := pub.GetPublishedBySlugAndSchema(ctx, "shared", "it-doc-a"); err != nil || ent.ID != baseID {
		t.Fatalf("AndSchema = %v, %v", ent, err)
	}
	if _, err := pub.GetPublishedBySlugAndSchema(ctx, "shared", "it-doc-b"); err != ErrEntityNotFound {
		t.Fatalf("wrong schema must not resolve, got %v", err)
	}
	if ent, err := pub.GetPublishedBySlugAndSchemas(ctx, "other", []string{"it-doc-a", "it-doc-b"}); err != nil || ent.Slug != "other" {
		t.Fatalf("AndSchemas = %v, %v", ent, err)
	}

	// Archives list base rows only; aliases merge schemas.
	res, err := pub.ListPublished(ctx, "it-doc-a", 1, 50)
	if err != nil || res.Total != 1 || len(res.Items) != 1 || res.Items[0].ID != baseID {
		t.Fatalf("ListPublished = %+v, %v", res, err)
	}
	res, err = pub.ListPublishedBySchemas(ctx, []string{"it-doc-a", "it-doc-b"}, 1, 50)
	if err != nil || res.Total != 2 || len(res.Items) != 2 {
		t.Fatalf("ListPublishedBySchemas = %+v, %v", res, err)
	}

	// Sitemap feed: no variant rows.
	all, err := pub.ListAllPublished(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range all {
		if e.Slug == "shared" {
			n++
		}
		if e.Slug == "only-variant" {
			t.Fatal("ListAllPublished returned a variant row")
		}
	}
	if n != 1 {
		t.Fatalf("ListAllPublished returned %d rows for slug shared, want 1", n)
	}
}
