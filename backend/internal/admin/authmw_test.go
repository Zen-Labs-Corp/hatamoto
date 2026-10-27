package admin

import (
	"context"
	"net/http"
	"testing"

	"hatamoto.dev/core/internal/session"
)

// countingSessions counts store calls around a real fake store.
type countingSessions struct {
	SessionStore
	validates int
}

func (c *countingSessions) Validate(ctx context.Context, token string) (*session.Session, error) {
	c.validates++
	return c.SessionStore.Validate(ctx, token)
}

func countedRig(t *testing.T) (*rig, *countingSessions) {
	t.Helper()
	g := newRig(t)
	cs := &countingSessions{SessionStore: g.a.deps.Sessions}
	g.a.deps.Sessions = cs
	return g, cs
}

func TestRequireSessionReusesGuardAuth(t *testing.T) {
	t.Parallel()

	g, cs := countedRig(t)
	if rec := g.do("GET", "/admin/profile", "owner-token", nil, nil); rec.Code != 200 {
		t.Fatalf("guarded request: %d", rec.Code)
	}
	if cs.validates != 1 {
		t.Errorf("guarded request validated the session %d times, want 1", cs.validates)
	}

	// Mounted alone (no guard in front), RequireSession authenticates itself.
	cs.validates = 0
	h := g.a.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if rec := serve(h, httptestReq("GET", "/x", "owner-token"), nil); rec.Code != 204 || cs.validates != 1 {
		t.Errorf("direct mount: %d after %d validations, want 204 after 1", rec.Code, cs.validates)
	}
}

func TestIdleSessionRevokedOnce(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	fs := g.a.deps.Sessions.(*fakeSessions)
	if rec := g.do("GET", "/admin/profile", "idle-token", nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("idle session: %d, want a redirect to sign in", rec.Code)
	}
	if len(fs.revoked) != 1 {
		t.Errorf("idle session revoked %d times, want 1", len(fs.revoked))
	}
}
