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

// ListFilter.IDs restricts a listing to an id set; an empty non-nil set matches nothing.
func TestListPublishedFiltered_IDs(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	sch := fmt.Sprintf("tid-%s", uuid.NewString()[:8])
	schema, err := schemas.Create(ctx, &CreateSchemaInput{Name: sch, Slug: sch, Schema: json.RawMessage(`{"type":"object","properties":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, schema.ID) })
	for _, s := range []string{"a", "b", "c"} {
		seedAttrs(t, ctx, schemas, schema.ID, s, s, `{}`, "published")
	}
	seedAttrs(t, ctx, schemas, schema.ID, "d", "d", `{}`, "draft")

	repo := NewPostgresPublicRepository(db)
	all, _, err := repo.ListPublishedFiltered(ctx, ListFilter{SchemaSlugs: []string{sch}, Order: "name"}, 1, 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("seed list = %d, %v", len(all), err)
	}
	list := func(ids []uuid.UUID) ([]string, int) {
		t.Helper()
		items, total, err := repo.ListPublishedFiltered(ctx, ListFilter{SchemaSlugs: []string{sch}, Order: "name", IDs: ids}, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		return slugsOf(items), total
	}
	if got, total := list([]uuid.UUID{all[0].ID, all[2].ID}); !slices.Equal(got, []string{"a", "c"}) || total != 2 {
		t.Errorf("ids = %v (%d), want [a c]", got, total)
	}
	if got, total := list([]uuid.UUID{uuid.New()}); len(got) != 0 || total != 0 {
		t.Errorf("unknown id = %v (%d)", got, total)
	}
	if got, total := list([]uuid.UUID{}); len(got) != 0 || total != 0 {
		t.Errorf("empty set = %v (%d), want nothing", got, total)
	}
	if got, total := list(nil); len(got) != 3 || total != 3 {
		t.Errorf("nil set = %v (%d), want all", got, total)
	}
}
