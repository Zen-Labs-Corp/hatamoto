package admin

import (
	"net/url"
	"testing"

	"hatamoto.dev/core/audit"
)

const usersAuditPassword = "users-audit-password-1"

// usersAuditOne returns the single captured event after a request.
func usersAuditOne(t *testing.T, read func() []AuditEvent, action, target string) AuditEvent {
	t.Helper()
	evs := read()
	if len(evs) != 1 {
		t.Fatalf("events = %+v, want exactly one %s", evs, action)
	}
	assertAuditSafe(t, evs, usersAuditPassword, "invite-token-")
	ev := evs[0]
	if ev.Action != action || ev.Target != target || ev.Actor != "owner@example.com" {
		t.Fatalf("row = %+v, want %s on %s by the owner", ev, action, target)
	}
	return ev
}

func TestUsersAuditInviteLifecycle(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	read := auditCapture(g.a)

	if rec := g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {"not an address"}, "role": {"editor"}}, nil); rec.Code != 422 {
		t.Fatalf("invalid invite: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed invite audited: %+v", evs)
	}

	if rec := g.do("POST", "/admin/settings/users/invite", "owner-token", inviteForm("new@example.com"), nil); rec.Code != 303 {
		t.Fatalf("invite: %d", rec.Code)
	}
	ev := usersAuditOne(t, read, AuditUserInvited, "new@example.com")
	if ev.Detail["role"] != "editor" {
		t.Fatalf("detail = %v", ev.Detail)
	}
	var id string
	for _, inv := range g.tokens.invites {
		id = inv.ID.String()
	}

	g.do("POST", "/admin/settings/users/invites/"+id+"/resend", "owner-token", url.Values{}, nil)
	evs := read()
	if len(evs) != 2 || evs[1].Action != AuditUserInviteResent || evs[1].Target != "new@example.com" || evs[1].Detail["role"] != "editor" {
		t.Fatalf("resend events = %+v", evs)
	}
	assertAuditSafe(t, evs, "invite-token-")

	for _, inv := range g.tokens.invites {
		id = inv.ID.String()
	}
	g.do("POST", "/admin/settings/users/invites/"+id+"/revoke", "owner-token", url.Values{}, nil)
	evs = read()
	if len(evs) != 3 || evs[2].Action != AuditUserInviteRevoked || evs[2].Target != id {
		t.Fatalf("revoke events = %+v", evs)
	}
}

func TestUsersAuditInviteAcceptedUsesNewUserAsActor(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	g.do("POST", "/admin/settings/users/invite", "owner-token", inviteForm("new@example.com"), nil)
	var token string
	for tok := range g.tokens.invites {
		token = tok
	}
	read := auditCapture(g.a)

	form := url.Values{"token": {token}, "name": {"Newbie"}, "password": {"short"}, "confirm": {"short"}}
	if rec := g.do("POST", "/admin/accept", "", form, nil); rec.Code != 422 {
		t.Fatalf("short password: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("refused accept audited: %+v", evs)
	}

	form.Set("password", usersAuditPassword)
	form.Set("confirm", usersAuditPassword)
	g.do("POST", "/admin/accept", "", form, nil)
	evs := read()
	assertAuditSafe(t, evs, usersAuditPassword, token)
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	ev := evs[0]
	if ev.Action != AuditUserInviteAccepted || ev.Target != "new@example.com" || ev.Actor != "new@example.com" || ev.ActorID == "" ||
		ev.ActorKind != audit.KindUser || ev.Detail["role"] != "editor" {
		t.Fatalf("row = %+v", ev)
	}
}

func TestUsersAuditCreateUpdateResetDelete(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	read := auditCapture(g.a)

	bad := url.Values{"email": {"made@example.com"}, "name": {"Made"}, "role": {"editor"}, "password": {"x"}}
	if rec := g.do("POST", "/admin/settings/users", "owner-token", bad, nil); rec.Code != 422 {
		t.Fatalf("short password: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed create audited: %+v", evs)
	}

	good := url.Values{"email": {"made@example.com"}, "name": {"Made"}, "role": {"editor"}, "password": {usersAuditPassword}}
	if rec := g.do("POST", "/admin/settings/users", "owner-token", good, nil); rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	if ev := usersAuditOne(t, read, AuditUserCreated, "made@example.com"); ev.Detail["role"] != "editor" {
		t.Fatalf("detail = %v", ev.Detail)
	}

	base := "/admin/settings/users/" + g.editor.ID.String()
	// A name-only edit writes no row.
	g.do("POST", base, "owner-token", url.Values{"name": {"Ed"}, "role": {"editor"}}, nil)
	if evs := read(); len(evs) != 1 {
		t.Fatalf("name-only edit audited: %+v", evs)
	}
	if rec := g.do("POST", base, "owner-token", url.Values{"name": {"Ed"}, "role": {"nope"}}, nil); rec.Code != 422 {
		t.Fatalf("bad role: %d", rec.Code)
	}
	if evs := read(); len(evs) != 1 {
		t.Fatalf("failed update audited: %+v", evs)
	}
	g.do("POST", base, "owner-token", url.Values{"name": {"Ed"}, "role": {"owner"}}, nil)
	evs := read()
	if len(evs) != 2 || evs[1].Action != AuditUserRoleChanged || evs[1].Target != "ed@example.com" ||
		evs[1].Detail["from"] != "editor" || evs[1].Detail["to"] != "owner" {
		t.Fatalf("role events = %+v", evs)
	}

	g.do("POST", base+"/reset", "owner-token", url.Values{}, nil)
	evs = read()
	if len(evs) != 3 || evs[2].Action != AuditUserResetLinkSent || evs[2].Target != "ed@example.com" {
		t.Fatalf("reset events = %+v", evs)
	}

	g.do("POST", base+"/delete", "owner-token", url.Values{}, nil)
	evs = read()
	if len(evs) != 4 || evs[3].Action != AuditUserDeleted || evs[3].Target != "ed@example.com" || evs[3].Detail["role"] != "owner" {
		t.Fatalf("delete events = %+v", evs)
	}
	assertAuditSafe(t, evs, usersAuditPassword, "reset-token")
	for _, ev := range evs {
		if ev.Actor != "owner@example.com" {
			t.Errorf("%s actor = %q", ev.Action, ev.Actor)
		}
	}
}
