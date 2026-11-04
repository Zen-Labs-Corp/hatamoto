package admin

import (
	"strings"
	"testing"
)

func TestSettingsNavHasChangesUnderSystem(t *testing.T) {
	t.Parallel()

	var found bool
	for _, s := range SettingsFor("owner") {
		if s.ID != "changes" {
			continue
		}
		found = true
		if s.Group != "System" {
			t.Errorf("Changes group %q, want System", s.Group)
		}
		if len(s.Tabs) != 1 || !settingsLive["changes"] || !strings.HasSuffix(s.Tabs[0].Href, "/settings/changes") || len(s.Tabs[0].Roles) != 1 || s.Tabs[0].Roles[0] != "owner" {
			t.Errorf("Changes tabs %+v, want one live tab at /settings/changes", s.Tabs)
		}
	}
	if !found {
		t.Fatal("owner Settings has no Changes section")
	}
	for _, s := range SettingsFor("editor") {
		if s.ID == "changes" {
			t.Error("editor sees Changes")
		}
	}
}

func TestPluginRunsTabOnlyForPluginsWithRuns(t *testing.T) {
	t.Parallel()

	g, fp := newPluginRig(t)
	fp.list[0].Runs = []RunDecl{{Name: "import", Title: "Import"}}
	runs := `href="/admin/plugins/hello/runs"`

	for _, path := range []string{"/admin/plugins/hello/settings", "/admin/plugins/hello/permissions"} {
		if body := g.do("GET", path, "owner-token", nil, nil).Body.String(); !strings.Contains(body, runs) {
			t.Errorf("%s: owner lacks the Runs tab", path)
		}
	}
	if body := g.do("GET", "/admin/plugins/hello/settings", "editor-token", nil, nil).Body.String(); strings.Contains(body, runs) {
		t.Error("editor sees the Runs tab")
	}
	fp.list[0].Runs = nil
	if body := g.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil).Body.String(); strings.Contains(body, runs) {
		t.Error("a plugin without runs shows the Runs tab")
	}
}
