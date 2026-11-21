package admin

import (
	"html"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/user"
)

func TestInviteAndCreateAcceptEveryRole(t *testing.T) {
	t.Parallel()

	for _, role := range user.Roles() {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			g := newOwnerRig(t, true)

			addr := string(role) + "@example.com"
			rec := g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {addr}, "role": {string(role)}}, nil)
			if rec.Code != 303 {
				t.Fatalf("invite = %d %s", rec.Code, rec.Body)
			}
			g.tokens.mu.Lock()
			var got user.Role
			for _, inv := range g.tokens.invites {
				if inv.Email == addr {
					got = inv.Role
				}
			}
			g.tokens.mu.Unlock()
			if got != role {
				t.Errorf("invited role = %q, want %q", got, role)
			}
			g.mail.mu.Lock()
			mailed := len(g.mail.invites) == 1 && g.mail.invites[0].Role == string(role)
			g.mail.mu.Unlock()
			if !mailed {
				t.Errorf("the invite mail does not carry role %q", role)
			}

			if role == user.RoleOwner {
				return // a granted owner needs the owner-grant right; covered by the existing owner tests
			}
			rec = g.do("POST", "/admin/settings/users", "owner-token", url.Values{"email": {"pw-" + addr}, "name": {"Pat"}, "role": {string(role)}, "password": {"a-long-enough-password-1"}}, nil)
			if rec.Code != 303 {
				t.Fatalf("create = %d %s", rec.Code, rec.Body)
			}
			created := false
			for _, u := range g.mgr.users {
				created = created || (u.Email == "pw-"+addr && u.Role == role)
			}
			if !created {
				t.Errorf("no user created with role %q", role)
			}
		})
	}
}

func TestUsersListFilterByNewRole(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	ro := &user.User{ID: uuid.New(), Email: "readonly-person@example.com", Role: user.RoleReadOnly}
	g.mgr.users[ro.ID] = ro

	body := g.do("GET", "/admin/settings/users?role=read-only", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "readonly-person@example.com") || strings.Contains(body, "ed@example.com") {
		t.Errorf("filter role=read-only did not narrow the list")
	}
	if !strings.Contains(body, "Read-only") {
		t.Error("the badge does not read Read-only")
	}
}

func TestInviteDialogListsSixRoles(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	body := g.do("GET", "/admin/settings/users", "owner-token", nil, nil).Body.String()
	i := strings.Index(body, `id="invite-role"`)
	if i < 0 {
		t.Fatal("no invite role select")
	}
	sel := body[i:]
	sel = sel[:strings.Index(sel, "</select>")]
	if n := strings.Count(sel, "<option"); n != 6 {
		t.Errorf("%d options, want 6", n)
	}
	for _, r := range user.Roles() {
		if want := html.EscapeString(r.Label() + ": " + roleDescriptions[r]); !strings.Contains(sel, want) {
			t.Errorf("select lacks %q", want)
		}
	}
	if n := strings.Count(sel, " selected"); n != 1 {
		t.Errorf("%d selected options, want 1", n)
	}
}

func TestSignInOIDCRoleChoices(t *testing.T) {
	t.Parallel()

	g := newOIDCRig(t, savedOIDCView())
	g.grant()
	if rec := g.do("POST", "/admin/settings/users/sign-in/oidc", "owner-token", oidcForm("role", "read-only"), nil); rec.Code != 303 {
		t.Errorf("role=read-only = %d, want 303", rec.Code)
	}
	rec := g.do("POST", "/admin/settings/users/sign-in/oidc", "owner-token", oidcForm("role", "viewer"), nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose one of the listed roles") {
		t.Errorf("role=viewer = %d, want 422 with the role error", rec.Code)
	}
}
