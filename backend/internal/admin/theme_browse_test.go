package admin

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

func init() {
	areaPages["themebrowse"] = []string{"theme/browse"}
	areaPages["themebrowsedetail"] = []string{"theme/browse-detail"}
}

const themeRegistryHost = "registry.example.test"

// themeBrowseView is a registry of six themes: the live one (installed, blog,
// first-party, featured, with a screenshot), an installable docs theme, one with
// an update, an installed one, one with no site type or screenshot, and a plugin.
func themeBrowseView() RegistryView {
	return RegistryView{Host: themeRegistryHost, Entries: []RegistryEntry{
		{Kind: "theme", Name: "hanshi", Title: "Hanshi", Tier: "first-party", Version: "1.0.0", Description: "A calm blog theme", SiteType: "blog",
			Flags: []string{"dark", "nojs"}, Installed: true, InstalledVersion: "1.0.0", Featured: true, Screenshot: "https://registry.example.test/screenshots/hanshi.jpg"},
		{Kind: "theme", Name: "ledger", Title: "Ledger", Author: "Acme", Version: "0.3.0", Description: "Docs", SiteType: "docs", Flags: []string{"nojs"},
			Screenshot: "https://registry.example.test/screenshots/ledger.jpg"},
		{Kind: "theme", Name: "kiln", Author: "Someone", Version: "0.2.0", Description: "Landing pages", SiteType: "landing", Flags: []string{"builder"},
			Installed: true, InstalledVersion: "0.1.0", Update: true},
		{Kind: "theme", Name: "shelf", Version: "2.0.0", Installed: true, InstalledVersion: "2.0.0", SiteType: "blog"},
		{Kind: "theme", Name: "plain", Version: "v0.1.0", Description: "No extras"},
		{Kind: "plugin", Name: "seo", Version: "1.0.0"},
	}}
}

func themeBrowseRig(t *testing.T, view RegistryView) (*rig, *fakePackages) {
	t.Helper()
	g, fp := browseRig(t, view)
	fp.themes = []ThemeInfo{{Name: "default", Builtin: true}, {Name: "hanshi", Active: true, HasSettings: true}, {Name: "kiln"}, {Name: "shelf"}}
	return g, fp
}

func getThemeBrowse(g *rig, query string) string {
	g.t.Helper()
	rec := g.do("GET", "/admin/theme/browse"+query, "owner-token", nil, nil)
	if rec.Code != 200 {
		g.t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestThemeBrowseOff(t *testing.T) {
	t.Parallel()

	g, _ := themeBrowseRig(t, RegistryView{Off: true})
	body := getThemeBrowse(g, "")
	if !strings.Contains(body, "No registry is set up") || !strings.Contains(body, "browse themes") || strings.Contains(body, "Search themes") {
		t.Error("registry-off state")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestThemeBrowseError(t *testing.T) {
	t.Parallel()

	g, fp := themeBrowseRig(t, RegistryView{Err: "dial tcp: refused", Host: themeRegistryHost})
	body := getThemeBrowse(g, "")
	for _, w := range []string{"not reachable right now", "Try again", themeRegistryHost, "Installed themes keep working", "dial tcp: refused",
		`action="/admin/plugins/check-updates"`, `name="return" value="theme-browse"`} {
		if !strings.Contains(body, w) {
			t.Errorf("unreachable state misses %q", w)
		}
	}
	rec := g.do("POST", "/admin/plugins/check-updates", "owner-token", url.Values{"return": {"theme-browse"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/theme/browse" || fp.refreshed != 1 {
		t.Errorf("try again: %d %q refreshed %d", rec.Code, rec.Header().Get("Location"), fp.refreshed)
	}
}

func TestThemeBrowseEmpty(t *testing.T) {
	t.Parallel()

	g, _ := themeBrowseRig(t, RegistryView{Host: themeRegistryHost})
	if body := getThemeBrowse(g, ""); !strings.Contains(body, "The registry has no themes yet") || strings.Contains(body, "Clear filters") {
		t.Error("empty unfiltered registry needs a plain empty state")
	}
	full, _ := themeBrowseRig(t, themeBrowseView())
	if body := getThemeBrowse(full, "?q=zzz"); !strings.Contains(body, "No themes match") || !strings.Contains(body, "Clear filters") {
		t.Error("no-match state missing")
	}
	allInstalled, _ := themeBrowseRig(t, RegistryView{Host: themeRegistryHost, Entries: []RegistryEntry{{Kind: "theme", Name: "a", Version: "1.0.0", Installed: true}}})
	if body := getThemeBrowse(allInstalled, ""); !strings.Contains(body, "All themes are installed") || !strings.Contains(body, "Show installed") {
		t.Error("all-installed state missing")
	}
}

func TestThemeBrowsePopulated(t *testing.T) {
	t.Parallel()

	g, _ := themeBrowseRig(t, themeBrowseView())
	body := getThemeBrowse(g, "")
	for _, w := range []string{
		"Browse themes", "From registry.example.test", "Search themes",
		`<img class="hm-themecard__img" src="https://registry.example.test/screenshots/hanshi.jpg" alt="" loading="lazy">`,
		`href="/admin/theme/themes/hanshi/about"`, `data-hm-open="plugin-panel"`,
		"Blog · v1.0.0", "● Live", "browse-featured", "Docs · v0.3.0", ">Install<",
		"v0.1.0", `hm-themecard__tile`, "2 installed hidden",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
	if !strings.Contains(body, ` themes · 2 installed hidden · <a href="/admin/theme/browse?hide=0">Show installed</a></p>`) {
		t.Error("count and installed-hidden note must share one line")
	}
	if !strings.Contains(body, `aria-labelledby="tc-c-hanshi-name" aria-describedby="tc-c-hanshi-action"`) || !strings.Contains(body, `id="tc-c-hanshi-action">● Live<`) {
		t.Error("a card must describe itself by its state text, named by the theme name")
	}
	if strings.Contains(body, ">seo<") || strings.Contains(body, "Permissions") {
		t.Error("plugin entries or permission facets leaked into theme browse")
	}
	all := getThemeBrowse(g, "?hide=0")
	for _, w := range []string{"Update to 0.2.0", "✓ Installed", "● Live"} {
		if !strings.Contains(all, w) {
			t.Errorf("hide=0 misses %q", w)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if got := strings.Join(trailLabels(t, body), " / "); got != "Home / Theme / Themes / Browse" {
		t.Errorf("crumbs %q", got)
	}
}

func TestThemeBrowseFiltersAndChips(t *testing.T) {
	t.Parallel()

	g, _ := themeBrowseRig(t, themeBrowseView())
	body := getThemeBrowse(g, "?type=docs&type=landing&feat=nojs&hide=0")
	for _, w := range []string{"Ledger", "Site type: Docs", "Features: Works without JS", "Remove filter: Site type: Docs", "Remove filter: Features: Works without JS",
		`name="type"`, `name="feat"`, "Blog (2)", "Dark mode", "Works without JS", "Visual builder", "1 theme"} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
	for _, n := range []string{"Hanshi", "shelf", "plain", "Kiln"} {
		if strings.Contains(body, ">"+n+"<") {
			t.Errorf("shows %q", n)
		}
	}
	and := getThemeBrowse(g, "?feat=dark&feat=nojs&hide=0")
	if !strings.Contains(and, ">Hanshi<") || strings.Contains(and, ">Ledger<") {
		t.Error("feat must be AND")
	}
	if strings.Contains(and, "Clear filters") && !strings.Contains(and, "Remove") {
		t.Error("filters without chips")
	}
	bare, _ := themeBrowseRig(t, RegistryView{Host: themeRegistryHost, Entries: []RegistryEntry{{Kind: "theme", Name: "a", Version: "1.0.0"}}})
	if b := getThemeBrowse(bare, ""); strings.Contains(b, `name="type"`) || strings.Contains(b, `name="feat"`) {
		t.Error("Site type and Features groups shown although no theme has a value")
	}
}

func TestThemeBrowseHideInstalledLine(t *testing.T) {
	t.Parallel()

	g, _ := themeBrowseRig(t, themeBrowseView())
	for _, query := range []string{"", "?hide=1"} {
		body := getThemeBrowse(g, query)
		// installed: kiln, shelf (hanshi is live and never counted nor hidden)
		for _, w := range []string{"2 installed hidden", `href="/admin/theme/browse?hide=0">Show installed`, ">Hanshi<", "● Live"} {
			if !strings.Contains(body, w) {
				t.Errorf("%q: missing %q", query, w)
			}
		}
		for _, n := range []string{"kiln", "shelf"} {
			if strings.Contains(body, ">"+n+"<") {
				t.Errorf("%q: installed %s shown", query, n)
			}
		}
		if strings.Contains(body, "Clear filters") || strings.Contains(body, "No themes match") {
			t.Errorf("%q: the default hide is not a filter", query)
		}
	}
	if body := getThemeBrowse(g, "?hide=0"); !strings.Contains(body, "Hide installed") || !strings.Contains(body, ">shelf<") {
		t.Error("hide=0 must show installed themes")
	}
}

func TestThemeBrowseEditorForbidden(t *testing.T) {
	t.Parallel()

	g, _ := themeBrowseRig(t, themeBrowseView())
	for _, p := range []string{"/admin/theme/browse", "/admin/theme/themes/hanshi/about"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("%s: editor got %d", p, rec.Code)
		}
	}
}

func TestThemeBrowseRefreshLine(t *testing.T) {
	t.Parallel()

	view := themeBrowseView()
	view.FetchedAt = time.Now().Add(-3 * time.Minute)
	g, _ := themeBrowseRig(t, view)
	if body := getThemeBrowse(g, ""); !strings.Contains(body, "From registry.example.test. Signed index, refreshed 3 minutes ago.") {
		t.Error("registry line missing")
	}
}

// The card link is named by the theme name alone, and a theme shown twice
// (Featured and the grid) keeps its ids unique.
func TestThemeBrowseCardAccessibleName(t *testing.T) {
	t.Parallel()

	g, _ := themeBrowseRig(t, themeBrowseView())
	body := getThemeBrowse(g, "?hide=0")
	for _, w := range []string{`aria-labelledby="tc-f-hanshi-name"`, `aria-labelledby="tc-c-hanshi-name"`, `id="tc-f-hanshi-name">Hanshi<`, `id="tc-c-hanshi-name">Hanshi<`, `id="tc-c-ledger-name">Ledger<`} {
		if !strings.Contains(body, w) {
			t.Errorf("card misses %q", w)
		}
	}
	if n := strings.Count(body, `id="tc-c-hanshi-name"`); n != 1 {
		t.Errorf("name id repeated %d times", n)
	}
}

// A registry theme named like the built-in theme is not listed.
func TestThemeBrowseHidesDefaultNamedEntry(t *testing.T) {
	t.Parallel()

	view := themeBrowseView()
	view.Entries = append(view.Entries, RegistryEntry{Kind: "theme", Name: "default", Title: "Default Lookalike", Version: "1.0.0"})
	g, _ := themeBrowseRig(t, view)
	if body := getThemeBrowse(g, "?hide=0"); strings.Contains(body, "Default Lookalike") {
		t.Error("registry theme named default is listed")
	}
}
