//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// byline_person_id / byline_org round trip through create, update, the admin
// filter, the public filter and the import upsert.
func TestBylineColumnsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	repo := NewPostgresEntityRepository(db)
	pub := NewPostgresPublicRepository(db)
	slug := fmt.Sprintf("byl-%s", uuid.NewString()[:8])
	schema, err := schemas.Create(ctx, &CreateSchemaInput{Name: slug, Slug: slug, Schema: json.RawMessage(`{"type":"object","properties":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, schema.ID) })

	mk := func(name string, in CreateEntityInput) *Entity {
		t.Helper()
		in.SchemaID, in.Name, in.Slug, in.Attributes = schema.ID, name, name, json.RawMessage(`{}`)
		e, err := repo.Create(ctx, nil, &in)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		t.Cleanup(func() { _ = repo.Delete(ctx, e.ID) })
		if _, err := repo.UpdateStatus(ctx, e.ID, nil, StatusPublished); err != nil {
			t.Fatal(err)
		}
		return e
	}
	personID := uuid.New()
	a := mk("a-"+slug, CreateEntityInput{BylinePersonID: &personID})
	b := mk("b-"+slug, CreateEntityInput{BylineOrg: true})
	c := mk("c-"+slug, CreateEntityInput{})
	if a.BylinePersonID == nil || *a.BylinePersonID != personID || a.BylineOrg {
		t.Errorf("create person byline: %+v", a)
	}
	if b.BylinePersonID != nil || !b.BylineOrg {
		t.Errorf("create org byline: %+v", b)
	}

	got, err := repo.GetByID(ctx, a.ID)
	if err != nil || got.BylinePersonID == nil || *got.BylinePersonID != personID {
		t.Fatalf("GetByID byline = %+v, %v", got, err)
	}

	// admin filter
	res, err := repo.ListWithOffset(ctx, &ListEntitiesFilter{SchemaID: &schema.ID, PersonID: &personID}, &OffsetPage{Limit: 10})
	if err != nil || len(res.Items) != 1 || res.Items[0].ID != a.ID {
		t.Errorf("admin PersonID filter = %v, %v", res, err)
	}
	// public filter
	items, _, err := pub.ListPublishedFiltered(ctx, ListFilter{SchemaSlugs: []string{slug}, PersonID: personID}, 1, 10)
	if err != nil || len(items) != 1 || items[0].ID != a.ID {
		t.Errorf("public PersonID filter = %v, %v", items, err)
	}

	// update: set, then clear, then org on/off; an empty update leaves it alone
	other := uuid.New()
	e, err := repo.Update(ctx, c.ID, nil, &UpdateEntityInput{BylinePersonID: &other})
	if err != nil || e.BylinePersonID == nil || *e.BylinePersonID != other {
		t.Fatalf("update set: %+v, %v", e, err)
	}
	yes := true
	e, err = repo.Update(ctx, c.ID, nil, &UpdateEntityInput{ClearBylinePerson: true, BylineOrg: &yes})
	if err != nil || e.BylinePersonID != nil || !e.BylineOrg {
		t.Fatalf("update clear+org: %+v, %v", e, err)
	}
	name := "renamed"
	e, err = repo.Update(ctx, c.ID, nil, &UpdateEntityInput{Name: &name})
	if err != nil || !e.BylineOrg {
		t.Fatalf("an unrelated update changed the byline: %+v, %v", e, err)
	}
	no := false
	e, err = repo.Update(ctx, c.ID, nil, &UpdateEntityInput{BylineOrg: &no})
	if err != nil || e.BylineOrg {
		t.Fatalf("org off: %+v, %v", e, err)
	}

	// import upsert insert and update
	rec := &ImportRecord{SchemaID: schema.ID, Name: "imp", Slug: "imp-" + slug, Status: StatusPublished,
		Attributes: json.RawMessage(`{}`), Metadata: json.RawMessage(`{"import_key":"` + slug + `"}`), BylinePersonID: &personID}
	ie, created, err := repo.ImportUpsert(ctx, rec)
	if err != nil || !created || ie.BylinePersonID == nil || *ie.BylinePersonID != personID {
		t.Fatalf("import insert: %+v, %v, %v", ie, created, err)
	}
	t.Cleanup(func() { _ = repo.Delete(ctx, ie.ID) })
	rec.ID, rec.BylinePersonID, rec.BylineOrg = &ie.ID, nil, true
	ie, _, err = repo.ImportUpsert(ctx, rec)
	if err != nil || ie.BylinePersonID != nil || !ie.BylineOrg {
		t.Fatalf("import update: %+v, %v", ie, err)
	}
}
