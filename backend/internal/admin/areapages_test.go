package admin

import (
	"net/http"
	"testing"
)

// areaPages lists, per admin area, the page templates whose a11y tests live in
// that area's own test file (TestEveryPageTemplateIsExercised merges them), so
// areas do not edit one shared list.
var areaPages = map[string][]string{
	"menus":     {"menus/list", "menus/new", "menus/edit"},
	"redirects": {"redirects/list", "redirects/notfound", "redirects/explain", "redirects/edit"},
	"sections":  {"sections/list", "sections/edit"},
	"analytics": {"analytics/overview", "analytics/crawlers", "analytics/content", "analytics/referrals", "analytics/health", "analytics/agent"},
	"linking":   {"linking/rules", "linking/rule", "linking/import", "linking/suggestions", "linking/equity", "linking/inbound"},
	"plugins":   {"settings/plugins", "plugins/plugin", "plugins/permissions"},
	"forms":     {"forms/list", "forms/new", "forms/edit", "forms/submissions", "forms/submission", "bulkdelete"},
}

// TestSiteAndGrowthScreensAnswerInGo: with no service wired, every screen of
// this slug still answers in Go (a 503 page or a redirect), never a 404.
func TestSiteAndGrowthScreensAnswerInGo(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	id := "00000000-0000-4000-8000-000000000001"
	for _, p := range []string{
		"/admin/theme/menus", "/admin/theme/menus/" + id, "/admin/settings/forms", "/admin/settings/forms/new", "/admin/settings/forms/" + id + "/edit", "/admin/settings/submissions?form=" + id,
		"/admin/settings/submissions/export.csv?form=" + id, "/admin/settings/redirects", "/admin/settings/redirects/404", "/admin/settings/redirects/explain", "/admin/settings/redirects/new",
		"/admin/theme/templates", "/admin/analytics", "/admin/analytics/crawlers", "/admin/analytics/content", "/admin/analytics/referrals", "/admin/analytics/health", "/admin/analytics/agents/gptbot", "/admin/analytics/data/overview.json",
		"/admin/settings/linking", "/admin/settings/linking/equity", "/admin/settings/linking/suggestions", "/admin/settings/linking/import", "/admin/settings/linking/rules/new",
		"/admin/plugins", "/admin/settings/redirects", "/admin/settings/general",
	} {
		rec := g.do("GET", p, "owner-token", nil, nil)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusOK {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

// The previous admin signed in at /auth/login; those URLs land on the Go page.
func TestOldAuthPathsRedirectToLogin(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, p := range []string{"/auth/login", "/auth/register", "/auth/login?next=/admin/entities"} {
		rec := g.do("GET", p, "", nil, nil)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin/login" {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
}
