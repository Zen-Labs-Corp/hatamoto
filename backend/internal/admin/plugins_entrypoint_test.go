package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

// The package security page has one way in: the Plugins overflow menu. No other
// page, banner or dialog of the plugin and theme screens links to it.
func TestPluginsSecurityOnlyFromOverflow(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	const link = "/admin/plugins/security"
	var inMenu, total int
	for _, path := range []string{"/admin/plugins", "/admin/theme/themes", "/admin/theme/themes/sunrise/settings", "/admin/plugins/upload"} {
		rec := g.do("GET", path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		body := rec.Body.String()
		total += strings.Count(body, link)
		if i := strings.Index(body, `id="hm-overflow"`); i >= 0 {
			menu := body[i:]
			menu = menu[:strings.Index(menu, "</div>")]
			inMenu += strings.Count(menu, link)
			if path != "/admin/plugins" && strings.Contains(menu, link) {
				t.Errorf("%s: its overflow menu links to package security", path)
			}
		}
	}
	if total != 1 || inMenu != 1 {
		t.Errorf("package security is linked %d times, %d of them in the Plugins overflow menu; want exactly one, in the menu", total, inMenu)
	}
}

// The overflow menu shows a second, muted line under an item that has a hint.
func TestPartialPageheaderOverflowHint(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	out := checkPartial(t, a, "hm/pageheader", "overflow-hint", map[string]any{
		"CSRF": "tok", "Title": "Plugins",
		"Overflow": []ui.Action{
			{Label: "Check for updates", Post: "/admin/plugins/check-updates", Hint: "Last checked 3 hours ago"},
			{Label: "Package security", Href: "/admin/plugins/security"},
		},
	})
	if !strings.Contains(out, `<span class="hm-menu__hint">Last checked 3 hours ago</span>`) || strings.Count(out, "hm-menu__hint") != 1 {
		t.Errorf("hint markup: %s", out)
	}
}

func TestPluginPageTrails(t *testing.T) {
	t.Parallel()

	fp := &fakePackages{registry: RegistryView{Host: "registry.example", Entries: []RegistryEntry{{Kind: "plugin", Name: "gallery", Title: "Gallery", Version: "1.0.0"}}}}
	g := newRig(t, func(d *Deps) {
		d.Growth.Packages, d.Growth.Plugins = fp, &fakePlugins{settings: map[string]map[string]any{}}
	})
	for path, want := range map[string]string{
		"/admin/plugins/browse":        "Home > Plugins > Browse",
		"/admin/plugins/gallery/about": "Home > Plugins > Browse > Gallery",
		"/admin/plugins/security":      "Home > Plugins > Package security",
	} {
		body := g.do("GET", path, "owner-token", nil, nil).Body.String()
		if got := strings.Join(trailLabels(t, body), " > "); got != want {
			t.Errorf("%s: trail %q, want %q", path, got, want)
		}
	}
}
