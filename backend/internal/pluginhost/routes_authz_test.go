package pluginhost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/sdk"
)

// Not parallel: the authorizer registration is process-wide.

func serveAdmin(r *capaRig, method, rest string, user *sdk.HTTPUser) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.rt.AdminRoutes().Serve(rec, httptest.NewRequest(method, "/x/"+rest, nil), "capa", rest, user)
	return rec
}

func TestAdminRouteDeniedByAuthorizer(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	authztest.Use(t, authztest.DenyAll)

	rec := serveAdmin(r, "GET", "echo", &sdk.HTTPUser{ID: "u1", Role: "owner"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"forbidden"`) {
		t.Fatalf("owner under deny-all = %d %q", rec.Code, rec.Body)
	}

	// Public routes carry no check.
	pub := httptest.NewRecorder()
	r.rt.PublicRoutes().Serve(pub, httptest.NewRequest("GET", "/x/who/ada", nil), "capa", "who/ada", nil)
	if pub.Code != 200 || pub.Body.String() != "ada" {
		t.Fatalf("public route under deny-all = %d %q", pub.Code, pub.Body)
	}
}

func TestAdminRouteAuthorizerError(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	authztest.Use(t, authztest.Func(func(_ context.Context, _ authz.Subject, _ authz.Action, _ authz.Object) (bool, error) {
		return false, errors.New("backend down")
	}))
	if c := serveAdmin(r, "GET", "echo", &sdk.HTTPUser{ID: "u1", Role: "owner"}).Code; c != http.StatusForbidden {
		t.Fatalf("authorizer error = %d, want 403", c)
	}
}

func TestAdminRouteAsksAuthorizerAfterManifestRoles(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, func(m *sdk.Manifest) {
		m.Routes = append(m.Routes, sdk.Route{Method: "GET", Path: "/editors", Roles: []string{"editor"}})
	})
	rec := &authztest.Recorder{}
	authztest.Use(t, rec)

	if c := serveAdmin(r, "GET", "echo", &sdk.HTTPUser{ID: "u1", Role: "owner"}).Code; c != 201 {
		t.Fatalf("owner on echo = %d", c)
	}
	calls := rec.Calls()
	if len(calls) != 1 || calls[0].Action != authz.ActionPluginRouteInvoke ||
		calls[0].Subject.UserID != "u1" || calls[0].Subject.Role != "owner" || calls[0].Subject.KeyID != "" ||
		calls[0].Object.Kind != "plugin" || calls[0].Object.ID != "capa" {
		t.Fatalf("authorizer asked %+v", calls)
	}

	// The manifest list is kept: an owner is refused on an editors-only route
	// before authz is consulted, even though the default would allow it.
	if c := serveAdmin(r, "GET", "editors", &sdk.HTTPUser{ID: "u1", Role: "owner"}).Code; c != http.StatusForbidden {
		t.Fatalf("owner on an editors-only route = %d", c)
	}
	if n := len(rec.Calls()); n != 1 {
		t.Fatalf("authorizer asked %d times, want 1 (manifest check first)", n)
	}
}
