//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnsureReuseSchemasIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	for i := 0; i < 2; i++ {
		st, err := EnsureReuseSchemas(ctx, db.Pool(), testLogger())
		if err != nil || !st.Block || !st.Part {
			t.Fatalf("run %d: %+v %v", i, st, err)
		}
	}
	for _, slug := range []string{BlockSchemaSlug, PartSchemaSlug} {
		var n int
		var builtin bool
		var settings string
		if err := db.Pool().QueryRow(ctx, `SELECT count(*), bool_and(is_builtin), min(settings::text) FROM hatamoto_entity_schemas WHERE slug = $1`, slug).Scan(&n, &builtin, &settings); err != nil {
			t.Fatal(err)
		}
		if n != 1 || !builtin || settings != `{"sitemap": {"exclude": true}}` {
			t.Errorf("%s: rows=%d builtin=%v settings=%s", slug, n, builtin, settings)
		}
	}
}

func TestEnsureReuseSchemasForeignOwner(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	// Run inside a rolled-back transaction so the shared schema table is untouched.
	tx, err := db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE slug IN ('block','theme_part')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, is_builtin) VALUES ('My block', 'block', '{"type":"object"}', false)`); err != nil {
		t.Fatal(err)
	}
	st, err := EnsureReuseSchemas(ctx, tx, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if st.Block || !st.Part {
		t.Errorf("status = %+v, want block unavailable, part available", st)
	}
	var name string
	var builtin bool
	if err := tx.QueryRow(ctx, `SELECT name, is_builtin FROM hatamoto_entity_schemas WHERE slug = 'block'`).Scan(&name, &builtin); err != nil || name != "My block" || builtin {
		t.Errorf("foreign schema changed: %q %v %v", name, builtin, err)
	}
}

// Every service write path keeps a block or part a draft.
func TestReuseEntitiesStayDraft(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	if _, err := EnsureReuseSchemas(ctx, db.Pool(), testLogger()); err != nil {
		t.Fatal(err)
	}
	schemas := NewPostgresSchemaRepository(db)
	repo := NewPostgresEntityRepository(db)
	svc := NewService(schemas, repo, testLogger())
	published := StatusPublished
	for _, slug := range []string{BlockSchemaSlug, PartSchemaSlug} {
		sc, err := schemas.GetBySlug(ctx, slug)
		if err != nil {
			t.Fatal(err)
		}
		attrs := `{"slot":"header","page_doc":{"v":2,"nodes":[]}}`
		suffix := slug + "-" + uuid.NewString()[:8]
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: sc.ID, Name: suffix, Slug: suffix, Status: &published, Attributes: json.RawMessage(attrs)})
		if err != nil {
			t.Fatalf("%s create: %v", slug, err)
		}
		t.Cleanup(func() { _ = repo.Delete(ctx, e.ID) })
		want := func(what string) {
			t.Helper()
			got, err := repo.GetByID(ctx, e.ID)
			if err != nil || got.Status != StatusDraft || got.PublishedAt != nil {
				t.Fatalf("%s %s: status=%v published_at=%v err=%v", slug, what, got.Status, got.PublishedAt, err)
			}
		}
		want("create with published")

		next := json.RawMessage(`{"slot":"header","description":"v2","page_doc":{"v":2,"nodes":[]}}`)
		if _, err := svc.UpdateEntity(ctx, e.ID, nil, &UpdateEntityInput{Attributes: &next, Status: &published}); err != nil {
			t.Fatalf("%s update: %v", slug, err)
		}
		want("update with published")
		name := "renamed"
		if _, err := svc.UpdateEntity(ctx, e.ID, nil, &UpdateEntityInput{Name: &name, Status: &published}); err != nil {
			t.Fatal(err)
		}
		want("rename with published")

		for _, st := range []EntityStatus{StatusPublished, StatusDraft, StatusArchived} {
			if _, err := svc.UpdateEntityStatus(ctx, e.ID, nil, st); err == nil || !errors.Is(err, ErrValidationFailed) {
				t.Errorf("%s UpdateEntityStatus(%s) = %v", slug, st, err)
			}
		}
		if _, err := svc.ScheduleEntity(ctx, e.ID, nil, time.Now().Add(time.Hour)); err == nil || !errors.Is(err, ErrValidationFailed) {
			t.Errorf("%s schedule = %v", slug, err)
		}
		want("after refused status calls")

		if _, err := svc.RestoreVersion(ctx, e.ID, nil, 1); err != nil {
			t.Fatalf("%s restore: %v", slug, err)
		}
		want("restore")

		res, err := svc.Import(ctx, nil, ImportBatch{Source: "reuse-test", Items: []ImportItem{
			{Key: suffix + "-i", SchemaSlug: slug, Name: "imp", Slug: suffix + "-i", Status: StatusPublished, Attributes: json.RawMessage(attrs)},
		}})
		if err != nil || res.Items[0].Err != nil {
			t.Fatalf("%s import = %v, %v", slug, res, err)
		}
		t.Cleanup(func() { _ = repo.Delete(ctx, res.Items[0].ID) })
		if got, _ := repo.GetByID(ctx, res.Items[0].ID); got == nil || got.Status != StatusDraft {
			t.Errorf("%s import status = %v", slug, got)
		}
	}
}

// A block or part that is published by some other route (an older release, a
// restore, raw SQL) is never served publicly, and the next boot puts it back
// to draft.
func TestReuseRowsPublishedElsewhereAreHiddenAndReset(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	if _, err := EnsureReuseSchemas(ctx, db.Pool(), testLogger()); err != nil {
		t.Fatal(err)
	}
	schemas := NewPostgresSchemaRepository(db)
	repo := NewPostgresEntityRepository(db)
	public := NewPostgresPublicRepository(db)
	var ids []uuid.UUID
	for _, slug := range []string{BlockSchemaSlug, PartSchemaSlug} {
		sc, err := schemas.GetBySlug(ctx, slug)
		if err != nil {
			t.Fatal(err)
		}
		name := slug + "-" + uuid.NewString()[:8]
		e, err := NewService(schemas, repo, testLogger()).CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: sc.ID, Name: name, Slug: name, Attributes: json.RawMessage(`{"slot":"header"}`)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Delete(ctx, e.ID) })
		ids = append(ids, e.ID)
		if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET status = 'published', published_at = now() WHERE id = $1`, e.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := public.GetPublishedBySlug(ctx, name); err == nil && got != nil {
			t.Errorf("%s: served publicly by slug", slug)
		}
		if got, err := public.GetPublishedByID(ctx, e.ID); err == nil && got != nil {
			t.Errorf("%s: served publicly by id", slug)
		}
		res, err := public.ListPublished(ctx, slug, 1, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range res.Items {
			if it.ID == e.ID {
				t.Errorf("%s: listed publicly", slug)
			}
		}
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := EnsureReuseSchemas(ctx, db.Pool(), testLogger()); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			got, err := repo.GetByID(ctx, id)
			if err != nil || got.Status != StatusDraft || got.PublishedAt != nil {
				t.Fatalf("run %d: status=%v published_at=%v err=%v", i, got.Status, got.PublishedAt, err)
			}
		}
	}
}
