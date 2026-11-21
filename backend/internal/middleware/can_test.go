package middleware

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
)

func TestCanRequestSubject(t *testing.T) {
	claims := func(role string) context.Context {
		return ContextWithClaims(context.Background(), &auth.Claims{UserID: uuid.New(), Role: role})
	}
	if !Can(claims("owner"), authz.ActionUserManage, authz.Object{}) {
		t.Error("owner session denied user.manage")
	}
	if Can(claims("editor"), authz.ActionUserManage, authz.Object{}) {
		t.Error("editor session allowed user.manage")
	}
	if Can(context.Background(), authz.ActionUserManage, authz.Object{}) {
		t.Error("no subject allowed user.manage")
	}

	// A key request runs with synthesised claims; the key decides, never the
	// session those claims describe, and keys never hold scope-less actions.
	creator := uuid.New()
	key := &auth.APIKey{ID: uuid.New(), CreatedBy: &creator, CreatorRole: "owner", Scopes: []auth.APIKeyScope{auth.ScopeAll}}
	ctx := context.WithValue(claims("owner"), apiKeyContextKey, key)
	ctx = context.WithValue(ctx, authTypeContextKey, AuthTypeAPIKey)
	if Can(ctx, authz.ActionUserManage, authz.Object{}) {
		t.Error("key allowed user.manage because its claims say owner")
	}
	if !Can(ctx, authz.ActionAnalyticsRead, authz.Object{}) {
		t.Error("* key denied analytics.read")
	}
	key.CreatorRole = ""
	if Can(ctx, authz.ActionAnalyticsRead, authz.Object{}) {
		t.Error("key without a creator role allowed analytics.read")
	}
}
