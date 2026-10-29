package admin

import (
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

func init() { areaPages["pluginbrowsedetail"] = []string{"plugins/browse-detail"} }

func detailView() RegistryView {
	return RegistryView{Host: "registry.example.test", Entries: []RegistryEntry{
		{Kind: "plugin", Name: "fresh", Title: "Fresh", Author: "Acme", Tier: "first-party", Version: "1.0.0", Category: "SEO",
			About: "First paragraph.\nSecond <b>paragraph</b>.", Features: []string{"Does one", "Does two"},
			Permissions: []string{"settings", "site.scripts"}, Homepage: "https://example.test/fresh", Source: "javascript:alert(1)",
			Commit: "abc1234", Size: 2048},
		{Kind: "plugin", Name: "have", Version: "1.0.0", Description: "Installed one", Installed: true, InstalledVersion: "1.0.0"},
		{Kind: "plugin", Name: "old", Version: "2.0.0", Installed: true, InstalledVersion: "1.0.0", Update: true},
		{Kind: "plugin", Name: "settings", Version: "1.0.0", Description: "A plugin with a route-like name"},
		{Kind: "theme", Name: "skin", Version: "1.0.0"},
	}}
}

func getAbout(g *rig, name string, hdr map[string]string) (int, string) {
	g.t.Helper()
	rec := g.do("GET", "/admin/plugins/"+name+"/about", "owner-token", nil, hdr)
	return rec.Code, rec.Body.String()
}

func TestBrowseDetailNotInstalled(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, detailView())
	code, body := getAbout(g, "fresh", nil)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"By Acme", "First paragraph.", "Second &lt;b&gt;paragraph&lt;/b&gt;.", "Does two", "<code>site.scripts</code>",
		"Handle with care", "You can turn each one off on the next screen, before anything is installed.",
		`action="/admin/plugins/fetch"`, `name="name" value="fresh"`, "Install…", "2 KB", "abc1234",
		`href="https://example.test/fresh" rel="noopener noreferrer"`, "SEO"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "javascript:") {
		t.Error("an unsafe source address is rendered")
	}
	if strings.Contains(body, "/admin/plugins/fresh/settings") {
		t.Error("Settings shown for a plugin that is not installed")
	}
	if got := strings.Join(trailLabels(t, body), " / "); got != "Home / Plugins / Browse / Fresh" {
		t.Errorf("crumbs %q", got)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestBrowseDetailInstalledAndUpdate(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, detailView())
	_, have := getAbout(g, "have", nil)
	if !strings.Contains(have, `href="/admin/plugins/have/settings"`) || strings.Contains(have, "/plugins/fetch") {
		t.Error("installed plugin: want a Settings link and no install form")
	}
	if !strings.Contains(have, "It asks for no permissions.") {
		t.Error("no-permission copy missing")
	}
	_, old := getAbout(g, "old", nil)
	if !strings.Contains(old, "Review update") || !strings.Contains(old, `action="/admin/plugins/fetch"`) || !strings.Contains(old, `href="/admin/plugins/old/settings"`) {
		t.Error("update available: want Review update and Settings")
	}
}

// A plugin named like a route word still resolves: the literal "about" segment
// is the last one, so /plugins/settings/about is the detail of "settings".
func TestBrowseDetailRouteLikeName(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, detailView())
	code, body := getAbout(g, "settings", nil)
	if code != 200 || !strings.Contains(body, "A plugin with a route-like name") {
		t.Fatalf("status %d", code)
	}
}

func TestBrowseDetailNotFoundAndStates(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, detailView())
	for _, name := range []string{"missing", "skin"} {
		if code, _ := getAbout(g, name, nil); code != 404 {
			t.Errorf("%s: status %d, want 404", name, code)
		}
	}
	bad, _ := browseRig(t, RegistryView{Err: "down", Host: "registry.example.test"})
	if code, body := getAbout(bad, "fresh", nil); code != 503 || !strings.Contains(body, "not reachable") {
		t.Errorf("unreachable: %d", code)
	}
	off, _ := browseRig(t, RegistryView{Off: true})
	if code, _ := getAbout(off, "fresh", nil); code != 404 {
		t.Errorf("registry off: %d", code)
	}
	if rec := g.do("GET", "/admin/plugins/fresh/about", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestBrowseDetailFragment(t *testing.T) {
	t.Parallel()

	g, _ := browseRig(t, detailView())
	_, full := getAbout(g, "fresh", nil)
	code, frag := getAbout(g, "fresh", map[string]string{"HX-Request": "true", "HX-Target": "plugin-panel"})
	if code != 200 || strings.Contains(frag, "<html") || strings.Contains(frag, "hm-sidebar") || !strings.Contains(frag, "First paragraph.") {
		t.Fatalf("fragment status %d", code)
	}
	if !strings.Contains(full, "<html") {
		t.Error("full page lacks the shell")
	}
	// HX-Request alone is not a fragment request: the drawer must name its target.
	if _, noTarget := getAbout(g, "fresh", map[string]string{"HX-Request": "true"}); !strings.Contains(noTarget, "<html") {
		t.Error("HX-Request without HX-Target must get the full page")
	}
}

// The drawer's fetch must send the HX-Target the server needs to answer with a fragment.
func TestPluginPanelJSSendsHXTarget(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/js/pluginpanel.js")
	if err != nil {
		t.Fatal(err)
	}
	if src := string(b); !strings.Contains(src, `"HX-Request": "true"`) || !strings.Contains(src, `"HX-Target": "plugin-panel"`) {
		t.Error("pluginpanel.js must send HX-Request and HX-Target so the server returns the fragment")
	}
}

func TestHTTPURL(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"https://example.test/a": "https://example.test/a", "http://example.test": "http://example.test",
		"javascript:alert(1)": "", "//example.test": "", "/relative": "", "": "", "ftp://example.test": "",
	} {
		if got := httpURL(in); got != want {
			t.Errorf("httpURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The filters collapse on a narrow screen; the markup keeps them open for wide screens and no-JS.
func TestBrowseFiltersCollapseOnNarrowScreens(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/js/pluginpanel.js")
	if err != nil {
		t.Fatal(err)
	}
	if src := string(b); !strings.Contains(src, "matchMedia(NARROW)") || !strings.Contains(src, `removeAttribute("open")`) || !strings.Contains(src, "hm-pluginbrowse__facets") {
		t.Error("pluginpanel.js must collapse the browse filters on a narrow screen")
	}
	css, err := os.ReadFile("../../web/admin/css/pages/pluginbrowse.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "(min-width: 900px)") {
		t.Error("pluginbrowse.css breakpoint moved: update NARROW in pluginpanel.js")
	}
}
