package middleware

import (
	"net/http"
	"slices"
	"testing"

	"hatamoto.dev/core/internal/auth"
)

const rolesEntityID = "6f1b1d52-8b3e-4a52-9c2f-0a1b2c3d4e5f"

// A key never outgrows its creator: a `*` key created by a role that may not
// write is read-only, a content role's key reaches only what that role may do
// (with the zero Object), and an editor's key is unchanged.
func TestAPIKeyCreatorRolesCeiling(t *testing.T) {
	cases := []struct {
		method, path string
		allowed      []string // creator roles that reach the handler
	}{
		{"GET", "/api/v1/entities", []string{"editor", "author", "contributor", "reviewer", "read-only"}},
		{"POST", "/api/v1/entities", []string{"editor", "author", "contributor"}},
		{"PATCH", "/api/v1/entities/" + rolesEntityID + "/status", []string{"editor", "author", "reviewer"}},
		{"POST", "/api/v1/media/upload", []string{"editor", "author", "contributor", "reviewer"}},
	}
	for _, role := range []string{"editor", "author", "contributor", "reviewer", "read-only"} {
		key := testKey(auth.ScopeAll)
		key.CreatorRole = role
		h := newKeyHarness(t, true, map[string]*auth.APIKey{"all": key})
		for _, c := range cases {
			want := http.StatusForbidden
			if slices.Contains(c.allowed, role) {
				want = http.StatusOK
			}
			before := h.reachedKey.Load()
			if rec := do(h.handler, c.method, c.path, "all"); rec.Code != want {
				t.Errorf("%s key (creator %s): %s %s = %d, want %d", "*", role, c.method, c.path, rec.Code, want)
			}
			if reached := h.reachedKey.Load() != before; reached != (want == http.StatusOK) {
				t.Errorf("creator %s: %s %s handler reached = %v, want %v", role, c.method, c.path, reached, want == http.StatusOK)
			}
		}
	}
}
