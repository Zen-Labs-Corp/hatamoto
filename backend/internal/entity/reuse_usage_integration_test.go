//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestRefUsers(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	if _, err := EnsureReuseSchemas(ctx, db.Pool(), testLogger()); err != nil {
		t.Fatal(err)
	}
	schemas := NewPostgresSchemaRepository(db)
	repo := NewPostgresEntityRepository(db)
	svc := NewService(schemas, repo, testLogger())
	page, err := schemas.GetBySlug(ctx, "page")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := schemas.GetBySlug(ctx, BlockSchemaSlug)
	part, _ := schemas.GetBySlug(ctx, PartSchemaSlug)
	tag := uuid.NewString()[:8]
	mk := func(sc *EntitySchema, name, attrs string) *Entity {
		t.Helper()
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: sc.ID, Name: name, Slug: fmt.Sprintf("ru-%s-%s", tag, name), Attributes: json.RawMessage(attrs)})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Cleanup(func() { _ = repo.Delete(ctx, e.ID) })
		return e
	}
	docWith := func(id string) string {
		return `{"page_doc":{"v":2,"nodes":[{"id":"r","t":"ref","p":{"block":"` + id + `"}}]}}`
	}

	target := mk(block, "target", `{"page_doc":{"v":2,"nodes":[]}}`)
	id := target.ID.String()
	onPage := mk(page, "onpage", docWith(id))
	inBlock := mk(block, "inblock", docWith(id))
	inPart := mk(part, "inpart", `{"slot":"header","page_doc":{"v":2,"nodes":[{"id":"r","t":"ref","p":{"block":"`+id+`"}}]}}`)
	mk(page, "textonly", `{"page_doc":{"v":2,"nodes":[{"id":"t","t":"text","p":{"text":"`+id+`"}}]}}`)
	mk(page, "other", docWith("aaaaaaaa-2222-3333-4444-555555555555"))
	// A block that points at itself is not its own user.
	self := mk(block, "self", `{"page_doc":{"v":2,"nodes":[]}}`)
	selfDoc := json.RawMessage(docWith(self.ID.String()))
	if _, err := svc.UpdateEntity(ctx, self.ID, nil, &UpdateEntityInput{Attributes: &selfDoc}); err != nil {
		t.Fatal(err)
	}

	users, capped, err := svc.RefUsers(ctx, target.ID, 0)
	if err != nil || capped {
		t.Fatalf("RefUsers = %v capped=%v err=%v", users, capped, err)
	}
	got := map[uuid.UUID]string{}
	for _, u := range users {
		got[u.ID] = u.SchemaSlug
	}
	if len(got) != 3 || got[onPage.ID] != "page" || got[inBlock.ID] != BlockSchemaSlug || got[inPart.ID] != PartSchemaSlug {
		t.Fatalf("users = %+v", users)
	}
	if users, _, _ := svc.RefUsers(ctx, self.ID, 0); len(users) != 0 {
		t.Errorf("self reference listed: %+v", users)
	}

	users, capped, err = svc.RefUsers(ctx, target.ID, 2)
	if err != nil || !capped || len(users) != 2 {
		t.Errorf("cap 2: %d users capped=%v err=%v", len(users), capped, err)
	}
}
