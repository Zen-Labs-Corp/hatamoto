//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// seedAttrs inserts a published base entity with attributes.
func seedAttrs(t *testing.T, ctx context.Context, repo *PostgresSchemaRepository, schemaID uuid.UUID, slug, name, attrs, status string) {
	t.Helper()
	conn, err := repo.db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	id := uuid.New()
	if _, err := conn.Exec(ctx, `
		INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes, published_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, now())`, id, schemaID, name, slug, status, attrs); err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
	t.Cleanup(func() {
		if c, err := repo.db.GetConn(ctx); err == nil {
			_, _ = c.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id)
			c.Release()
		}
	})
}

func slugsOf(items []*Entity) []string {
	var out []string
	for _, e := range items {
		out = append(out, e.Slug)
	}
	return out
}

func TestListPublishedFiltered_AttrOrderAndValues(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	sch := fmt.Sprintf("tdf-%s", uuid.NewString()[:8])
	schema, err := schemas.Create(ctx, &CreateSchemaInput{Name: sch, Slug: sch, Schema: json.RawMessage(`{"type":"object","properties":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, schema.ID) })
	schemaID := schema.ID
	seedAttrs(t, ctx, schemas, schemaID, "p10", "Zed", `{"series_slug":"s1","series_part":10,"category":"B"}`, "published")
	seedAttrs(t, ctx, schemas, schemaID, "p2", "Amy", `{"series_slug":"s1","series_part":2,"category":"A"}`, "published")
	seedAttrs(t, ctx, schemas, schemaID, "p1", "Bob", `{"series_slug":"s1","series_part":"1","category":"A"}`, "published")
	seedAttrs(t, ctx, schemas, schemaID, "other", "Cy", `{"series_slug":"s2","category":"C"}`, "published")
	seedAttrs(t, ctx, schemas, schemaID, "draft", "Dee", `{"series_slug":"s1","category":"D"}`, "draft")
	repo := NewPostgresPublicRepository(db)
	list := func(f ListFilter, page, per int) ([]string, int) {
		t.Helper()
		f.SchemaSlugs = []string{sch}
		items, total, err := repo.ListPublishedFiltered(ctx, f, page, per)
		if err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
		return slugsOf(items), total
	}

	got, total := list(ListFilter{Attr: "series_slug", AttrEq: "s1", Order: "attr:series_part"}, 1, 10)
	if want := []string{"p1", "p2", "p10"}; !slices.Equal(got, want) || total != 3 {
		t.Errorf("series by numeric part = %v (%d), want %v: draft excluded, 2 before 10", got, total, want)
	}
	got, _ = list(ListFilter{Attr: "series_slug", AttrEq: "s1", Order: "-attr:series_part"}, 1, 10)
	if want := []string{"p10", "p2", "p1"}; !slices.Equal(got, want) {
		t.Errorf("desc = %v, want %v", got, want)
	}
	got, total = list(ListFilter{Attr: "series_part", AttrEq: "2"}, 1, 10) // a number attribute matches its text
	if !slices.Equal(got, []string{"p2"}) || total != 1 {
		t.Errorf("numeric eq = %v (%d)", got, total)
	}
	got, _ = list(ListFilter{Order: "name"}, 1, 10)
	if want := []string{"p2", "p1", "other", "p10"}; !slices.Equal(got, want) {
		t.Errorf("by name = %v, want %v", got, want)
	}
	got, total = list(ListFilter{Order: "-name"}, 2, 3)
	if !slices.Equal(got, []string{"p2"}) || total != 4 {
		t.Errorf("page 2 of desc names = %v (%d)", got, total)
	}
	if got, total = list(ListFilter{Attr: "bad key; drop", AttrEq: "x"}, 1, 10); len(got) != 0 || total != 0 {
		t.Errorf("invalid key must match nothing: %v %d", got, total)
	}
	if got, _ = list(ListFilter{Attr: "series_slug", AttrEq: "s1' OR '1'='1"}, 1, 10); len(got) != 0 {
		t.Errorf("injection-shaped value matched: %v", got)
	}
	if got, _ = list(ListFilter{Order: "attr:x; DROP TABLE hatamoto_entities"}, 1, 10); len(got) != 4 {
		t.Errorf("invalid order falls back to the default: %v", got)
	}

	// An eq given but empty matches the empty string; NaN/Inf stay strings.
	seedAttrs(t, ctx, schemas, schemaID, "blank", "Eve", `{"series_slug":"","tag":"NaN","flag":true}`, "published")
	if got, _ = list(ListFilter{Attr: "series_slug", AttrEqSet: true}, 1, 10); !slices.Equal(got, []string{"blank"}) {
		t.Errorf("empty eq = %v", got)
	}
	if got, _ = list(ListFilter{Attr: "tag", AttrEq: "NaN"}, 1, 10); !slices.Equal(got, []string{"blank"}) {
		t.Errorf("NaN eq = %v", got)
	}
	if got, _ = list(ListFilter{Attr: "tag", AttrEq: "+Inf"}, 1, 10); len(got) != 0 {
		t.Errorf("Inf eq = %v", got)
	}
	if got, _ = list(ListFilter{Attr: "flag", AttrEq: "true"}, 1, 10); !slices.Equal(got, []string{"blank"}) {
		t.Errorf("bool eq = %v", got)
	}
	if got, total = list(ListFilter{Attr: "series_slug"}, 1, 10); total != 5 {
		t.Errorf("existence = %v (%d)", got, total)
	}
	vals, err := repo.ListAttrValues(ctx, []string{sch}, "category", 50)
	if err != nil {
		t.Fatal(err)
	}
	want := []AttrValueCount{{"A", 2}, {"B", 1}, {"C", 1}} // draft D and non-published ignored
	if !slices.Equal(vals, want) {
		t.Errorf("attr values = %v, want %v", vals, want)
	}
	if vals, _ := repo.ListAttrValues(ctx, []string{sch}, "series_part", 50); len(vals) != 1 || vals[0].Value != "1" {
		t.Errorf("only string values count: %v", vals)
	}
	if vals, _ := repo.ListAttrValues(ctx, []string{sch}, "category", 2); len(vals) != 2 {
		t.Errorf("limit not applied: %v", vals)
	}
}

func TestListPublishedFiltered_ExcludeOffsetUpdatedAt(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	sch := fmt.Sprintf("tdo-%s", uuid.NewString()[:8])
	schema, err := schemas.Create(ctx, &CreateSchemaInput{Name: sch, Slug: sch, Schema: json.RawMessage(`{"type":"object","properties":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, schema.ID) })
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		seedAttrs(t, ctx, schemas, schema.ID, n, n, `{}`, "published")
	}
	seedAttrs(t, ctx, schemas, schema.ID, "draft", "draft", `{}`, "draft")
	seedAttrs(t, ctx, schemas, schema.ID, "arch", "arch", `{}`, "archived")
	seedAttrs(t, ctx, schemas, schema.ID, "soon", "soon", `{}`, "scheduled")
	// A trigger stamps updated_at on update: touching e..a in turn makes a the newest.
	conn, err := schemas.db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, n := range []string{"e", "d", "c", "b", "a"} {
		if _, err := conn.Exec(ctx, `UPDATE hatamoto_entities SET metadata = metadata WHERE schema_id = $1 AND slug = $2`, schema.ID, n); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewPostgresPublicRepository(db)
	list := func(f ListFilter, page, per int) ([]string, int) {
		t.Helper()
		f.SchemaSlugs = []string{sch}
		items, total, err := repo.ListPublishedFiltered(ctx, f, page, per)
		if err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
		return slugsOf(items), total
	}
	eq := func(name string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}

	// updated_at both directions; draft, archived and scheduled never appear.
	got, total := list(ListFilter{Order: "-updated_at"}, 1, 20)
	eq("-updated_at", got, []string{"a", "b", "c", "d", "e"})
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	got, _ = list(ListFilter{Order: "updated_at"}, 1, 20)
	eq("updated_at", got, []string{"e", "d", "c", "b", "a"})
	got, total = list(ListFilter{Order: "-updated_at", IncludeScheduled: true}, 1, 20)
	if total != 6 || !slices.Contains(got, "soon") || slices.Contains(got, "draft") || slices.Contains(got, "arch") {
		t.Errorf("scheduled = %v total=%d", got, total)
	}

	// Exclude omits one row; the total drops with it.
	var aID uuid.UUID
	if err := conn.QueryRow(ctx, `SELECT id FROM hatamoto_entities WHERE schema_id = $1 AND slug = 'a'`, schema.ID).Scan(&aID); err != nil {
		t.Fatal(err)
	}
	got, total = list(ListFilter{Order: "-updated_at", ExcludeID: aID}, 1, 20)
	eq("exclude", got, []string{"b", "c", "d", "e"})
	if total != 4 {
		t.Errorf("exclude total = %d, want 4", total)
	}

	// Offset+limit windows equal the slice of the full order; the total is raw.
	full, _ := list(ListFilter{Order: "-updated_at"}, 1, 20)
	got, total = list(ListFilter{Order: "-updated_at", Offset: 1}, 1, 2)
	eq("offset 1 page 1", got, full[1:3])
	if total != 5 {
		t.Errorf("offset total = %d, want 5", total)
	}
	got, _ = list(ListFilter{Order: "-updated_at", Offset: 1}, 2, 2)
	eq("offset 1 page 2", got, full[3:5])
	got, _ = list(ListFilter{Order: "-updated_at", Offset: 4000}, 1, 2)
	eq("offset clamped", got, []string{})
	got, _ = list(ListFilter{Order: "-updated_at", Offset: -3}, 1, 2)
	eq("negative offset", got, full[:2])
}

// A block and a hidden person are never listed, whatever the filter.
func TestListPublishedFiltered_HidesBlocksAndHiddenPeople(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	conn, err := db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	sfx := uuid.NewString()[:8]
	ids := map[string]uuid.UUID{"block": uuid.New(), "person-hidden": uuid.New(), "person-public": uuid.New()}
	for slug, id := range ids {
		kind, attrs := "block", `{}`
		if slug != "block" {
			kind, attrs = "person", fmt.Sprintf(`{"public":%v}`, slug == "person-public")
		}
		if _, err := conn.Exec(ctx, `
			INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes, published_at)
			SELECT $1, id, $2::text, $2::text, 'published', $3::jsonb, now() FROM hatamoto_entity_schemas WHERE slug = $4`,
			id, slug+"-"+sfx, attrs, kind); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id)
		}
	})
	repo := NewPostgresPublicRepository(db)
	for _, f := range []ListFilter{
		{SchemaSlugs: []string{"block", "person"}},
		{SchemaSlugs: []string{"block", "person"}, IncludeScheduled: true, Order: "-updated_at"},
		{Search: sfx},
	} {
		items, _, err := repo.ListPublishedFiltered(ctx, f, 1, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range items {
			if e.ID == ids["block"] || e.ID == ids["person-hidden"] {
				t.Errorf("%+v listed %s", f, e.Slug)
			}
		}
		if f.Search == "" && !slices.ContainsFunc(items, func(e *Entity) bool { return e.ID == ids["person-public"] }) {
			t.Errorf("%+v lost the public person", f)
		}
	}
}
