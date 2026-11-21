package admin

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
)

// An authorizer that grants user.manage but denies user.owner.grant stops the
// admin create, update and invite paths from making an owner, and still lets
// the same paths make an editor.
func TestAdminOwnerGrantNeedsOwnerGrantAction(t *testing.T) {
	authztest.Use(t, authztest.Func(func(ctx context.Context, s authz.Subject, a authz.Action, o authz.Object) (bool, error) {
		if a == authz.ActionUserOwnerGrant {
			return false, nil
		}
		return authz.Default().Can(ctx, s, a, o)
	}))
	g := newOwnerRig(t, true)
	own := func(role string) url.Values {
		return url.Values{"email": {"new@example.com"}, "name": {"New"}, "password": {"a-long-enough-password"}, "role": {role}}
	}
	editorPath := "/admin/settings/users/" + g.editor.ID.String()

	for name, c := range map[string]struct{ path string }{
		"create": {"/admin/settings/users"},
		"invite": {"/admin/settings/users/invite"},
		"update": {editorPath},
	} {
		if rec := g.do("POST", c.path, "owner-token", own("owner"), nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s as owner: %d, want 403\n%s", name, rec.Code, rec.Body.String())
		}
	}
	if got := g.editor.Role; got != "editor" {
		t.Errorf("editor role became %q", got)
	}
	if rec := g.do("POST", "/admin/settings/users/invite", "owner-token", own("editor"), nil); rec.Code != http.StatusSeeOther {
		t.Errorf("invite as editor: %d, want 303\n%s", rec.Code, rec.Body.String())
	}
}
