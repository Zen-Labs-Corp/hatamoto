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

func runWriteGuard(ctx context.Context, method string) (int, string, bool) {
	reached := false
	h := WriteGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(method, "/api/v1/entities", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), reached
}

func claimsCtx(role string) context.Context {
	return ContextWithClaims(context.Background(), &auth.Claims{UserID: uuid.New(), Role: role})
}

func TestWriteGuard(t *testing.T) {
	for _, role := range []string{"owner", "editor"} {
		if code, _, reached := runWriteGuard(claimsCtx(role), http.MethodPost); code != http.StatusNoContent || !reached {
			t.Errorf("%s POST = %d reached=%v, want passed", role, code, reached)
		}
	}
	for _, role := range []string{"author", "contributor", "reviewer", "read-only"} {
		code, body, reached := runWriteGuard(claimsCtx(role), http.MethodPost)
		if code != http.StatusForbidden || reached || body != "{\"error\":\"forbidden\"}\n" {
			t.Errorf("%s POST = %d %q reached=%v, want 403 forbidden", role, code, body, reached)
		}
		for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
			if code, _, reached := runWriteGuard(claimsCtx(role), m); code != http.StatusNoContent || !reached {
				t.Errorf("%s %s = %d reached=%v, want passed", role, m, code, reached)
			}
		}
	}
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if code, _, reached := runWriteGuard(claimsCtx("read-only"), m); code != http.StatusForbidden || reached {
			t.Errorf("read-only %s = %d reached=%v, want 403", m, code, reached)
		}
	}
}

func TestWriteGuardFailsClosed(t *testing.T) {
	if code, _, reached := runWriteGuard(context.Background(), http.MethodPost); code != http.StatusForbidden || reached {
		t.Errorf("no claims POST = %d reached=%v, want 403", code, reached)
	}
	ctx := context.WithValue(claimsCtx("owner"), authTypeContextKey, AuthTypeAPIKey)
	if code, _, reached := runWriteGuard(ctx, http.MethodPost); code != http.StatusForbidden || reached {
		t.Errorf("api-key auth without a key POST = %d reached=%v, want 403", code, reached)
	}
}

func TestWriteGuardKeyRequestPasses(t *testing.T) {
	creator := uuid.New()
	key := &auth.APIKey{ID: uuid.New(), CreatedBy: &creator, CreatorRole: "read-only", Scopes: []auth.APIKeyScope{auth.ScopeAll}}
	ctx := context.WithValue(claimsCtx("editor"), apiKeyContextKey, key)
	ctx = context.WithValue(ctx, authTypeContextKey, AuthTypeAPIKey)
	if code, _, reached := runWriteGuard(ctx, http.MethodPost); code != http.StatusNoContent || !reached {
		t.Errorf("key POST = %d reached=%v, want passed to the route's own gate", code, reached)
	}
}

func TestWriteGuardAskedAuthorizerDenies(t *testing.T) {
	authztest.Use(t, authztest.Func(func(context.Context, authz.Subject, authz.Action, authz.Object) (bool, error) {
		return false, nil
	}))
	if code, _, reached := runWriteGuard(claimsCtx("owner"), http.MethodPost); code != http.StatusForbidden || reached {
		t.Errorf("owner POST under deny-all = %d reached=%v, want 403", code, reached)
	}
}
