package admin

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

func init() { areaPages["pluginbrowse"] = []string{"plugins/browse"} }

func browseRig(t *testing.T, view RegistryView) (*rig, *fakePackages) {
	t.Helper()
	fp := &fakePackages{registry: view}
	return newRig(t, func(d *Deps) { d.Growth.Packages = fp }), fp
}

func browseView(n int) RegistryView {
	v := RegistryView{Host: "registry.example.test"}
	for i := 0; i < n; i++ {
		v.Entries = append(v.Entries, RegistryEntry{Kind: "plugin", Name: fmt.Sprintf("p%03d", i), Version: "1.0.0", Description: "Does thing " + fmt.Sprint(i)})
	}
	return v
}

func getBrowse(g *rig, query string) string {
	g.t.Helper()
	rec := g.do("GET", "/admin/plugins/browse"+query, "owner-token", nil, nil)
	if rec.Code != 200 {
		g.t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestBrowsePaging(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, browseView(25))
	first := getBrowse(g, "")
	for _, want := range []string{"p000", "p011", "25 plugins", `href="/admin/plugins/p000/about"`, "Showing 1–12 of 25", "Browse plugins"} {
		if !strings.Contains(first, want) {
			t.Errorf("page 1 misses %q", want)
		}
	}
	if strings.Contains(first, "p012") {
		t.Error("page 1 shows an item of page 2")
	}
	second := getBrowse(g, "?page=2")
	if !strings.Contains(second, "p012") || !strings.Contains(second, "p023") || strings.Contains(second, "p011") || strings.Contains(second, "p024") {
		t.Error("page 2 shows the wrong items")
	}
	if !strings.Contains(second, "Showing 13–24 of 25") {
		t.Error("page 2 summary")
	}
	if issues := a11y.Check(first, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestBrowseFiltersAndSort(t *testing.T) {
	t.Parallel()

	view := RegistryView{Host: "registry.example.test", Entries: []RegistryEntry{
		{Kind: "plugin", Name: "alpha", Title: "Alpha", Author: "Acme", Tier: "first-party", Category: "SEO", Permissions: []string{"settings"}},
		{Kind: "plugin", Name: "bravo", Title: "Bravo", Tier: "community", Category: "Forms", Permissions: []string{"http.fetch"}, Installed: true},
		{Kind: "plugin", Name: "charlie", Title: "Charlie", Permissions: []string{"site.scripts"}, Installed: true, Update: true},
	}}
	g, _ := browseRig(t, view)
	for _, tc := range []struct {
		query     string
		want, not []string
	}{
		{"?q=alp", []string{"Alpha", "1 plugin"}, []string{"Bravo", "Charlie"}},
		{"?tier=first-party", []string{"Alpha", "First-party"}, []string{"Bravo", "Charlie"}},
		{"?cat=Forms&hide=0", []string{"Bravo"}, []string{"Alpha", "Charlie"}},
		{"?perm=nonet&hide=0", []string{"Alpha", "Charlie"}, []string{"Bravo"}},
		{"?hide=1", []string{"Alpha"}, []string{"Bravo", "Charlie"}},
		{"?sort=name-desc&hide=0", []string{"Charlie"}, nil},
	} {
		body := getBrowse(g, tc.query)
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", tc.query, w)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(body, ">"+n+"<") {
				t.Errorf("%s: shows %q", tc.query, n)
			}
		}
	}
	desc := getBrowse(g, "?sort=name-desc&hide=0")
	if strings.Index(desc, "Charlie") > strings.Index(desc, "Alpha") {
		t.Error("descending sort not applied")
	}
	all := getBrowse(g, "?hide=0")
	for _, w := range []string{"Installed", "Update", ">Install<", `name="cat"`} {
		if !strings.Contains(all, w) {
			t.Errorf("card states: missing %q", w)
		}
	}
}

func TestBrowseEmptyAndStates(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, browseView(3))
	if body := getBrowse(g, "?q=zzz"); !strings.Contains(body, "No plugins match") || !strings.Contains(body, "Clear filters") {
		t.Error("no-match state missing")
	}
	empty, _ := browseRig(t, RegistryView{Host: "registry.example.test"})
	if body := getBrowse(empty, ""); !strings.Contains(body, "The registry has no plugins yet") || strings.Contains(body, "No plugins match") || strings.Contains(body, "Clear filters") {
		t.Error("an empty unfiltered registry needs a plain empty state without a clear button")
	}
	if body := getBrowse(g, ""); strings.Contains(body, `name="cat"`) {
		t.Error("category facet shown although no entry has a category")
	}

	off, _ := browseRig(t, RegistryView{Off: true})
	if body := getBrowse(off, ""); !strings.Contains(body, "No registry is set up") || strings.Contains(body, "Search plugins") {
		t.Error("registry-off state")
	}

	bad, _ := browseRig(t, RegistryView{Err: "dial tcp: refused", Host: "registry.example.test"})
	body := getBrowse(bad, "")
	for _, w := range []string{"not reachable right now", "Try again", "registry.example.test", "dial tcp: refused", `action="/admin/plugins/check-updates"`} {
		if !strings.Contains(body, w) {
			t.Errorf("unreachable state misses %q", w)
		}
	}
}

func TestBrowseFeaturedOnlyUnfiltered(t *testing.T) {
	t.Parallel()

	view := browseView(5)
	view.Entries[2].Featured = true
	g, _ := browseRig(t, view)
	if body := getBrowse(g, ""); !strings.Contains(body, "browse-featured") {
		t.Error("featured row missing")
	}
	if body := getBrowse(g, "?q=p"); strings.Contains(body, "browse-featured") {
		t.Error("featured row shown under a filter")
	}
}

func TestBrowseCategoryFacetShown(t *testing.T) {
	t.Parallel()

	view := browseView(2)
	view.Entries[0].Category = "Media"
	g, _ := browseRig(t, view)
	if body := getBrowse(g, ""); !strings.Contains(body, `name="cat"`) || !strings.Contains(body, "Media") {
		t.Error("category facet missing")
	}
}

func TestBrowseRefreshLine(t *testing.T) {
	t.Parallel()

	view := browseView(1)
	view.FetchedAt = time.Now().Add(-3 * time.Minute)
	g, _ := browseRig(t, view)
	if body := getBrowse(g, ""); !strings.Contains(body, "From registry.example.test. Signed index, refreshed 3 minutes ago.") {
		t.Error("registry line missing")
	}
	for _, c := range []struct {
		d    time.Duration
		want string
	}{{10 * time.Second, "just now"}, {time.Minute, "1 minute ago"}, {5 * time.Hour, "5 hours ago"}, {50 * time.Hour, "2 days ago"}} {
		if got := agoText(c.d); got != c.want {
			t.Errorf("agoText(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestBrowseCrumbsAndAccess(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, browseView(1))
	if got := strings.Join(trailLabels(t, getBrowse(g, "")), " / "); got != "Home / Plugins / Browse" {
		t.Errorf("crumbs %q", got)
	}
	if rec := g.do("GET", "/admin/plugins/browse", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestCheckUpdates(t *testing.T) {
	t.Parallel()

	g, fp := browseRig(t, browseView(1))
	for _, tc := range []struct{ ret, to string }{{"", "/admin/plugins"}, {"browse", "/admin/plugins/browse"}, {"elsewhere", "/admin/plugins"}} {
		rec := g.do("POST", "/admin/plugins/check-updates", "owner-token", url.Values{"return": {tc.ret}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != tc.to {
			t.Errorf("return %q: %d %q", tc.ret, rec.Code, rec.Header().Get("Location"))
		}
	}
	if fp.refreshed != 3 {
		t.Errorf("refreshed %d times, want 3", fp.refreshed)
	}
	if rec := g.do("POST", "/admin/plugins/check-updates", "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestBrowseTryAgainRefreshes(t *testing.T) {
	t.Parallel()

	g, fp := browseRig(t, RegistryView{Err: "down", Host: "registry.example.test"})
	rec := g.do("POST", "/admin/plugins/check-updates", "owner-token", url.Values{"return": {"browse"}}, nil)
	if rec.Code != 303 || fp.refreshed != 1 {
		t.Fatalf("status %d, refreshed %d", rec.Code, fp.refreshed)
	}
}

func TestCheckUpdatesNeedsCSRF(t *testing.T) {
	t.Parallel()

	g, fp := browseRig(t, browseView(1))
	g.realTokens()
	rec := g.do("POST", "/admin/plugins/check-updates", "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""})
	if rec.Code < 400 || fp.refreshed != 0 {
		t.Fatalf("status %d, refreshed %d", rec.Code, fp.refreshed)
	}
}

func installedView() RegistryView {
	return RegistryView{Host: "registry.example.test", Entries: []RegistryEntry{
		{Kind: "plugin", Name: "alpha", Title: "Alpha", Tier: "first-party", Permissions: []string{"settings"}},
		{Kind: "plugin", Name: "bravo", Title: "Bravo", Installed: true},
		{Kind: "plugin", Name: "charlie", Title: "Charlie", Installed: true, Update: true},
	}}
}

func TestBrowseHidesInstalledByDefault(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, installedView())
	for _, query := range []string{"", "?hide=1"} {
		body := getBrowse(g, query)
		if !strings.Contains(body, ">Alpha<") || strings.Contains(body, ">Bravo<") || strings.Contains(body, ">Charlie<") {
			t.Errorf("%q: installed plugins must be hidden", query)
		}
		for _, w := range []string{"1 plugin", "2 installed hidden", `href="/admin/plugins/browse?hide=0">Show installed`} {
			if !strings.Contains(body, w) {
				t.Errorf("%q: missing %q", query, w)
			}
		}
		if strings.Contains(body, "Clear filters") || strings.Contains(body, "No plugins match") || strings.Contains(body, "Remove filter") {
			t.Errorf("%q: the default hide is not a filter, chip or empty state", query)
		}
		if !strings.Contains(body, `id="browse-count"`) || !strings.Contains(body, `<span class="hm-visually-hidden">, </span>1<`) {
			t.Errorf("%q: the Filters badge must count the default hide", query)
		}
	}
}

func TestBrowseShowInstalled(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, installedView())
	body := getBrowse(g, "?hide=0")
	for _, w := range []string{">Alpha<", ">Bravo<", ">Charlie<", "3 plugins", `href="/admin/plugins/browse">Hide installed`} {
		if !strings.Contains(body, w) {
			t.Errorf("hide=0: missing %q", w)
		}
	}
	if strings.Contains(body, "installed hidden") || strings.Contains(body, "Show installed") {
		t.Error("hide=0: the hidden-count line must not show")
	}
	if !strings.Contains(body, `id="browse-count" hidden`) {
		t.Error("hide=0 and no chips: the badge is empty and hidden")
	}
}

func TestBrowseNoInstalledNoLine(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, browseView(3))
	body := getBrowse(g, "")
	if strings.Contains(body, "installed hidden") || strings.Contains(body, "Show installed") || strings.Contains(body, "Hide installed</a>") {
		t.Error("no plugin is installed: no muted line")
	}
}

func TestBrowseAllInstalled(t *testing.T) {
	t.Parallel()

	view := installedView()
	view.Entries = view.Entries[1:]
	g, _ := browseRig(t, view)
	body := getBrowse(g, "")
	if !strings.Contains(body, "All plugins are installed") || strings.Contains(body, "The registry has no plugins yet") || strings.Contains(body, "No plugins match") {
		t.Error("a fully installed registry needs its own state")
	}
	if !strings.Contains(body, `href="/admin/plugins/browse?hide=0"`) {
		t.Error("the state links to Show installed")
	}
}

func TestBrowseChipsAndBadgeCount(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, installedView())
	body := getBrowse(g, "?perm=nonet")
	for _, w := range []string{`href="/admin/plugins/browse" aria-label="Remove filter: No network access"`, ">Clear all<", `<span class="hm-visually-hidden">, </span>2<`} {
		if !strings.Contains(body, w) {
			t.Errorf("perm chip: missing %q", w)
		}
	}
	if strings.Contains(body, "Remove filter: Hide installed") {
		t.Error("hide must not be a chip")
	}
	if !strings.Contains(body, `perm=nonet`) || strings.Contains(body, `hide=1`) {
		t.Error("links carry the canonical query")
	}
	if issues := a11y.Check(getBrowse(g, "?perm=nonet&hide=0"), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestBrowseCountAnnouncedOutsideSwapTarget(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, installedView())
	body := getBrowse(g, "")
	i := strings.Index(body, `id="browse-status"`)
	j := strings.Index(body, `id="browse-results"`)
	if i < 0 || j < 0 || i > j || !strings.Contains(body, `id="browse-status" class="hm-visually-hidden" role="status"`) {
		t.Error("the live region must sit before, so outside, the swapped #browse-results")
	}
	if end := strings.Index(body[j:], `id="plugin-panel"`); end < 0 || strings.Contains(body[j:j+end], "aria-live") {
		t.Error("no live region inside the swap target")
	}
}

func TestBrowseCardPillAndDrawerWidth(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, installedView())
	body := getBrowse(g, "")
	if !strings.Contains(body, "hm-badge--sm") || !strings.Contains(body, `data-width="wide"`) {
		t.Error("First-party pill must be small and the plugin panel wide")
	}
	if strings.Count(body, `role="search"`) != 1 || strings.Contains(body, "hm-pluginbrowse__facets") {
		t.Error("one search form and no facet column")
	}
}
