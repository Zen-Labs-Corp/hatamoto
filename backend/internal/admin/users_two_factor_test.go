package admin

import (
	"github.com/google/uuid"
	"hatamoto.dev/core/internal/user"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/siteconfig"
)

// uuidRE hides the random user ids of the rig.
var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func newUsersTwoFactorRig(t *testing.T) *twoFactorSecRig {
	t.Helper()
	g := newTwoFactorSecRig(t)
	g.a.deps.Manager = &fakeManager{users: map[uuid.UUID]*user.User{g.owner.ID: g.owner, g.editor.ID: g.editor}}
	g.tf.secure = false // the Users table reads Counts
	return g
}

func TestUsersPageShowsPolicyAndTwoStepColumn(t *testing.T) {
	t.Parallel()

	g := newUsersTwoFactorRig(t)
	g.setPolicy(siteconfig.TwoFactorOwners)
	rec := g.do("GET", "/admin/settings/users", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, "2-step") || strings.Contains(body, `name="policy"`) {
		t.Error("the users list lacks the 2-step column or still carries the policy form")
	}
	rec = g.do("GET", "/admin/settings/users/sign-in", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{"Required for owners", "Required for everyone", "Optional", `name="policy"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(body, `value="owners" checked`) {
		t.Error("the stored policy is not checked")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/users-sign-in.html", uuidRE.ReplaceAllString(normalizePage(body), "UUID"))
}

func TestUsersPolicySaveNeedsSudoOwnerAndValidValue(t *testing.T) {
	t.Parallel()

	g := newUsersTwoFactorRig(t)
	form := url.Values{"policy": {siteconfig.TwoFactorEveryone}}
	if rec := g.do("POST", "/admin/settings/users/two-factor", "owner-token", form, nil); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/sudo") {
		t.Fatalf("without sudo: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if g.a.deps.Site.(*fakeSite).cfg.TwoFactorPolicy == siteconfig.TwoFactorEveryone {
		t.Fatal("saved without sudo")
	}
	if rec := g.do("POST", "/admin/settings/users/two-factor", "editor-token", form, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
	g.grant()
	if rec := g.do("POST", "/admin/settings/users/two-factor", "owner-token", url.Values{"policy": {"sometimes"}}, nil); rec.Code != 422 {
		t.Errorf("invalid value: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/users/two-factor", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	if g.a.deps.Site.(*fakeSite).cfg.TwoFactorPolicy != siteconfig.TwoFactorEveryone {
		t.Error("policy not stored")
	}
	if got := g.actions(); len(got) != 1 || got[0] != "settings.two_factor_policy" {
		t.Errorf("audit: %v", got)
	}
	if d := g.events[0].Detail; d["from"] == nil || d["to"] != siteconfig.TwoFactorEveryone {
		t.Errorf("audit detail: %v", d)
	}
}
