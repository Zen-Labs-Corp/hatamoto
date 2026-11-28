//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// ExcludeOptedOut hides the llms.txt opt-out and noindex entities in SQL, so
// the total matches the items; without it the listing is unchanged.
func TestListPublishedFiltered_ExcludeOptedOut(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	sch := fmt.Sprintf("tdo-%s", uuid.NewString()[:8])
	schema, err := schemas.Create(ctx, &CreateSchemaInput{Name: sch, Slug: sch, Schema: jsontext.Value(`{"type":"object","properties":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, schema.ID) })
	seedAttrs(t, ctx, schemas, schema.ID, "plain", "Plain", `{}`, "published")
	seedAttrs(t, ctx, schemas, schema.ID, "optout", "Optout", `{"seo":{"include_in_llms_txt":false}}`, "published")
	seedAttrs(t, ctx, schemas, schema.ID, "noindex", "Noindex", `{"seo":{"robots":"noindex"}}`, "published")
	seedAttrs(t, ctx, schemas, schema.ID, "malformed", "Malformed", `{"seo":{"include_in_llms_txt":"yes"}}`, "published")

	repo := NewPostgresPublicRepository(db)
	list := func(exclude bool) ([]string, int) {
		t.Helper()
		items, total, err := repo.ListPublishedFiltered(ctx, ListFilter{SchemaSlugs: []string{sch}, ExcludeOptedOut: exclude, Order: "name"}, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		return slugsOf(items), total
	}
	if got, total := list(true); !slices.Equal(got, []string{"malformed", "plain"}) || total != 2 {
		t.Errorf("with ExcludeOptedOut = %v (%d), want [malformed plain] (2)", got, total)
	}
	if got, total := list(false); len(got) != 4 || total != 4 {
		t.Errorf("without ExcludeOptedOut = %v (%d), want all 4", got, total)
	}
}
