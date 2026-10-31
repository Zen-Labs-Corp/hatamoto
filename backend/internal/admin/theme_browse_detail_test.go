package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

func themeDetailView() RegistryView {
	return RegistryView{Host: themeRegistryHost, Entries: []RegistryEntry{
		{Kind: "theme", Name: "fresh", Title: "Fresh", Author: "Acme", Tier: "first-party", Version: "1.0.0", SiteType: "docs", Flags: []string{"dark", "nojs", "builder"},
			About: "First paragraph.\nSecond <b>paragraph</b>.", Features: []string{"Does one", "Does two"}, Size: 2048, Commit: "abc1234",
			Homepage: "https://demo.example.test/fresh", Source: "javascript:alert(1)", Screenshot: "https://registry.example.test/screenshots/fresh.jpg"},
		{Kind: "theme", Name: "featonly", Version: "1.0.0", Features: []string{"Only a feature"}},
		{Kind: "theme", Name: "bare", Version: "1.0.0", Description: "Bare one", Homepage: "javascript:alert(1)"},
		{Kind: "theme", Name: "have", Version: "1.0.0", Installed: true, InstalledVersion: "1.0.0"},
		{Kind: "theme", Name: "old", Version: "2.0.0", Installed: true, InstalledVersion: "1.0.0", Update: true},
		{Kind: "theme", Name: "live", Version: "1.0.0", Installed: true, InstalledVersion: "1.0.0"},
		{Kind: "theme", Name: "liveup", Version: "2.0.0", Installed: true, InstalledVersion: "1.0.0", Update: true},
		{Kind: "theme", Name: "livebare", Version: "1.0.0", Installed: true, InstalledVersion: "1.0.0"},
		{Kind: "theme", Name: "default", Version: "1.0.0"},
		{Kind: "plugin", Name: "seo", Version: "1.0.0"},
	}}
}

func themeDetailRig(t *testing.T, view RegistryView) *rig {
	t.Helper()
	g, fp := browseRig(t, view)
	fp.themes = []ThemeInfo{
		{Name: "live", Active: true, HasSettings: true},
		{Name: "liveup", Active: true, HasSettings: true},
		{Name: "livebare", Active: true},
		{Name: "have"}, {Name: "old"},
	}
	return g
}

func getThemeAbout(g *rig, name string, hdr map[string]string) (int, string) {
	g.t.Helper()
	rec := g.do("GET", "/admin/theme/themes/"+name+"/about", "owner-token", nil, hdr)
	return rec.Code, rec.Body.String()
}

func TestThemeBrowseDetailInstall(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	code, body := getThemeAbout(g, "fresh", nil)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"By Acme", "First-party", "First paragraph.", "Second &lt;b&gt;paragraph&lt;/b&gt;.", "Does two",
		`<img class="hm-themebrowse__shot" src="https://registry.example.test/screenshots/fresh.jpg" alt="">`,
		"Installing adds the theme to your Themes list. Your site does not change until you activate it.",
		`action="/admin/theme/themes/fetch"`, `name="name" value="fresh"`, ">Install<",
		"<dt>Site type</dt><dd>Docs</dd>", "Dark mode, Works without JavaScript, Visual builder", "2 KB", "abc1234",
		`href="https://demo.example.test/fresh"`, "Preview demo"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "javascript:") {
		t.Error("an unsafe address is rendered")
	}
	if got := strings.Join(trailLabels(t, body), " / "); got != "Home / Theme / Themes / Browse / Fresh" {
		t.Errorf("crumbs %q", got)
	}
	if !strings.Contains(body, `href="/admin/theme/browse"`) {
		t.Error("crumb back to Browse missing")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestThemeBrowseDetailUpdate(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	_, body := getThemeAbout(g, "old", nil)
	if !strings.Contains(body, "Review update") || !strings.Contains(body, `action="/admin/theme/themes/fetch"`) || strings.Contains(body, "Activate…") {
		t.Error("update available (not live): want Review update, no Activate")
	}
}

func TestThemeBrowseDetailInstalled(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	_, body := getThemeAbout(g, "have", nil)
	for _, w := range []string{`href="/admin/theme/themes/have/activate"`, "Activate…", "Installed. Activate it when you are ready; your content stays as it is."} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Contains(body, "/theme/themes/fetch") || strings.Contains(body, "Preview demo") {
		t.Error("installed theme: no install form, no demo without a homepage")
	}
}

func TestThemeBrowseDetailLive(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	_, body := getThemeAbout(g, "live", nil)
	if !strings.Contains(body, "This is your live theme.") || !strings.Contains(body, `href="/admin/theme/themes/live/settings"`) || strings.Contains(body, "Activate…") || strings.Contains(body, "Review update") {
		t.Error("live theme: want Settings only")
	}
	_, bare := getThemeAbout(g, "livebare", nil)
	if !strings.Contains(bare, "This is your live theme.") || strings.Contains(bare, "/themes/livebare/settings") || strings.Contains(bare, "Activate…") || strings.Contains(bare, `action="/admin/theme/themes/fetch"`) {
		t.Error("live theme without settings or update has no primary")
	}
}

func TestThemeBrowseDetailLiveWithUpdate(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	_, body := getThemeAbout(g, "liveup", nil)
	if !strings.Contains(body, "Review update") || !strings.Contains(body, `action="/admin/theme/themes/fetch"`) || !strings.Contains(body, `href="/admin/theme/themes/liveup/settings"`) {
		t.Error("live with update: want Review update primary and Settings secondary")
	}
	if strings.Index(body, "Settings") > strings.Index(body, "Review update") {
		t.Error("Settings (secondary) must come before the primary")
	}
}

func TestThemeBrowseDetailPreviewDemoOnlyWithHomepage(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	_, with := getThemeAbout(g, "fresh", nil)
	if !strings.Contains(with, `href="https://demo.example.test/fresh"`) || !strings.Contains(with, `target="_blank"`) || !strings.Contains(with, "noopener") {
		t.Error("preview demo link missing or not opening safely")
	}
	_, unsafe := getThemeAbout(g, "bare", nil)
	if strings.Contains(unsafe, "Preview demo") {
		t.Error("an unsafe homepage must not become a preview link")
	}
}

func TestThemeBrowseDetailNotFoundAndStates(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	for _, name := range []string{"missing", "seo", "default"} {
		code, body := getThemeAbout(g, name, nil)
		if code != 404 || !strings.Contains(body, "Theme not found") || !strings.Contains(body, `href="/admin/theme/browse"`) || !strings.Contains(body, "Back to browse") {
			t.Errorf("%s: status %d, want 404 with a way back to Browse", name, code)
		}
	}
	bad := themeDetailRig(t, RegistryView{Err: "down", Host: themeRegistryHost})
	if code, body := getThemeAbout(bad, "fresh", nil); code != 503 || !strings.Contains(body, "not reachable") || !strings.Contains(body, "Installed themes keep working") {
		t.Errorf("unreachable: %d", code)
	}
	off := themeDetailRig(t, RegistryView{Off: true})
	if code, body := getThemeAbout(off, "fresh", nil); code != 404 || !strings.Contains(body, "No registry is set up") || !strings.Contains(body, "Back to themes") {
		t.Errorf("registry off: %d", code)
	}
}

func TestThemeBrowseDetailFragment(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	_, full := getThemeAbout(g, "fresh", nil)
	code, frag := getThemeAbout(g, "fresh", map[string]string{"HX-Request": "true", "HX-Target": "plugin-panel"})
	if code != 200 || strings.Contains(frag, "<html") || strings.Contains(frag, "hm-sidebar") || !strings.Contains(frag, "First paragraph.") {
		t.Fatalf("fragment status %d", code)
	}
	if !strings.Contains(full, "<html") {
		t.Error("full page lacks the shell")
	}
	if strings.Contains(full, "data-hm-panel-actions") || !strings.Contains(full, `class="hm-row"><a class="hm-btn hm-btn--secondary" href="https://demo.example.test/fresh"`) {
		t.Error("the no-JS page keeps its actions in the page header")
	}
	i := strings.Index(frag, "data-hm-panel-actions")
	if i < 0 || strings.Index(frag, "hm-pageheader") > i || strings.Count(frag, "Preview demo") != 1 || strings.Count(frag, `action="/admin/theme/themes/fetch"`) != 1 {
		t.Error("the panel renders its actions once, in the block the footer takes")
	}
	if !strings.Contains(frag[i:], "Preview demo") || !strings.Contains(frag[i:], ">Install<") {
		t.Error("Preview demo and the primary belong in the footer block")
	}
}

// Features show even when the theme has no About text.
func TestThemeBrowseDetailFeaturesWithoutAbout(t *testing.T) {
	t.Parallel()

	g := themeDetailRig(t, themeDetailView())
	_, body := getThemeAbout(g, "featonly", nil)
	if !strings.Contains(body, `aria-label="Features"`) || !strings.Contains(body, "Only a feature") {
		t.Error("features list missing without about text")
	}
}
