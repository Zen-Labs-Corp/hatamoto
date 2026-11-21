package admin

import (
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/adminpagestate"
)

func modulePageDef(id, icon string) adminpagestate.Page {
	return adminpagestate.Page{ID: id, Label: "Example " + id, Desc: "A module page.", Icon: icon, Templates: fstest.MapFS{}}
}

func moduleView(a *Admin, role, section string) *View {
	return &View{User: &CurrentUser{ID: "u-" + role, Role: role}, Section: section, adminPath: a.adminPath, areas: a.areas}
}

func TestModuleAreaCommunityUnchanged(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	if &g.a.areas[0] != &adminAreas[0] || g.a.settings != settingsArea {
		t.Fatal("a community Admin must use the shared areas")
	}
	for _, sec := range settingsFor("/admin", roleViewer("owner")) {
		if sec.ID == "module-example" {
			t.Fatal("global settingsFor must not see module sections")
		}
	}
}

func TestModuleAreaOwnerSubnav(t *testing.T) {
	t.Parallel()
	g := newRig(t, func(d *Deps) { d.ModulePages = []adminpagestate.Page{modulePageDef("example", "nope")} })
	section, tab := areaLocateAmong(g.a.areas, "/admin", "/admin/settings/modules/example/save")
	if section != "module-example" || tab != "module-example" {
		t.Fatalf("locate = %q %q", section, tab)
	}
	v := moduleView(g.a, "owner", section)
	nav := v.AreaNav()
	last := nav[len(nav)-1]
	if last.ID != "module-example" || last.Group != "System" || last.Href != "/admin/settings/modules/example" {
		t.Fatalf("last section = %+v", last)
	}
	if last.Icon != "puzzle" {
		t.Fatalf("unknown icon must fall back to puzzle, got %q", last.Icon)
	}
	if v.CurrentSection() == nil {
		t.Fatal("owner must see the module section as current")
	}
	if got := nav[len(nav)-2].Group; got != "System" {
		t.Fatalf("core's last section group = %q", got)
	}
	crumbs := defaultAreaCrumbsIn(g.a.areas, "/admin", "/admin/settings/modules/example/save", section, tab, "Save")
	if len(crumbs) == 0 || crumbs[len(crumbs)-1].Label != "Save" {
		t.Fatalf("crumbs = %+v", crumbs)
	}
}

func TestModuleAreaEditorHasNoTab(t *testing.T) {
	t.Parallel()
	g := newRig(t, func(d *Deps) { d.ModulePages = []adminpagestate.Page{modulePageDef("example", "")} })
	for _, s := range moduleView(g.a, "editor", "general").AreaNav() {
		if s.ID == "module-example" {
			t.Fatal("editor must not see a module tab")
		}
	}
	if v := moduleView(g.a, "owner", "module-example"); v.AreaNav()[len(v.AreaNav())-1].Icon != "puzzle" {
		t.Fatal("empty icon must fall back to puzzle")
	}
}
