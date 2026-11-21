package admin

import (
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
)

// gateOnStub serves a stub behind RequireSession and RequireCan(act).
func gateOnStub(g *rig, act authz.Action) http.Handler {
	h := g.a.RequireSession(g.a.RequireCan(act)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	mux := http.NewServeMux()
	mux.Handle("GET /x", g.a.guardFor(scopeAdmin)(secureHeaders(h)))
	return mux
}

// A registered authorizer that refuses everything blocks even the owner, with
// the same 403 page or fetch error as a role refusal. Not parallel: the
// registration is process-wide.
func TestAdminAuthzDenyAll(t *testing.T) {
	authztest.Use(t, authztest.DenyAll)
	g := newRig(t)
	h := gateOnStub(g, authz.ActionUserManage)

	rec := serve(h, httptestReq("GET", "/x", "owner-token"), nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Not allowed") {
		t.Errorf("owner page: %d %q", rec.Code, rec.Body.String())
	}

	req := httptestReq("GET", "/x", "owner-token")
	req.Header.Set("X-Hm-Fetch", "1")
	rec = serve(h, req, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
		t.Errorf("owner fetch: %d %q", rec.Code, rec.Body.String())
	}
}

// The gate asks with the session subject and the action it was mounted with.
func TestAdminAuthzAsksWithSessionSubject(t *testing.T) {
	rc := &authztest.Recorder{}
	authztest.Use(t, rc)
	g := newRig(t)
	h := gateOnStub(g, authz.ActionSettingsManage)

	if rec := serve(h, httptestReq("GET", "/x", "owner-token"), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("owner: %d", rec.Code)
	}
	calls := rc.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.Action != authz.ActionSettingsManage || c.Subject.UserID != g.owner.ID.String() || c.Subject.Role != "owner" || c.Subject.KeyID != "" {
		t.Errorf("call = %+v", c)
	}
}
