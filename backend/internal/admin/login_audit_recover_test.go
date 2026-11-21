package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/user"
)

const recoverAuditPassword = "a-long-enough-pw-1"

func recoverAuditActions(evs []AuditEvent) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Action)
	}
	return out
}

func TestRecoverAuditResetLinkWritesResetAndSignIn(t *testing.T) {
	t.Parallel()

	g, read := newLoginAuditAuthRig(t)
	tok, _ := g.tokens.IssueReset(context.Background(), uuid.Nil)
	form := url.Values{"token": {tok}, "password": {recoverAuditPassword}, "confirm": {recoverAuditPassword}}
	if rec := g.do("POST", "/admin/reset", "", form, nil); rec.Code != 303 {
		t.Fatalf("reset: %d", rec.Code)
	}
	evs := read()
	if got := recoverAuditActions(evs); len(got) != 2 || got[0] != AuditUserPasswordReset || got[1] != AuditSignIn {
		t.Fatalf("rows = %+v", evs)
	}
	if evs[0].Actor != "olga@example.com" || evs[0].Target != "olga@example.com" || evs[1].Detail["method"] != SignInReset {
		t.Fatalf("rows = %+v", evs)
	}
	assertAuditSafe(t, evs, recoverAuditPassword, tok)

	// A reused link changes nothing and writes nothing.
	if rec := g.do("POST", "/admin/reset", "", form, nil); rec.Code != 200 {
		t.Fatalf("reused: %d", rec.Code)
	}
	if n := len(read()); n != 2 {
		t.Fatalf("a dead link wrote rows: %d", n)
	}
}

func TestRecoverAuditInviteWritesSignIn(t *testing.T) {
	t.Parallel()

	g, read := newLoginAuditAuthRig(t)
	g.tokens.invites["inv-1"] = &auth.Invite{Email: "new@example.com", Role: user.RoleEditor}
	form := url.Values{"token": {"inv-1"}, "name": {"Nia"}, "password": {recoverAuditPassword}, "confirm": {recoverAuditPassword}}
	if rec := g.do("POST", "/admin/accept", "", form, nil); rec.Code != 303 {
		t.Fatalf("accept: %d", rec.Code)
	}
	evs := read()
	if got := recoverAuditActions(evs); len(got) != 2 || got[0] != AuditUserInviteAccepted || got[1] != AuditSignIn {
		t.Fatalf("rows = %+v", evs)
	}
	if evs[1].Actor != "new@example.com" || evs[1].Detail["method"] != SignInInvite || strings.Contains(evs[1].Actor, "-") {
		t.Fatalf("sign-in row = %+v", evs[1])
	}
}

func TestRecoverAuditPasswordChange(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	read := auditCapture(g.a)
	wrong := url.Values{"current": {"nope"}, "password": {recoverAuditPassword}, "confirm": {recoverAuditPassword}}
	if rec := g.do("POST", "/admin/profile/password", "editor-token", wrong, nil); rec.Code != 422 {
		t.Fatalf("wrong current: %d", rec.Code)
	}
	if n := len(read()); n != 0 {
		t.Fatalf("a refused change wrote rows: %d", n)
	}
	ok := url.Values{"current": {"current-password-1"}, "password": {recoverAuditPassword}, "confirm": {recoverAuditPassword}}
	if rec := g.do("POST", "/admin/profile/password", "editor-token", ok, nil); rec.Code != 303 {
		t.Fatalf("change: %d", rec.Code)
	}
	evs := read()
	if len(evs) != 1 || evs[0].Action != AuditUserPasswordChanged || evs[0].Actor != g.editor.Email || evs[0].Target != g.editor.Email {
		t.Fatalf("rows = %+v", evs)
	}
	assertAuditSafe(t, evs, "current-password-1", recoverAuditPassword)
}
