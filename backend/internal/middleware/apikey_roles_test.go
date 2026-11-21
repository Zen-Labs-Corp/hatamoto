package middleware

import (
	"net/http"
	"testing"

	"hatamoto.dev/core/internal/auth"
)

const rolesEntityID = "6f1b1d52-8b3e-4a52-9c2f-0a1b2c3d4e5f"

// A key never outgrows its creator: a `*` key created by a role that may not
// write is read-only, and an editor's key is unchanged.
func TestAPIKeyCreatorRolesCeiling(t *testing.T) {
	cases := []struct {
		method, path string
		writes       bool
	}{
		{"GET", "/api/v1/entities", false},
		{"POST", "/api/v1/entities", true},
		{"PATCH", "/api/v1/entities/" + rolesEntityID + "/status", true},
		{"POST", "/api/v1/media/upload", true},
	}
	for _, role := range []string{"editor", "author", "contributor", "reviewer", "read-only"} {
		key := testKey(auth.ScopeAll)
		key.CreatorRole = role
		h := newKeyHarness(t, true, map[string]*auth.APIKey{"all": key})
		for _, c := range cases {
			want := http.StatusOK
			if c.writes && role != "editor" {
				want = http.StatusForbidden
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
