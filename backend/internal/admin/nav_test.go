package admin

import (
	"strings"
	"testing"
)

func labels(es []NavEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Label)
	}
	return out
}

func current(es []NavEntry) (label, cur string) {
	for _, e := range es {
		if e.Current != "" {
			return e.Label, e.Current
		}
	}
	return "", ""
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// editorPluginNav is DefaultNav with the Plugins item wired the way New wires
// it: visible to an editor only when first (a plugin UI's name) is set.
func editorPluginNav(first string) *Nav {
	n := DefaultNav()
	n.update(pluginsID, func(it *NavItem) {
		it.Roles = nil
		it.Visible = func(role string) bool { return role == "owner" || first != "" }
		it.HrefFor = func(role string) string {
			if role == "owner" {
				return "/admin/plugins"
			}
			return "/admin/plugins/" + first + "/settings"
		}
	})
	return n
}

func TestNavOwnerSeesSixPrimaryItems(t *testing.T) {
	t.Parallel()

	got := editorPluginNav("hreflang").For("owner", "/admin/entities")
	if want := []string{"Overview", "Entities", "Analytics", "Plugins", "Theme", "Settings"}; !eq(labels(got), want) {
		t.Fatalf("owner nav %v, want %v", labels(got), want)
	}
	if got[3].Href != "/admin/plugins" {
		t.Fatalf("owner Plugins href %q", got[3].Href)
	}
}

func TestNavEditor(t *testing.T) {
	t.Parallel()

	// No plugin page for the editor: Plugins is hidden. Settings shows because
	// the editor reaches Media and more.
	for _, n := range []*Nav{DefaultNav(), editorPluginNav("")} {
		if got := n.For("editor", "/admin/entities"); !eq(labels(got), []string{"Overview", "Entities", "Analytics", "Theme", "Settings"}) {
			t.Fatalf("editor nav %v", labels(got))
		}
	}
	got := editorPluginNav("hreflang").For("editor", "/admin/entities")
	if want := []string{"Overview", "Entities", "Analytics", "Plugins", "Theme", "Settings"}; !eq(labels(got), want) {
		t.Fatalf("editor nav with a plugin page %v, want %v", labels(got), want)
	}
	if pl := got[3]; pl.Href != "/admin/plugins/hreflang/settings" {
		t.Fatalf("editor Plugins entry must link its first reachable plugin: %+v", pl)
	}
}

func TestNavActive(t *testing.T) {
	t.Parallel()

	n := editorPluginNav("hreflang")
	for _, tc := range []struct{ path, label, cur string }{
		{"/admin", "Overview", "page"},
		{"/admin/", "Overview", "page"},
		{"/admin/entities", "Entities", "page"},
		{"/admin/entities/12/edit", "Entities", "page"},
		{"/admin/analytics/referrals", "Analytics", "page"},
		{"/admin/analytics/health", "Analytics", "page"},
		{"/admin/analytics/google", "Analytics", "page"},
		{"/admin/analytics/agents/gptbot", "Analytics", "page"},
		{"/admin/plugins/hreflang/settings", "Plugins", "page"},
		{"/admin/plugins", "Plugins", "page"},
		{"/admin/theme", "Theme", "page"},
		{"/admin/theme/menus/3", "Theme", "page"},
		{"/admin/theme/blocks", "Theme", "page"},
		{"/admin/settings/api", "Settings", "page"},
		{"/admin/settings/media", "Settings", "page"},
		{"/admin/media/3", "Settings", "page"},
		{"/admin/people", "Entities", "page"},
		{"/admin/people/new", "Entities", "page"},
		{"/admin/people/duplicates", "Entities", "page"},
		{"/admin/entitiesx", "", ""},
		{"/admin/profile", "", ""},
	} {
		if l, c := current(n.For("owner", tc.path)); l != tc.label || c != tc.cur {
			t.Errorf("%s: current = %q %q, want %q %q", tc.path, l, c, tc.label, tc.cur)
		}
	}
}

func TestSidebarFooterControls(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	body := g.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		`<nav class="hm-sidebar" id="hm-sidebar" aria-label="Admin">`,
		`href="/admin/profile" aria-label="Owner Olga, your profile" data-tooltip="Your profile"`,
		`<form method="post" action="/admin/theme">`,
		`data-hm-theme-toggle aria-label="Switch theme" data-tooltip="Switch theme"`,
		`<form method="post" action="/admin/logout">`,
		`aria-label="Sign out" data-tooltip="Sign out"`,
		`<span class="hm-avatar" data-initials="OO" aria-hidden="true"></span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sidebar lacks %q", want)
		}
	}
	if strings.Contains(body, "data-hm-menu") {
		t.Error("the sidebar still carries the old disclosure")
	}
	if n := strings.Count(body, `class="hm-sidebar__foot"`); n != 1 {
		t.Errorf("%d footers", n)
	}
}

func TestUserInitials(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{"Hana Sato": "HS", "ana maria de la cruz": "AC", "Cher": "CH", "": "?", "  ": "?", "Mary-Jane Watson": "MW"} {
		if got := (&CurrentUser{Name: name}).Initials(); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}

// Plugins never nests a child list, and a top-level item without an icon
// still gets one.
func TestSidebarPluginsNeverNests(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) {
		d.Nav = DefaultNav()
		d.Nav.Register(NavItem{ID: "bare", Label: "Bare", Href: "/admin/bare", Order: 9})
	})
	body := g.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, `aria-label="Plugins pages"`) {
		t.Error("Plugins renders a child list")
	}
	if !strings.Contains(body, `href="/admin/bare"><svg`) {
		t.Error("a top-level item without an icon renders no icon")
	}
}

func TestGroupNav(t *testing.T) {
	t.Parallel()

	shape := func(gs []NavGroup) string {
		var parts []string
		for _, g := range gs {
			parts = append(parts, g.Label+":"+strings.Join(labels(g.Items), ","))
		}
		return strings.Join(parts, "|")
	}
	for _, tc := range []struct {
		name string
		nav  *Nav
		role string
		want string
	}{
		{"owner", editorPluginNav("hreflang"), "owner", ":Overview|Site:Entities,Analytics|Customize:Plugins,Theme,Settings"},
		{"editor", DefaultNav(), "editor", ":Overview|Site:Entities,Analytics|Customize:Theme,Settings"},
	} {
		if got := shape(GroupNav(tc.nav.For(tc.role, "/admin"))); got != tc.want {
			t.Errorf("%s groups %q, want %q", tc.name, got, tc.want)
		}
	}
	// A group whose items are all hidden leaves no trace.
	n := DefaultNav()
	n.update("analytics", func(it *NavItem) { it.Visible = func(string) bool { return false } })
	n.update("entities", func(it *NavItem) { it.Visible = func(string) bool { return false } })
	if got := shape(GroupNav(n.For("owner", "/admin"))); got != ":Overview|Customize:Plugins,Theme,Settings" {
		t.Errorf("hidden group still listed: %q", got)
	}
}
