package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
)

type fakeDuplicator struct {
	user, src uuid.UUID
	err       error
}

func (f *fakeDuplicator) Duplicate(_ context.Context, user, src uuid.UUID) (*entity.Entity, error) {
	f.user, f.src = user, src
	if f.err != nil {
		return nil, f.err
	}
	return &entity.Entity{ID: uuid.New(), Name: "Copy of About", Slug: "about-copy", Status: entity.StatusDraft}, nil
}

func TestEntitiesDuplicateReturnsTheDraft(t *testing.T) {
	f := &fakeDuplicator{}
	src := uuid.New()
	text, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteEntities}, Deps{Duplicator: f}, "entities_duplicate", map[string]any{"id": src.String()})
	if isErr || f.src != src {
		t.Fatalf("entities_duplicate = %v %q (src %v)", isErr, text, f.src)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out["name"] != "Copy of About" || out["slug"] != "about-copy" || out["status"] != "draft" || out["id"] == "" {
		t.Errorf("result = %v", out)
	}
}

func TestEntitiesDuplicateNeedsWriteScope(t *testing.T) {
	f := &fakeDuplicator{}
	text, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeReadEntities}, Deps{Duplicator: f}, "entities_duplicate", map[string]any{"id": uuid.NewString()})
	if !isErr || !strings.Contains(text, "write:entities") || f.src != uuid.Nil {
		t.Fatalf("read key = %v %q", isErr, text)
	}
}

func TestEntitiesDuplicateErrors(t *testing.T) {
	scopes := []hmauth.APIKeyScope{hmauth.ScopeWriteEntities}
	text, isErr := callSEO(t, scopes, Deps{}, "entities_duplicate", map[string]any{"id": uuid.NewString()})
	if !isErr || !strings.Contains(text, "not available") {
		t.Errorf("nil duplicator = %v %q", isErr, text)
	}
	text, isErr = callSEO(t, scopes, Deps{Duplicator: &fakeDuplicator{err: entity.ErrDuplicateUnsupported}}, "entities_duplicate", map[string]any{"id": uuid.NewString()})
	if !isErr || !strings.Contains(text, "cannot be duplicated") {
		t.Errorf("unsupported = %v %q", isErr, text)
	}
	text, isErr = callSEO(t, scopes, Deps{Duplicator: &fakeDuplicator{}}, "entities_duplicate", map[string]any{"id": "nope"})
	if !isErr || !strings.Contains(text, "UUID") {
		t.Errorf("bad id = %v %q", isErr, text)
	}
}
