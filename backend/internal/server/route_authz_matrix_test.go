package server

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/middleware"
)

// matrixKeys maps the raw X-API-Key value to a fixture key, so the matrix
// needs no database.
type matrixKeys map[string]*auth.APIKey

func (m matrixKeys) ValidateKey(_ context.Context, raw string) (*auth.APIKey, error) {
	if k, ok := m[raw]; ok {
		return k, nil
	}
	return nil, auth.ErrInvalidToken
}

func (matrixKeys) RecordUsage(context.Context, uuid.UUID, string) error { return nil }

// matrixKey builds a fixture key created by a user with the given role.
func matrixKey(creatorRole string, scopes ...auth.APIKeyScope) *auth.APIKey {
	creator := uuid.New()
	return &auth.APIKey{ID: uuid.New(), Name: "matrix", Scopes: scopes, CreatedBy: &creator, CreatorRole: creatorRole, RateLimit: 1000}
}

// matrixCell renders one response: "reached" when the core handler died on the
// shell's nil collaborators, else the status plus the JSON error text.
func matrixCell(code int, body string) string {
	if code == 0 {
		return "reached"
	}
	cell := strconv.Itoa(code)
	var e struct {
		Error string `json:"error"`
	}
	if jsonx.Unmarshal([]byte(body), &e) == nil && e.Error != "" {
		cell += ":" + strings.ReplaceAll(e.Error, " ", "_")
	}
	return cell
}

// routeAuthzMatrix serves every protected /api/v1 route as each principal and
// returns one sorted line per route. The chain mirrors server.go (key or
// session auth, then the protected mounts) without CSRF and the demo guard.
func routeAuthzMatrix(t *testing.T) []string {
	t.Helper()
	return routeAuthzMatrixFor(t, []principal{
		{"anon", nil},
		{"editor", []string{"Authorization", "Bearer test-session-editor"}},
		{"owner", []string{"Authorization", "Bearer test-session-owner"}},
		{"key-read-ed", []string{"X-API-Key", "key-read-ed"}},
		{"key-all-ed", []string{"X-API-Key", "key-all-ed"}},
		{"key-all-own", []string{"X-API-Key", "key-all-own"}},
	})
}

// principal is one matrix column: a name and the request headers (name, value
// pairs) it sends.
type principal struct {
	name string
	hdr  []string
}

// routeAuthzMatrixFor is routeAuthzMatrix over any list of principals.
func routeAuthzMatrixFor(t *testing.T, principals []principal) []string {
	t.Helper()
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	s.hooks = hooks.New(s.logger)
	keys := matrixKeys{
		"key-read-ed": matrixKey("editor", auth.ScopeReadEntities),
		"key-all-ed":  matrixKey("editor", auth.ScopeAll),
		"key-all-own": matrixKey("owner", auth.ScopeAll),
	}
	mux := http.NewServeMux()
	g := httpx.NewGroup(mux, "")
	r := g.Group("/api/v1", middleware.AuthOrAPIKey(keys, allowAll{}, middleware.AuthMiddleware(fakeSession, fakeAPISession, false)))
	s.mountCoreProtectedRoutes(r)

	routes := g.Routes()
	has := map[string]bool{}
	for _, rt := range routes {
		has[rt.Pattern] = true
	}
	var lines []string
	for _, rt := range routes {
		method, path, _ := strings.Cut(rt.Pattern, " ")
		// "/x" next to "/x/{$}" is the same endpoint; keep the {$} form.
		if has[method+" "+path+"/{$}"] {
			continue
		}
		url := strings.ReplaceAll(path, "{$}", "")
		url = strings.ReplaceAll(url, "{rest...}", "a/b")
		url = paramRe.ReplaceAllString(url, "0b6f6a52-6c1e-4d3b-9a43-2f1f5e7c8d90")
		line := method + " " + path
		for _, p := range principals {
			code, body := hit(mux, method, url, p.hdr...)
			line += " " + p.name + "=" + matrixCell(code, body)
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return lines
}

// TestRouteAuthzMatrixGolden pins the status of every protected /api/v1 route
// for an anonymous caller, an editor and an owner session, and three API keys.
// A gate refactor must leave the file untouched. Regenerate with
// UPDATE_GOLDEN=1 only for a deliberate behaviour change.
func TestRouteAuthzMatrixGolden(t *testing.T) {
	lines := routeAuthzMatrix(t)
	got := strings.Join(lines, "\n") + "\n"
	if again := strings.Join(routeAuthzMatrix(t), "\n") + "\n"; again != got {
		t.Fatal("matrix is not deterministic across two runs")
	}

	gated := 0
	for _, l := range lines {
		if strings.Contains(l, " editor=403") && !strings.Contains(l, " owner=403") {
			gated++
		}
	}
	if gated < 8 {
		t.Fatalf("only %d routes reject the editor but not the owner; the gates were not reached", gated)
	}

	const golden = "testdata/route_authz.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got == string(want) {
		return
	}
	inWant := map[string]bool{}
	wl := strings.Split(string(want), "\n")
	for _, l := range wl {
		inWant[l] = true
	}
	inGot := map[string]bool{}
	for _, l := range strings.Split(got, "\n") {
		inGot[l] = true
		if !inWant[l] {
			t.Errorf("new:     %s", l)
		}
	}
	for _, l := range wl {
		if !inGot[l] {
			t.Errorf("missing: %s", l)
		}
	}
}
