//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// ExcludeSchemaIDs drops the entities of the listed types and keeps the rest,
// in the list and in its total.
func TestListExcludesSchemaIDs(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	repo := NewPostgresEntityRepository(db)
	var made []*EntitySchema
	for _, n := range []string{"keep", "drop"} {
		slug := fmt.Sprintf("%s-%s", n, uuid.NewString()[:8])
		sc, err := schemas.Create(ctx, &CreateSchemaInput{Name: slug, Slug: slug, Schema: json.RawMessage(`{"type":"object","properties":{}}`)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = schemas.Delete(ctx, sc.ID) })
		e, err := repo.Create(ctx, nil, &CreateEntityInput{SchemaID: sc.ID, Name: slug, Slug: slug, Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Delete(ctx, e.ID) })
		made = append(made, sc)
	}
	res, err := repo.ListWithOffset(ctx, &ListEntitiesFilter{ExcludeSchemaIDs: []uuid.UUID{made[1].ID}, Search: "-"}, &OffsetPage{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var keep, drop bool
	for _, e := range res.Items {
		keep = keep || e.SchemaID == made[0].ID
		drop = drop || e.SchemaID == made[1].ID
	}
	if !keep || drop {
		t.Errorf("kept the excluded type or lost the other: keep=%v drop=%v", keep, drop)
	}
}
