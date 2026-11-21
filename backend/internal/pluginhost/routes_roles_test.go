package pluginhost

import (
	"net/http"
	"testing"

	"hatamoto.dev/sdk"
)

// Not parallel: the authorizer registration is process-wide.

// The built-in roles never reach plugin routes: the manifest role list (owner
// when empty) refuses them before the authorizer is asked.
func TestPluginRoutesRefuseBuiltinRoles(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, func(m *sdk.Manifest) {
		m.Routes = append(m.Routes, sdk.Route{Method: "GET", Path: "/editors", Roles: []string{"editor"}})
	})
	for _, role := range []string{"author", "contributor", "reviewer", "read-only"} {
		for _, rest := range []string{"echo", "editors"} {
			if c := serveAdmin(r, "GET", rest, &sdk.HTTPUser{ID: "u1", Role: role}).Code; c != http.StatusForbidden {
				t.Errorf("%s on %s = %d, want 403", role, rest, c)
			}
		}
	}
	// Control: the editor reaches the editors-only route.
	if c := serveAdmin(r, "GET", "editors", &sdk.HTTPUser{ID: "u1", Role: "editor"}).Code; c == http.StatusForbidden {
		t.Errorf("editor on editors = %d, want reached", c)
	}
}
