package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
)

type routeKeyValidator struct{ key *auth.APIKey }

func (v routeKeyValidator) ValidateKey(context.Context, string) (*auth.APIKey, error) {
	return v.key, nil
}
func (routeKeyValidator) RecordUsage(context.Context, uuid.UUID, string) error { return nil }

type allowAll struct{}

func (allowAll) Allow(uuid.UUID, int) bool { return true }

var paramRe = regexp.MustCompile(`\{[^}$]*\}`)

// protectedRoutes returns every METHOD+pattern the protected /api/v1 group
// mounts without needing a database: core protected routes plus the render
// admin routes (normally only mounted when a render engine is configured).
func protectedRoutes(t *testing.T) [][2]string {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{logger: logger}
	mux := http.NewServeMux()
	inner := httpx.NewGroup(mux, "")
	{
		r := inner.Group("/api/v1")
		s.mountCoreProtectedRoutes(r)
		mountRenderAdmin(r, &renderAdminHandlers{})
	}
	var routes [][2]string
	all := inner.Routes()
	has := map[string]bool{}
	for _, rt := range all {
		has[rt.Pattern] = true
	}
	for _, rt := range all {
		method, path, _ := strings.Cut(rt.Pattern, " ")
		// "/x" next to "/x/{$}" is the same endpoint reachable without the
		// trailing slash; the {$} form carries the chi-style identity.
		if has[method+" "+path+"/{$}"] {
			continue
		}
		routes = append(routes, [2]string{method, path})
	}
	if len(routes) < 50 {
		t.Fatalf("expected the full protected route table, walked only %d routes", len(routes))
	}
	return routes
}

// TestEveryProtectedRouteDeniedForKeyUnlessAllowlisted mounts a stub for every
// real protected route behind AuthOrAPIKey with a "*" key and asserts that only
// the intended allowlist is reachable (ISC-A-2: default deny).
func TestEveryProtectedRouteDeniedForKeyUnlessAllowlisted(t *testing.T) {
	creator := uuid.New()
	key := &auth.APIKey{ID: uuid.New(), Name: "all", Scopes: []auth.APIKeyScope{auth.ScopeAll}, CreatedBy: &creator, CreatorRole: "editor", RateLimit: 1000}

	sessionHit := false
	session := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sessionHit = true })
	}
	mux := http.NewServeMux()
	outer := httpx.NewGroup(mux, "")
	routes := protectedRoutes(t)
	for _, rt := range routes {
		outer.Group("", middleware.AuthOrAPIKey(routeKeyValidator{key}, allowAll{}, session)).
			HandleFunc(rt[0]+" "+rt[1], func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	}

	want := map[string]bool{
		"GET /api/v1/entities/":                        true,
		"GET /api/v1/entities/{id}":                    true,
		"GET /api/v1/entities/{id}/versions":           true,
		"GET /api/v1/entities/{id}/versions/{version}": true,
		"GET /api/v1/render/theme":                     true,
		"POST /api/v1/render/preview":                  true,
		"POST /api/v1/entities/":                       true, // create (write:entities)
		"PATCH /api/v1/entities/{id}/status":           true, // publish (write:entities)
		"PUT /api/v1/entities/{id}":                    true,
		"PATCH /api/v1/entities/{id}":                  true,
		"POST /api/v1/entities/{id}/restore/{version}": true,
		"POST /api/v1/linking/suggest/{entity_id}":     true, // non-mutating suggestions (read:entities)
		"POST /api/v1/linking/insert":                  true, // wrap one anchor (write:entities)
	}

	seen := map[string]bool{}
	for _, rt := range routes {
		id := rt[0] + " " + strings.ReplaceAll(strings.ReplaceAll(rt[1], "{$}", ""), "/{rest...}", "")
		seen[id] = true
		url := paramRe.ReplaceAllString(strings.ReplaceAll(rt[1], "{$}", ""), "x")
		req := httptest.NewRequest(rt[0], url, nil)
		req.Header.Set("X-API-Key", "nxk_test")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		allowed := rec.Code == http.StatusOK
		if allowed != want[id] {
			t.Errorf("%s (as %s): reachable=%v, want %v (status %d)", id, url, allowed, want[id], rec.Code)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("allowlisted route %q is not a real protected route: allowlist and router drifted", id)
		}
	}
	if sessionHit {
		t.Error("key requests must not fall through to session auth")
	}
}

func TestSessionPathUnchanged(t *testing.T) {
	sessionHit := false
	session := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sessionHit = true
			next.ServeHTTP(w, r)
		})
	}
	h := middleware.AuthOrAPIKey(routeKeyValidator{}, allowAll{}, session)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !sessionHit || rec.Code != http.StatusTeapot {
		t.Fatalf("session request must reach the regular auth chain untouched: hit=%v code=%d", sessionHit, rec.Code)
	}
}

func TestChangeSummaryPrefix(t *testing.T) {
	user := "tweak hero"
	empty := ""
	cases := []struct {
		name string
		in   *string
		want string
	}{
		{"with summary", &user, "[api-key:tool] tweak hero"},
		{"nil", nil, "[api-key:tool]"},
		{"empty", &empty, "[api-key:tool]"},
	}
	for _, c := range cases {
		if got := *apiKeySummary("tool", c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
