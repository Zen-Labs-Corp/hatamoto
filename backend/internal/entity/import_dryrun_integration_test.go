//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// A dry run does the real write and rolls it back: constraints are exercised,
// no row or version changes, and the same batch then imports for real.
func TestImportDryRunRollsBack(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	repo := NewPostgresEntityRepository(db)
	svc := NewService(schemas, repo, testLogger())
	sink := &recordingSink{}
	svc.SetEvents(sink)
	sch := "dryrun-" + uuid.NewString()[:8]
	schema, err := schemas.Create(ctx, &CreateSchemaInput{Name: sch, Slug: sch, Schema: jsontext.Value(`{"type":"object","required":["title"],"properties":{"title":{"type":"string"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, schema.ID) })

	count := func(q string) int {
		t.Helper()
		var n int
		if err := db.Pool().QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	counts := func() (int, int) {
		return count(`SELECT count(*) FROM hatamoto_entities`), count(`SELECT count(*) FROM hatamoto_entity_versions`)
	}
	pfx := "dry-" + uuid.NewString()[:8]
	item := func(key, slug, title string) ImportItem {
		return ImportItem{Key: pfx + key, SchemaSlug: schema.Slug, Name: title, Slug: pfx + slug, Attributes: jsontext.Value(`{"title":"` + title + `"}`)}
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE metadata->>'import_key' LIKE $1`, pfx+"%")
	})

	// An existing row, to be updated by the dry run and by the real one.
	if _, err := svc.Import(ctx, nil, ImportBatch{Source: "test", Items: []ImportItem{item("old", "old", "Old")}}); err != nil {
		t.Fatal(err)
	}
	ents, vers := counts()
	events := len(sink.kinds())

	batch := ImportBatch{Source: "test", DryRun: true, Items: []ImportItem{
		item("old", "old", "Old changed"), item("a", "a", "A"), item("b", "a", "B"),
	}}
	res, err := svc.Import(ctx, nil, batch)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.Updated != 1 || res.Failed != 1 || !errors.Is(res.Items[2].Err, ErrSlugAlreadyExists) {
		t.Fatalf("dry run = %+v (item 2: %v)", res, res.Items[2].Err)
	}
	if e2, v2 := counts(); e2 != ents || v2 != vers {
		t.Fatalf("a dry run changed rows: entities %d->%d, versions %d->%d", ents, e2, vers, v2)
	}
	if len(sink.kinds()) != events {
		t.Fatalf("a dry run emitted events: %v", sink.kinds())
	}
	old, err := repo.GetByImportKey(ctx, pfx+"old")
	if err != nil || old.Name != "Old" {
		t.Fatalf("existing row after a dry run = %+v (%v)", old, err)
	}

	// A slug already used in the database is reported by the real constraint.
	clash := item("c", "old", "C")
	clash.Attributes = jsontext.Value(`{"title":"C"}`)
	res, err = svc.Import(ctx, nil, ImportBatch{Source: "test", DryRun: true, Items: []ImportItem{clash}})
	if err != nil || !errors.Is(res.Items[0].Err, ErrSlugAlreadyExists) {
		t.Fatalf("slug clash dry run = %+v (%v)", res, err)
	}

	batch.DryRun = false
	batch.Items = batch.Items[:2]
	res, err = svc.Import(ctx, nil, batch)
	if err != nil || res.Created != 1 || res.Updated != 1 || res.Failed != 0 {
		t.Fatalf("real run = %+v (%v)", res, err)
	}
	if e2, _ := counts(); e2 != ents+1 {
		t.Fatalf("real run entities = %d, want %d", e2, ents+1)
	}
}
