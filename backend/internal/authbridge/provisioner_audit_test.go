//go:build integration

package authbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

// provisionerAuditRig is a bridge rig whose provisioner records its events.
type provisionerAuditRig struct {
	*bridgeRig
	prov   *Provisioner
	events []auditstate.Event
}

func newProvisionerAuditRig(t *testing.T) *provisionerAuditRig {
	t.Helper()
	r := &provisionerAuditRig{bridgeRig: newBridgeRig(t)}
	r.prov = NewProvisioner(ProvisionerDeps{
		Users: r.users, Identities: r.idents, Sessions: r.sessions, Keys: r.keys, Tokens: r.tokens,
		AdminPath: func() string { return "/admin" }, Now: time.Now,
		Audit: func(_ context.Context, ev auditstate.Event) { r.events = append(r.events, ev) },
	})
	return r
}

func (r *provisionerAuditRig) only(t *testing.T, action string) auditstate.Event {
	t.Helper()
	var got []auditstate.Event
	for _, ev := range r.events {
		if ev.Action == action {
			got = append(got, ev)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%s events = %d (%+v), want 1", action, len(got), r.events)
	}
	return got[0]
}

// noSecrets fails when any event carries the subject, a token or a path to one.
func (r *provisionerAuditRig) noSecrets(t *testing.T, secrets ...string) {
	t.Helper()
	b, _ := json.Marshal(r.events)
	for _, s := range secrets {
		if strings.Contains(string(b), s) {
			t.Fatalf("event leaks %q: %s", s, b)
		}
	}
	if strings.Contains(string(b), "token") || strings.Contains(string(b), "reset?") {
		t.Fatalf("event leaks a token: %s", b)
	}
}

func TestProvisionerAuditCreateUpdateAndFind(t *testing.T) {
	r := newProvisionerAuditRig(t)
	email := r.email()
	created, isNew, err := r.prov.FindOrCreate(r.ctx, authstate.NewUser{Email: email, Role: "editor"})
	if err != nil || !isNew {
		t.Fatalf("FindOrCreate: %v new=%v", err, isNew)
	}
	uid, _ := uuid.Parse(created.ID)
	r.cleanupUser(t, uid)
	ev := r.only(t, "user.created")
	if ev.Target != email || ev.ActorKind != "system" || ev.Actor != "system (sign-in provider)" ||
		ev.Detail["role"] != "editor" || ev.Detail["via"] != "provider" || len(ev.Detail) != 2 {
		t.Fatalf("user.created = %+v", ev)
	}

	// The find path writes nothing.
	r.events = nil
	if _, isNew, err := r.prov.FindOrCreate(r.ctx, authstate.NewUser{Email: email, Role: "editor"}); err != nil || isNew || len(r.events) != 0 {
		t.Fatalf("find path: err=%v new=%v events=%+v", err, isNew, r.events)
	}

	// Same role: no row; new role: one row with from/to.
	same, owner, name := "editor", "owner", "New Name"
	if _, err := r.prov.Update(r.ctx, created.ID, authstate.Changes{Role: &same}); err != nil || len(r.events) != 0 {
		t.Fatalf("same role: err=%v events=%+v", err, r.events)
	}
	actor := uuid.New()
	ctx := database.WithUser(r.ctx, actor)
	if _, err := r.prov.Update(ctx, created.ID, authstate.Changes{Role: &owner}); err != nil {
		t.Fatal(err)
	}
	ev = r.only(t, "user.role_changed")
	if ev.Target != created.ID || ev.Detail["from"] != "editor" || ev.Detail["to"] != "owner" ||
		ev.ActorKind != "user" || ev.ActorID != actor.String() {
		t.Fatalf("user.role_changed = %+v", ev)
	}
	r.events = nil
	if _, err := r.prov.Update(r.ctx, created.ID, authstate.Changes{Name: &name}); err != nil {
		t.Fatal(err)
	}
	ev = r.only(t, "user.updated")
	if c, _ := ev.Detail["changed"].([]string); len(c) != 1 || c[0] != "name" {
		t.Fatalf("user.updated = %+v", ev)
	}
}

func TestProvisionerAuditAccessIdentityAndLink(t *testing.T) {
	r := newProvisionerAuditRig(t)
	u := r.newUser(t, user.RoleEditor)
	id := u.ID.String()
	r.giveSessionsAndKeys(t, u.ID)

	past := time.Now().Add(-time.Minute)
	if err := r.prov.SetAccessEnd(r.ctx, id, &past); err != nil {
		t.Fatal(err)
	}
	if ev := r.only(t, "user.access_changed"); ev.Target != id || ev.Detail["to"] == nil {
		t.Fatalf("user.access_changed = %+v", ev)
	}
	if ev := r.only(t, "user.sessions_revoked"); ev.Target != id || ev.Detail["keys"] != int64(2) {
		t.Fatalf("user.sessions_revoked = %+v", ev)
	}

	r.events = nil
	const subject = "subject-9f3c-secret"
	if err := r.prov.LinkIdentity(r.ctx, id, "https://issuer.example.test", subject); err != nil {
		t.Fatal(err)
	}
	if ev := r.only(t, "user.identity_linked"); ev.Target != id || ev.Detail["issuer"] != "https://issuer.example.test" || len(ev.Detail) != 1 {
		t.Fatalf("user.identity_linked = %+v", ev)
	}
	if err := r.prov.UnlinkIdentity(r.ctx, id, "https://issuer.example.test", subject); err != nil {
		t.Fatal(err)
	}
	r.only(t, "user.identity_unlinked")

	path, err := r.prov.IssuePasswordSetLink(r.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if ev := r.only(t, "user.reset_link_sent"); ev.Target != id || len(ev.Detail) != 0 {
		t.Fatalf("user.reset_link_sent = %+v", ev)
	}
	r.noSecrets(t, subject, path, strings.TrimPrefix(path, "/admin/reset?token="))

	// A failed change writes nothing.
	r.events = nil
	if err := r.prov.LinkIdentity(r.ctx, "not-a-uuid", "i", "s"); err == nil || len(r.events) != 0 {
		t.Fatalf("bad id: err=%v events=%+v", err, r.events)
	}
}
