package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func sectionTabs(secs []SettingsSection) map[string][]string {
	out := map[string][]string{}
	for _, s := range secs {
		for _, t := range s.Tabs {
			out[s.ID] = append(out[s.ID], t.ID)
		}
	}
	return out
}

func TestSettingsForRoles(t *testing.T) {
	t.Parallel()

	owner := sectionTabs(SettingsFor("owner"))
	for id, want := range map[string]string{
		"general": "general", "locales": "locales locale-addresses locale-business", "media": "media", "entity-models": "types taxonomies", "forms": "forms submissions spam",
		"seo": "seo-general seo-titles seo-social", "redirects": "redirects", "linking": "linking", "users": "users", "api": "api",
		"import-export": "import export", "performance": "performance", "updates": "updates", "backups": "backups", "security": "admin-address",
	} {
		if got := strings.Join(owner[id], " "); got != want {
			t.Errorf("owner %s tabs %q, want %q", id, got, want)
		}
	}
	editor := sectionTabs(SettingsFor("editor"))
	for id, want := range map[string]string{"media": "media", "forms": "forms submissions", "redirects": "redirects", "linking": "linking"} {
		if got := strings.Join(editor[id], " "); got != want {
			t.Errorf("editor %s tabs %q, want %q", id, got, want)
		}
	}
	for _, id := range []string{"appearance", "menus"} {
		if _, ok := owner[id]; ok {
			t.Errorf("Settings still has the section %s, which moved to Theme", id)
		}
	}
	for _, id := range []string{"general", "locales", "seo", "entity-models", "users", "api", "import-export", "performance", "updates", "backups", "security"} {
		if _, ok := editor[id]; ok {
			t.Errorf("editor sees section %s", id)
		}
	}
	if len(SettingsFor("nobody-role")) != 4 {
		// roles other than owner or editor still pass the ungated tabs today
		t.Errorf("unknown role sections: %v", sectionTabs(SettingsFor("nobody-role")))
	}
}

func TestSettingsLiveTabsUseTheNewHref(t *testing.T) {
	t.Parallel()

	// A tab with no live page and no legacy route stays hidden until it is live.
	has := func(id string) bool {
		for _, sec := range SettingsFor("owner") {
			for _, tab := range sec.Tabs {
				if tab.ID == id {
					return true
				}
			}
		}
		return false
	}
	if settingsLive["export"] {
		t.Skip("export is live; every tab is live")
	}
	if has("export") {
		t.Error("export tab shown before its page exists")
	}
	settingsLive["export"] = true
	defer delete(settingsLive, "export")
	if !has("export") {
		t.Error("live export tab is hidden")
	}
}

func TestSettingsEntryRedirects(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, tc := range []struct {
		token, path string
		code        int
		to          string
	}{
		{"owner-token", "/admin/settings", 303, "/admin/settings/general"},
		{"editor-token", "/admin/settings", 303, "/admin/settings/media"},
		{"editor-token", "/admin/settings?section=seo", 303, "/admin/settings/media"},
		{"editor-token", "/admin/settings?section=team", 303, "/admin/settings/media"},
		{"editor-token", "/admin/settings?section=forms", 303, "/admin/settings/forms"},
		{"owner-token", "/admin/settings/entity-models", 302, "/admin/settings/types"},
		{"owner-token", "/admin/settings/content-model", 302, "/admin/settings/types"},
		{"editor-token", "/admin/settings/seo", 403, ""},
		{"owner-token", "/admin/settings/seo", 302, "/admin/settings/seo/general"},
		{"owner-token", "/admin/settings?section=appearance", 303, "/admin/theme/themes"},
		{"owner-token", "/admin/settings?section=menus", 303, "/admin/theme/menus"},
		{"owner-token", "/admin/settings?section=menus-forms", 303, "/admin/theme/menus"},
		{"editor-token", "/admin/settings?section=appearance", 303, "/admin/theme/blocks"},
		{"editor-token", "/admin/settings?section=menus", 303, "/admin/theme/menus"},
		{"owner-token", "/admin/settings/team", 302, "/admin/settings/users"},
		{"editor-token", "/admin/settings/team", 403, ""},
		{"editor-token", "/admin/settings/import-export", 403, ""},
	} {
		rec := g.do("GET", tc.path, tc.token, nil, nil)
		if rec.Code != tc.code || rec.Header().Get("Location") != tc.to {
			t.Errorf("%s %s: %d %q, want %d %q", tc.token, tc.path, rec.Code, rec.Header().Get("Location"), tc.code, tc.to)
		}
	}
}

func TestSettingsFrameRendersForSettingsPages(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<nav class="hm-subnav" aria-label="Settings">`,
		`<a class="hm-subnav__link" href="/admin/settings/general" aria-current="page">`,
		`<form class="hm-subnav-select" method="get" action="/admin/settings">`,
		`<option value="redirects">Redirects</option>`, `<option value="api">API</option>`, `<option value="entity-models">Entity models</option>`,
		`<header class="hm-section-head">`, `<h1>General</h1>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	if strings.Contains(body, `Settings sections`) {
		t.Error("the old settings tab strip is still rendered")
	}
}

func TestSettingsCrumbsChain(t *testing.T) {
	t.Parallel()

	got := areaCrumbs("menus", Crumb{Label: "Main"})
	want := []Crumb{{Label: "Theme", Href: "/admin/theme"}, {Label: "Menus", Href: "/admin/theme/menus"}, {Label: "Main"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("crumbs = %v, want %v", got, want)
	}
	if got := areaCrumbs("media", Crumb{Label: "a.png"}); len(got) != 3 || got[1].Label != "Media" {
		t.Fatalf("a section named like its tab should appear once: %v", got)
	}
}

// Every Settings page has one trail, in the top bar, that starts Home, Settings
// and ends on the current page. The page header renders no crumbs of its own.
func TestSettingsPageCrumbsStartAtSettings(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	topbar := regexp.MustCompile(`(?s)<nav class="hm-topbar__crumbs" aria-label="Breadcrumb"><ol>(.*?)</ol></nav>`)
	items := regexp.MustCompile(`<li[^>]*>(.*?)</li>`)
	check := func(name, body string, wantLen int, last string) {
		t.Helper()
		if n := strings.Count(body, `aria-label="Breadcrumb"`); n != 1 {
			t.Errorf("%s: %d Breadcrumb navs, want 1", name, n)
		}
		if strings.Contains(body, "hm-crumbs") {
			t.Errorf("%s: panel crumbs rendered", name)
		}
		m := topbar.FindStringSubmatch(body)
		if m == nil {
			t.Errorf("%s: no top bar trail", name)
			return
		}
		li := items.FindAllStringSubmatch(m[1], -1)
		if len(li) != wantLen || !strings.Contains(li[0][1], ">Home</a>") || !strings.Contains(li[1][1], ">Settings</") {
			t.Errorf("%s: trail %q, want %d items starting Home, Settings", name, m[1], wantLen)
			return
		}
		tail := m[1][strings.LastIndex(m[1], "<li"):]
		if !strings.Contains(tail, `aria-current="page"`) || !strings.Contains(tail, last) {
			t.Errorf("%s: last crumb %q is not the current %q", name, tail, last)
		}
	}
	// A tab page has no crumbs of its own: the top bar falls back to section and title.
	rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("general: status %d", rec.Code)
	}
	check("general", rec.Body.String(), 3, "General")

	// A sub-page sets its crumbs; the top bar renders the whole chain.
	for name, crumbs := range map[string][]Crumb{
		"types edit":  areaCrumbs("types", Crumb{Label: "Team"}),
		"forms entry": areaCrumbs("forms", Crumb{Label: "Contact", Href: "/admin/settings/forms/x/edit"}, Crumb{Label: "Submission"}),
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/admin/profile", nil)
		v := g.a.NewView(rr, req, "T")
		v.Data, v.Crumbs = profileData{}, crumbs
		g.a.Render(rr, req, http.StatusOK, "profile/index", v)
		last := crumbs[len(crumbs)-1].Label
		check(name, rr.Body.String(), len(crumbs)+1, last)
	}
}

// groupsOf lists the distinct, consecutive group headings of a sidebar with the
// section IDs under each.
func groupsOf(secs []SettingsSection) (order []string, members map[string][]string) {
	members = map[string][]string{}
	for _, s := range secs {
		if len(order) == 0 || order[len(order)-1] != s.Group {
			order = append(order, s.Group)
		}
		members[s.Group] = append(members[s.Group], s.ID)
	}
	return order, members
}

func TestSettingsNavGroups(t *testing.T) {
	t.Parallel()

	order, members := groupsOf(SettingsFor("owner"))
	if got := strings.Join(order, ","); got != "Site,Content,System" {
		t.Fatalf("owner group order %q", got)
	}
	for g, want := range map[string]string{
		"Site":    "general locales email users",
		"Content": "media entity-models forms seo redirects linking",
		"System":  "api webhooks performance updates backups import-export security",
	} {
		if got := strings.Join(members[g], " "); got != want {
			t.Errorf("owner %s sections %q, want %q", g, got, want)
		}
	}
	// An editor keeps only the Content sections; Site and System have no
	// section left, so their headings are not produced at all.
	order, _ = groupsOf(SettingsFor("editor"))
	if got := strings.Join(order, ","); got != "Content" {
		t.Errorf("editor groups %q, want Content only", got)
	}
	// The Theme area shows no headings.
	for _, s := range ThemeFor("owner") {
		if s.Group != "" {
			t.Errorf("theme section %s has group %q", s.ID, s.Group)
		}
	}
}

func TestAreaNavRendersGroupHeadings(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	html := rec.Body.String()
	for _, h := range []string{">Site</li>", ">Content</li>", ">System</li>", `<optgroup label="System">`, `<optgroup label="Site">`} {
		if !strings.Contains(html, h) {
			t.Errorf("areanav missing %q", h)
		}
	}
	if strings.Count(html, "<optgroup") != strings.Count(html, "</optgroup>") {
		t.Error("unbalanced optgroup")
	}
	if !strings.Contains(html, ">Go<") {
		t.Error("the Go button must stay (WCAG 3.2.2)")
	}
	// The Theme area has no headings.
	rec = g.do("GET", "/admin/theme/themes", "owner-token", nil, nil)
	if strings.Contains(rec.Body.String(), "hm-subnav__group") || strings.Contains(rec.Body.String(), "<optgroup") {
		t.Error("theme sidebar must not show group headings")
	}
}

func TestAreaLocateAnalytics(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"/admin/analytics":                 "ai-overview",
		"/admin/analytics/traffic":         "ai-traffic",
		"/admin/analytics/conversions":     "ai-conversions",
		"/admin/analytics/crawlers":        "ai-crawlers",
		"/admin/analytics/content":         "ai-content",
		"/admin/analytics/referrals":       "ai-referrals",
		"/admin/analytics/health":          "ai-health",
		"/admin/analytics/agents/gptbot":   "ai-crawlers",
		"/admin/analytics/agents/unknown":  "ai-crawlers",
		"/admin/analytics/crawlers/robots": "ai-crawlers",
	} {
		if sec, tab := areaLocate(path); sec != want || tab != want {
			t.Errorf("areaLocate(%q) = %q, %q; want %q", path, sec, tab, want)
		}
	}
}

// AreaCarry is appended to every sidebar link, and only then: Settings and
// Theme pages carry nothing and keep their plain links.
func TestAreaNavAppendsTheCarry(t *testing.T) {
	t.Parallel()

	v := &View{Section: "ai-health", User: &CurrentUser{Role: "editor"}, AreaCarry: url.Values{"from": {"2026-01-01"}, "compare": {"1"}}}
	nav := v.AreaNav()
	if len(nav) != 8 {
		t.Fatalf("%d sections", len(nav))
	}
	for _, s := range nav {
		if !strings.HasSuffix(s.Href, "?compare=1&from=2026-01-01") || !strings.HasSuffix(s.Tabs[0].Href, "?compare=1&from=2026-01-01") {
			t.Errorf("%s: %s / %s", s.ID, s.Href, s.Tabs[0].Href)
		}
	}
	if again := analyticsArea.For("editor"); strings.Contains(again[0].Href, "?") {
		t.Error("the registry must not be changed by a carry")
	}
	plain := (&View{Section: "general", User: &CurrentUser{Role: "owner"}}).AreaNav()
	for _, s := range plain {
		if strings.Contains(s.Href, "?") {
			t.Errorf("settings link %s carries a query", s.Href)
		}
	}
}
