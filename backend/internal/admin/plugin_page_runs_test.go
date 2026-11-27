package admin

import (
	"html"
	"strings"
	"testing"
)

func appBody(t *testing.T, g *rig, token string) string {
	t.Helper()
	rec := g.do("GET", "/admin/plugins/hello/app", token, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("app as %s: %d", token, rec.Code)
	}
	return html.UnescapeString(rec.Body.String())
}

func TestPluginPageCarriesRunsForOwner(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newRunsRig(t)
	body := appBody(t, g, "owner-token")
	for _, want := range []string{
		`data-runs="[{"name":"import","title":"Import","inputs":["site","user"],"start":"/admin/plugins/hello/runs/import/start","host":"old.example.com"}]"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("owner page lacks %s", want)
		}
	}
}

func TestPluginPageHidesRunsFromEditor(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newRunsRig(t)
	p := runPlugin
	p.UI = &PluginUIInfo{Title: "Hello board", Entry: "index.html", Roles: []string{"owner", "editor"}}
	g.a.deps.Growth.Plugins = &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{p}}
	body := appBody(t, g, "editor-token")
	if !strings.Contains(body, "data-hm-pluginframe") {
		t.Fatal("editor should see the plugin frame")
	}
	if strings.Contains(body, "data-runs") {
		t.Error("an editor's page must not carry the runs it cannot start")
	}
}

func TestPluginPageWithoutRunsHasNoRunsAttr(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newRunsRig(t)
	p := runPlugin
	p.Runs = nil
	g.a.deps.Growth.Plugins = &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{p}}
	body := appBody(t, g, "owner-token")
	if !strings.Contains(body, "data-hm-pluginframe") || strings.Contains(body, "data-runs") {
		t.Error("a plugin without runs has a frame and no data-runs")
	}
}
