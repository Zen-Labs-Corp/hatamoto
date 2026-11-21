package mcpserver

import (
	"net/http/httptest"
	"testing"

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
