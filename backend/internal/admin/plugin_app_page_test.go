package admin

import (
	"strings"
	"testing"
)

func TestPluginHomeTab(t *testing.T) {
	ui := &PluginUIInfo{Title: "Board", Roles: []string{"owner", "editor"}}
	cases := []struct {
		name             string
		p                PluginInfo
		role             string
		manage, uiAccess bool
		want             string
	}{
		{"manager in roles", PluginInfo{Loaded: true, UI: ui}, "owner", true, false, "app"},
		{"ui access only", PluginInfo{Loaded: true, UI: ui}, "editor", false, true, "app"},
		{"neither right", PluginInfo{Loaded: true, UI: ui}, "owner", false, false, "settings"},
		{"role not in roles", PluginInfo{Loaded: true, UI: ui}, "author", true, true, "settings"},
		{"not loaded", PluginInfo{UI: ui}, "owner", true, true, "settings"},
		{"no ui", PluginInfo{Loaded: true}, "owner", true, true, "settings"},
		{"empty roles is owner only", PluginInfo{Loaded: true, UI: &PluginUIInfo{}}, "owner", true, false, "app"},
		{"empty roles excludes editor", PluginInfo{Loaded: true, UI: &PluginUIInfo{}}, "editor", false, true, "settings"},
	}
	for _, c := range cases {
		if got := pluginHomeTab(c.p, c.role, c.manage, c.uiAccess); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestScreenLabel(t *testing.T) {
	for _, c := range []struct {
		p    PluginInfo
		want string
	}{
		{PluginInfo{Name: "hello", UI: &PluginUIInfo{Title: "Board"}}, "Board"},
		{PluginInfo{Name: "hello", UI: &PluginUIInfo{Title: "  "}}, "hello"},
		{PluginInfo{Name: "hello"}, "hello"},
		{PluginInfo{Name: "hello", UI: &PluginUIInfo{Title: "Settings"}}, "hello"},
		{PluginInfo{Name: "hello", UI: &PluginUIInfo{Title: " permissions "}}, "hello"},
		{PluginInfo{Name: "hello", UI: &PluginUIInfo{Title: "RUNS"}}, "hello"},
		{PluginInfo{Name: "hello", UI: &PluginUIInfo{Title: "Automatic Updates"}}, "hello"},
	} {
		if got := screenLabel(c.p); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.p, got, c.want)
		}
	}
}

func TestPluginAppPage(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list, PluginInfo{Name: "edonly", Version: "1.0.0", Loaded: true, UI: &PluginUIInfo{Title: "E", Roles: []string{"editor"}}})

	rec := g.do("GET", "/admin/plugins/hello/app", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("owner app: %d", rec.Code)
	}
	for _, want := range []string{`data-hm-pluginframe`, `data-fit="1"`, `data-src="/plugin-ui/hello/index.html?t=`, `sandbox="allow-scripts"`, `data-width="wide"`} {
		if !strings.Contains(body, want) {
			t.Errorf("owner app page lacks %q", want)
		}
	}
	if strings.Contains(body, `id="plugin-page"`) || strings.Contains(body, `data-width="narrow"`) {
		t.Error("the screen tab is a wide page without a card")
	}
	a11yOK(t, body)

	body = g.do("GET", "/admin/plugins/board/app", "editor-token", nil, nil).Body.String()
	if !strings.Contains(body, `data-functions=""`) || !strings.Contains(body, "data-hm-pluginframe") {
		t.Errorf("editor app page: no frame or host functions leaked:\n%s", body)
	}
	for _, owned := range []string{"hm-tabs", "Turn off", "/admin/plugins/board/permissions"} {
		if strings.Contains(body, owned) {
			t.Errorf("an editor's screen must not carry %q", owned)
		}
	}

	for _, c := range []struct{ token, path string }{
		{"editor-token", "/admin/plugins/ownerui/app"},
		{"editor-token", "/admin/plugins/devonly/app"},
		{"owner-token", "/admin/plugins/devonly/app"},
		{"owner-token", "/admin/plugins/ghost/app"},
		{"owner-token", "/admin/plugins/edonly/app"},
	} {
		if rec := g.do("GET", c.path, c.token, nil, nil); rec.Code != 404 {
			t.Errorf("%s %s: %d, want 404", c.token, c.path, rec.Code)
		}
	}
}

func TestPluginTabsStartWithTheScreen(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list, PluginInfo{Name: "untitled", Version: "1.0.0", Loaded: true, UI: &PluginUIInfo{}})

	body := g.do("GET", "/admin/plugins/hello/app", "owner-token", nil, nil).Body.String()
	nav := body[strings.Index(body, `aria-label="Plugin sections"`):]
	first := nav[strings.Index(nav, "<a "):]
	if !strings.HasPrefix(first, `<a `) || !strings.Contains(first[:strings.Index(first, "</a>")], `href="/admin/plugins/hello/app"`) ||
		!strings.Contains(first[:strings.Index(first, "</a>")], `aria-current="page"`) {
		t.Errorf("the first tab must be the active screen tab:\n%.400s", first)
	}
	i, j, k := strings.Index(nav, "Hello board"), strings.Index(nav, ">Settings<"), strings.Index(nav, ">Permissions<")
	if i < 0 || i > j || j > k {
		t.Errorf("tab order: screen %d, settings %d, permissions %d", i, j, k)
	}

	if body := g.do("GET", "/admin/plugins/devonly/settings", "owner-token", nil, nil).Body.String(); strings.Contains(body, "/devonly/app") {
		t.Error("a plugin without ui has no screen tab")
	}
	if body := g.do("GET", "/admin/plugins/ownerui/settings", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Owner tool") {
		t.Error("the screen tab carries the ui title")
	}
	if body := g.do("GET", "/admin/plugins/untitled/settings", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `href="/admin/plugins/untitled/app"`) || !strings.Contains(body, ">untitled<") {
		t.Errorf("an untitled screen falls back to the plugin name:\n%s", body)
	}
}
