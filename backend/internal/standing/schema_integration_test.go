//go:build integration

package standing

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

func activeCount(t *testing.T, s *Store, ctx context.Context, plugin string) int {
	t.Helper()
	var n int
	if err := s.DB.Pool().QueryRow(ctx,
		`SELECT count(*) FROM hatamoto_standing_approvals WHERE plugin = $1 AND voided_at IS NULL`, plugin).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStandingApprovalVoid(t *testing.T) {
	s, ctx := newStore(t)
	svc := entity.NewService(entity.NewPostgresSchemaRepository(s.DB), entity.NewPostgresEntityRepository(s.DB),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	lookup := func(ctx context.Context, slug string) (jsontext.Value, bool, error) {
		sc, err := svc.GetSchemaBySlug(ctx, slug)
		if err != nil {
			return nil, false, nil
		}
		return sc.Schema, true, nil
	}
	svc.SetOnSchemaChange(func() {
		if _, err := s.VoidChangedSchemas(ctx, lookup); err != nil {
			t.Error(err)
		}
	})

	mk := func(slug string) *entity.EntitySchema {
		sc, err := svc.CreateSchema(ctx, &entity.CreateSchemaInput{Name: slug, Slug: slug,
			Schema: jsontext.Value(`{"type":"object","properties":{"price":{"type":"number"}}}`)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE slug = $1`, slug) })
		return sc
	}
	approve := func(plugin, slug string, sc *entity.EntitySchema) {
		h, err := SchemaHash(sc.Schema)
		if err != nil {
			t.Fatal(err)
		}
		fp := Fingerprint{Version: "1.0.0", ManifestHash: "m", SchemaHashes: map[string]string{slug: h}}
		if _, err := s.Approve(ctx, plugin, "sync", Policy{Fields: []string{"price"}, Schemas: []string{slug}, MaxPerHour: 200}, fp, "owner"); err != nil {
			t.Fatal(err)
		}
	}

	named, other := mk("named_"+uuid.NewString()[:8]), mk("other_"+uuid.NewString()[:8])
	pa, pb := testPlugin(t, s, ctx), testPlugin(t, s, ctx)
	approve(pa, named.Slug, named)
	approve(pb, other.Slug, other)

	changed := jsontext.Value(`{"type":"object","properties":{"price":{"type":"string"}}}`)
	if _, err := svc.UpdateSchema(ctx, named.ID, &entity.UpdateSchemaInput{Schema: &changed}); err != nil {
		t.Fatal(err)
	}
	if got := activeCount(t, s, ctx, pa); got != 0 {
		t.Fatalf("changing the named schema left %d active approvals", got)
	}
	if got := activeCount(t, s, ctx, pb); got != 1 {
		t.Fatalf("changing another schema voided an unrelated approval (active %d)", got)
	}

	approve(pa, named.Slug, mustSchema(t, svc, named.Slug))
	if err := svc.DeleteSchema(ctx, named.ID); err != nil {
		t.Fatal(err)
	}
	if got := activeCount(t, s, ctx, pa); got != 0 {
		t.Fatalf("deleting the named schema left %d active approvals", got)
	}
	if got := activeCount(t, s, ctx, pb); got != 1 {
		t.Fatalf("deleting another schema voided an unrelated approval (active %d)", got)
	}
}

func mustSchema(t *testing.T, svc *entity.Service, slug string) *entity.EntitySchema {
	t.Helper()
	sc, err := svc.GetSchemaBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}
