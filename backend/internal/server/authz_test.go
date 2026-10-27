package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/testutil"
)

// injectRole is a test middleware that stamps claims with the given role into
// the request context, standing in for AuthMiddleware. An empty role simulates
// an unauthenticated request (no claims).
func injectRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if role == "" {
				next.ServeHTTP(w, r)
				return
			}
			claims := &auth.Claims{UserID: uuid.New(), Role: role}
			next.ServeHTTP(w, r.WithContext(middleware.ContextWithClaims(r.Context(), claims)))
		})
	}
}

// mountGate wires a single route exactly as server_core.go does (RequireRole
// gate + handler) behind the role-injecting middleware, and returns the status
// code for a request made as `role`.
func statusFor(t *testing.T, role string, gate func(*httpx.Group)) int {
	t.Helper()
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	{
		r := r.Group("", injectRole(role))
		gate(r)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/cache/flush", nil))
	return rec.Code
}

func TestAuthz_CacheFlush_OwnerOnly(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	gate := func(r *httpx.Group) {
		{
			r := r.Group("", middleware.RequireRole("owner"))
			r.HandleFunc("POST /cache/flush", ok)
		}
	}
	testutil.Equal(t, http.StatusOK, statusFor(t, "owner", gate), "owner may flush")
	testutil.Equal(t, http.StatusForbidden, statusFor(t, "editor", gate), "editor may not flush")
	testutil.Equal(t, http.StatusForbidden, statusFor(t, "viewer", gate), "viewer may not flush")
	testutil.Equal(t, http.StatusForbidden, statusFor(t, "client", gate), "client may not flush")
	testutil.Equal(t, http.StatusUnauthorized, statusFor(t, "", gate), "unauthenticated is 401")
}

func TestAuthz_Users_OwnerOnly(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	// Mirror the /users sub-router gate; hit it via /cache/flush path reuse is
	// avoided by mounting at "/users" and requesting it directly.
	roleStatus := func(role string) int {
		mux := http.NewServeMux()
		r := httpx.NewGroup(mux, "")
		{
			r := r.Group("", injectRole(role))
			{
				r := r.Group("/users", middleware.RequireRole("owner"))
				r.HandleFunc("GET /", ok)
			}
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users", nil))
		return rec.Code
	}
	testutil.Equal(t, http.StatusOK, roleStatus("owner"), "owner may list users")
	testutil.Equal(t, http.StatusForbidden, roleStatus("editor"), "editor may NOT list users (tightened from owner+editor)")
	testutil.Equal(t, http.StatusForbidden, roleStatus("viewer"))
	testutil.Equal(t, http.StatusUnauthorized, roleStatus(""))
}
