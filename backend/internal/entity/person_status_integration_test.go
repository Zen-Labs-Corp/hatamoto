//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// A person's status follows attributes.public at every service write point, so
// the previous release (which knows only the status) never shows a hidden
// person after a rollback.
func TestPersonStatusFollowsPublic(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	repo := NewPostgresEntityRepository(db)
	svc := NewService(schemas, repo, testLogger())
	person, err := schemas.GetBySlug(ctx, "person")
	if err != nil {
		t.Fatalf("person schema (is the database migrated?): %v", err)
	}
	slug := "ps-" + uuid.NewString()[:8]
	mk := func(suffix, attrs string) *Entity {
		t.Helper()
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: person.ID, Name: suffix, Slug: slug + "-" + suffix, Attributes: json.RawMessage(attrs)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Delete(ctx, e.ID) })
		return e
	}
	want := func(what string, id uuid.UUID, status EntityStatus) {
		t.Helper()
		got, err := repo.GetByID(ctx, id)
		if err != nil || got.Status != status {
			t.Fatalf("%s: status = %v (%v), want %s", what, got, err, status)
		}
		if status == StatusPublished && got.PublishedAt == nil {
			t.Errorf("%s: published_at not set", what)
		}
	}

	hidden := mk("hidden", `{"public":false}`)
	shown := mk("shown", `{"public":true}`)
	want("create hidden", hidden.ID, StatusDraft)
	want("create public", shown.ID, StatusPublished)

	set := func(id uuid.UUID, attrs string) {
		t.Helper()
		raw := json.RawMessage(attrs)
		if _, err := svc.UpdateEntity(ctx, id, nil, &UpdateEntityInput{Attributes: &raw}); err != nil {
			t.Fatal(err)
		}
	}
	set(hidden.ID, `{"public":true}`)
	want("update to public", hidden.ID, StatusPublished)
	set(hidden.ID, `{"public":false}`)
	want("update to hidden", hidden.ID, StatusDraft)
	// An update that leaves attributes alone does not change it either.
	name := "Renamed"
	if _, err := svc.UpdateEntity(ctx, shown.ID, nil, &UpdateEntityInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	want("rename public", shown.ID, StatusPublished)

	// Status changes on a person are refused: the Public setting decides.
	for _, st := range []EntityStatus{StatusPublished, StatusDraft, StatusArchived} {
		if _, err := svc.UpdateEntityStatus(ctx, hidden.ID, nil, st); !errors.Is(err, ErrValidationFailed) {
			t.Errorf("UpdateEntityStatus(%s) = %v, want a validation error", st, err)
		}
	}
	want("after refused publish", hidden.ID, StatusDraft)

	// Import follows Public whatever status the source asks for.
	res, err := svc.Import(ctx, nil, ImportBatch{Source: "ps-test", Items: []ImportItem{
		{Key: fmt.Sprintf("%s-i", slug), SchemaSlug: "person", Name: "Imported", Slug: slug + "-i", Status: StatusPublished, Attributes: json.RawMessage(`{"public":false}`)},
	}})
	if err != nil || res.Items[0].Err != nil {
		t.Fatalf("import = %v, %v", res, err)
	}
	t.Cleanup(func() { _ = repo.Delete(ctx, res.Items[0].ID) })
	want("import hidden", res.Items[0].ID, StatusDraft)
}
