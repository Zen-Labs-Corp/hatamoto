package mcpserver

import (
	"context"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/authz"
	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authztest"
)

// Under DenyAll the creator check refuses an owner-created * key exactly like
// a gone creator: verify fails with ErrInvalidToken.
func TestVerifyRefusesCreatorDeniedByAuthz(t *testing.T) {
	s := &Server{d: Deps{Keys: fakeKeys{scopes: []hmauth.APIKeyScope{hmauth.ScopeAll}, role: "owner"}}}
	r := httptest.NewRequest("POST", "/mcp", nil)

	if _, err := s.verify(t.Context(), "good", r); err != nil {
		t.Fatalf("default authorizer: verify = %v, want nil", err)
	}
	gone := &Server{d: Deps{Keys: fakeKeys{scopes: []hmauth.APIKeyScope{hmauth.ScopeAll}, role: ""}}}
	_, goneErr := gone.verify(t.Context(), "good", r)

	authztest.Use(t, authztest.DenyAll)
	_, err := s.verify(t.Context(), "good", r)
	if err == nil || goneErr == nil || err.Error() != goneErr.Error() {
		t.Fatalf("DenyAll verify = %v, gone creator = %v; want the same refusal", err, goneErr)
	}
}

// creatorActAllowed lets the creator check through and sends every other
// question to a, so a test can aim a at the tool's own action.
func creatorActAllowed(a authz.Authorizer) authztest.Func {
	return func(ctx context.Context, s authz.Subject, act authz.Action, o authz.Object) (bool, error) {
		if act == authz.ActionAPIKeyCreatorAct {
			return true, nil
		}
		return a.Can(ctx, s, act, o)
	}
}
