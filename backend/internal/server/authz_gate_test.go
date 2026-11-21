package server

import (
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/authztest"
)

// The gates of the REST API ask authz, so a registered authorizer overrides
// the built-in role rules.
func TestRESTGatesAskAuthz(t *testing.T) {
	h, s := bootRoutes(t)
	auth := ownerToken(t, s)
	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/cache/flush"},
		{http.MethodGet, "/api/v1/users/"},
		{http.MethodGet, "/api/v1/settings/seo/"},
	}

	t.Run("deny-all", func(t *testing.T) {
		authztest.Use(t, authztest.DenyAll)
		for _, r := range routes {
			code, body := hit(h, r.method, r.path, "Authorization", auth)
			if code != http.StatusForbidden || strings.TrimSpace(body) != `{"error":"forbidden"}` {
				t.Errorf("%s %s under deny-all: %d %q, want 403 forbidden", r.method, r.path, code, body)
			}
		}
	})

	// the deny-all registration is gone after the subtest's cleanup

	for _, r := range routes {
		if code, _ := hit(h, r.method, r.path, "Authorization", auth); code == http.StatusForbidden {
			t.Errorf("%s %s with the default authorizer: owner got 403", r.method, r.path)
		}
	}
}
