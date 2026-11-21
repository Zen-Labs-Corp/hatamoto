package authzsubject

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
)

func TestForKeyAlwaysSetsKeyID(t *testing.T) {
	creator := uuid.New()
	k := &auth.APIKey{CreatedBy: &creator, CreatorRole: "editor", Scopes: []auth.APIKeyScope{auth.ScopeReadEntities}}
	s := ForKey(k)
	if s.KeyID == "" {
		t.Fatal("KeyID is empty for a key with the nil ID")
	}
	if s.Role != "" {
		t.Errorf("Role = %q, want empty", s.Role)
	}
	if s.UserID != creator.String() || s.CreatorRole != "editor" {
		t.Errorf("subject = %+v", s)
	}
}

func TestForKeyCopiesScopes(t *testing.T) {
	k := &auth.APIKey{ID: uuid.New(), Scopes: []auth.APIKeyScope{auth.ScopeReadEntities}}
	s := ForKey(k)
	s.Scopes[0] = "changed"
	if k.Scopes[0] != auth.ScopeReadEntities {
		t.Error("changing the subject changed the key")
	}
	k.Scopes[0] = auth.ScopeAll
	if s.Scopes[0] == string(auth.ScopeAll) {
		t.Error("changing the key changed the subject")
	}
}

func TestForKeyWithoutCreator(t *testing.T) {
	if s := ForKey(&auth.APIKey{ID: uuid.New()}); s.UserID != "" {
		t.Errorf("UserID = %q, want empty", s.UserID)
	}
}

func TestForSession(t *testing.T) {
	s := ForSession("u1", "owner")
	if s.UserID != "u1" || s.Role != "owner" || s.KeyID != "" || s.Scopes != nil {
		t.Errorf("subject = %+v", s)
	}
}

// An editor-created "*" key is not an editor session: it may read entities but
// not manage users or authors.
func TestEditorCreatedFullKeyIsNotAnEditor(t *testing.T) {
	creator := uuid.New()
	s := ForKey(&auth.APIKey{ID: uuid.New(), CreatedBy: &creator, CreatorRole: "editor", Scopes: []auth.APIKeyScope{auth.ScopeAll}})
	for _, c := range []struct {
		act  authz.Action
		want bool
	}{
		{authz.ActionEntityRead, true},
		{authz.ActionUserManage, false},
		{authz.ActionAuthorManage, false},
	} {
		got, err := authz.Default().Can(context.Background(), s, c.act, authz.Object{})
		if err != nil || got != c.want {
			t.Errorf("%s = %v, %v; want %v", c.act, got, err, c.want)
		}
	}
	if !slices.Equal(s.Scopes, []string{"*"}) {
		t.Errorf("scopes = %v", s.Scopes)
	}
}
