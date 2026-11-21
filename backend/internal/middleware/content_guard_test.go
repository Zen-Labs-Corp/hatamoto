package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authztest"
)

func runContentGuard(ctx context.Context, method string) (int, bool) {
	reached := false
	h := ContentGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(method, "/api/v1/entities", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, reached
}

func TestContentGuard(t *testing.T) {
	for _, role := range []string{"owner", "editor", "author", "contributor", "reviewer"} {
		if code, reached := runContentGuard(claimsCtx(role), http.MethodPost); code != http.StatusNoContent || !reached {
			t.Errorf("%s POST = %d reached=%v, want passed", role, code, reached)
		}
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if code, reached := runContentGuard(claimsCtx("read-only"), m); code != http.StatusForbidden || reached {
			t.Errorf("read-only %s = %d reached=%v, want 403", m, code, reached)
		}
	}
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if code, reached := runContentGuard(claimsCtx("read-only"), m); code != http.StatusNoContent || !reached {
			t.Errorf("read-only %s = %d reached=%v, want passed", m, code, reached)
		}
	}
}

func TestContentGuardKeyRequestPasses(t *testing.T) {
	creator := uuid.New()
	key := &auth.APIKey{ID: uuid.New(), CreatedBy: &creator, CreatorRole: "read-only", Scopes: []auth.APIKeyScope{auth.ScopeAll}}
	ctx := context.WithValue(claimsCtx("editor"), apiKeyContextKey, key)
	ctx = context.WithValue(ctx, authTypeContextKey, AuthTypeAPIKey)
	if code, reached := runContentGuard(ctx, http.MethodPost); code != http.StatusNoContent || !reached {
		t.Errorf("key POST = %d reached=%v, want passed to the route's own gate", code, reached)
	}
}

func TestContentGuardAskedAuthorizerDenies(t *testing.T) {
	authztest.Use(t, authztest.Func(func(context.Context, authz.Subject, authz.Action, authz.Object) (bool, error) {
		return false, nil
	}))
	if code, reached := runContentGuard(claimsCtx("owner"), http.MethodPost); code != http.StatusForbidden || reached {
		t.Errorf("owner POST under deny-all = %d reached=%v, want 403", code, reached)
	}
}
