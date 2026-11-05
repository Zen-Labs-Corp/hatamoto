package admin

import (
	"strings"
	"testing"
)

func TestSettingsNavHasChangesUnderBackupsRecovery(t *testing.T) {
	t.Parallel()

	var found bool
	for _, s := range SettingsFor("owner") {
		if s.ID != "backups-recovery" {
			continue
		}
		if s.Group != "System" {
			t.Errorf("Backups & recovery group %q, want System", s.Group)
		}
		for _, tab := range s.Tabs {
			if tab.ID != "changes" {
				continue
			}
			found = true
			if !settingsLive["changes"] || !strings.HasSuffix(tab.Href, "/settings/changes") || len(tab.Roles) != 1 || tab.Roles[0] != "owner" {
				t.Errorf("Changes tab %+v, want a live owner-only tab at /settings/changes", tab)
			}
		}
	}
	if !found {
		t.Fatal("owner Backups & recovery has no Changes tab")
	}
	for _, s := range SettingsFor("editor") {
		if s.ID == "backups-recovery" {
			t.Error("editor sees Backups & recovery")
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
