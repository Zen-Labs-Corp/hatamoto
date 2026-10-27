//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// Converting is not a content edit: updated_at, which feeds the
// public dateModified and the sitemap lastmod, stays; a normal update bumps it.
func TestBuilderConvert_KeepsUpdatedAt_Integration(t *testing.T) {
	dsn, pool := scratchDB(t)
	ctx := context.Background()
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	schemaID, id := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'conv','conv','{"type":"object"}','{}')`, schemaID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes,published_at,updated_at)
		VALUES ($1,$2,'p','p','published',$3::jsonb,'2026-01-01','2026-10-12 10:00:00+00')`, id,
		schemaID, `{"page_sections":[{"id":"a","type":"content","sort_order":1,"content":{"body":"<p>x</p>"}}]}`); err != nil {
		t.Fatal(err)
	}
	state := func() (int, time.Time) {
		var v int
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT version, updated_at FROM hatamoto_entities WHERE id=$1`, id).Scan(&v, &at); err != nil {
			t.Fatal(err)
		}
		return v, at
	}
	run := func(args ...string) {
		t.Helper()
		var out bytes.Buffer
		if err := runBuilderConvert(ctx, dsn, args, &out); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
	}
	v0, at0 := state()

	run()
	v1, at1 := state()
	if v1 != v0+1 || !at1.Equal(at0) {
		t.Fatalf("after convert: version %d->%d, updated_at %v->%v", v0, v1, at0, at1)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entity_versions WHERE entity_id=$1 AND change_summary='Converted to builder doc'`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("convert versions = %d (%v)", n, err)
	}

	// The setting is transaction-local: a normal update still stamps updated_at.
	t.Setenv("DATABASE_URL", dsn)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := entity.NewService(entity.NewPostgresSchemaRepository(db), entity.NewPostgresEntityRepository(db), quietLogger())
	attrs := []byte(`{"page_doc":{"v":2,"nodes":[{"id":"a-c","t":"container","p":{"tag":"section","v1":true},"c":[{"id":"a","t":"component","p":{"type":"content","content":{"body":"<p>edited</p>"}}}]}]}}`)
	raw := json.RawMessage(attrs)
	e, err := svc.UpdateEntity(ctx, id, nil, &entity.UpdateEntityInput{Attributes: &raw})
	if err != nil {
		t.Fatal(err)
	}
	if _, at3 := state(); !at3.After(at0) || !e.UpdatedAt.After(at0) {
		t.Fatalf("a normal update kept updated_at %v", at3)
	}
}
