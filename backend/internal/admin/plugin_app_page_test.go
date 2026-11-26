package admin

import "testing"

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
	} {
		if got := screenLabel(c.p); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.p, got, c.want)
		}
	}
}
