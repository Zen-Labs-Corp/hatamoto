package admin

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
)

func newSetupPendingRig(t *testing.T) *rig {
	t.Helper()
	return newRig(t, func(d *Deps) {
		d.Site = &fakeSite{cfg: newFakeSite("Acme Site").cfg}
		d.Site.(*fakeSite).cfg.SetupCompletedAt = nil
	})
}

func TestShellNoticesFollowAuthz(t *testing.T) {
	const block = `class="hm-notices"`
	const setup = "Setup is not finished"

	g := newSetupPendingRig(t)
	body := g.do("GET", "/admin/profile", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, block) || !strings.Contains(body, setup) {
		t.Fatalf("owner page lacks the setup notice (block %v)", strings.Contains(body, block))
	}
	if body := g.do("GET", "/admin/profile", "editor-token", nil, nil).Body.String(); strings.Contains(body, block) {
		t.Error("editor sees the notices block")
	}

	// Deny only settings.manage: the page still renders, without the notices.
	authztest.Use(t, authztest.Func(func(ctx context.Context, s authz.Subject, a authz.Action, o authz.Object) (bool, error) {
		if a == authz.ActionSettingsManage {
			return false, nil
		}
		return authz.Default().Can(ctx, s, a, o)
	}))
	rec := g.do("GET", "/admin/profile", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hm-page") {
		t.Fatalf("owner page = %d, want a rendered page", rec.Code)
	}
	if strings.Contains(rec.Body.String(), block) {
		t.Error("owner page has the notices block without settings.manage")
	}
}
