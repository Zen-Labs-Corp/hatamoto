package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
)

// No t.Parallel: authztest.Use registers a process-wide authorizer.

// denyOnly answers like the default authorizer except that it refuses act, so
// the route group still admits the owner and only the in-handler check fails.
func denyOnly(act authz.Action) authztest.Func {
	return func(ctx context.Context, s authz.Subject, a authz.Action, o authz.Object) (bool, error) {
		if a == act {
			return false, nil
		}
		return authz.Default().Can(ctx, s, a, o)
	}
}

func TestAuthzInlineTrafficSettingsSave(t *testing.T) {
	eng := &fakeEngagement{on: true}
	g, _ := settingsRig(t, eng)

	if rec := g.do("POST", "/admin/analytics/traffic/settings", "owner-token", url.Values{"engagement": {"1"}}, nil); rec.Code != 303 || len(eng.set) != 1 {
		t.Fatalf("default owner save: %d, saved %v", rec.Code, eng.set)
	}

	authztest.Use(t, denyOnly(authz.ActionAnalyticsSettingsManage))
	rec := g.do("POST", "/admin/analytics/traffic/settings", "owner-token", url.Values{"engagement": {"1"}}, nil)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "Only the site owner can change what the visit counter records.") {
		t.Errorf("denied save: %d %q", rec.Code, rec.Body.String())
	}
	if len(eng.set) != 1 {
		t.Errorf("a denied save changed the setting: %v", eng.set)
	}
}

func TestAuthzInlineRobotsSave(t *testing.T) {
	g, fs := newAnalyticsRig(t)
	form := url.Values{}
	for _, p := range robotsPurposes {
		form.Set("purpose."+string(p), "allow")
	}
	form.Set("purpose.training", "deny")

	authztest.Use(t, denyOnly(authz.ActionRobotsPolicyWrite))
	rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", form, nil)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "Only the site owner can change the robots policy.") {
		t.Errorf("denied save: %d %q", rec.Code, rec.Body.String())
	}
	if len(fs.denied) != 0 {
		t.Errorf("a denied save stored a policy: %v", fs.denied)
	}

	authztest.Use(t, authz.Default())
	if rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", form, nil); rec.Code != 303 {
		t.Errorf("default owner save: %d", rec.Code)
	}
}
