package admin

import (
	"slices"
	"strings"
	"testing"
)

const richSchemaJSON = `{"type":"object","properties":{
 "body":{"type":"string","title":"Body","format":"richtext"},
 "hero_image":{"type":"string","title":"Hero","x-media":true},
 "links":{"type":"array","title":"Links","items":{"type":"object","properties":{"label":{"type":"string","title":"Label"}}}}}}`

// markupNeeds is what each hook in a page's HTML requires to work: a module
// script (module name) or a classic vendored script (file prefix).
var markupNeeds = []struct{ hook, module, script string }{
	{`data-hm-editor="tiptap"`, "editors", ""},
	{`data-hm-editor="html"`, "editors", ""},
	{`data-hm-sortable`, "repeater", "/admin/_/vendor/sortable-"},
	{`data-hm-repeater`, "repeater", ""},
	{`data-hm-media-picker`, "content", ""},
}

// assertHooksLoaded fails when body carries a data-hm-* hook without the module
// or script tag that gives it behaviour.
func assertHooksLoaded(t *testing.T, name, body string) {
	t.Helper()
	scripts, _ := moduleTags(body)
	for _, n := range markupNeeds {
		if !strings.Contains(body, n.hook) {
			continue
		}
		if !slices.Contains(scripts, n.module) {
			t.Errorf("%s: %s present but module %s is not loaded: %v", name, n.hook, n.module, scripts)
		}
		if n.script != "" && !strings.Contains(body, `<script nonce="`) || !strings.Contains(body, n.script) {
			t.Errorf("%s: %s present but script %s is not loaded", name, n.hook, n.script)
		}
	}
}

func TestSchemaformPagesLoadTheirHooks(t *testing.T) {
	t.Parallel()

	g, pk, fp := newPackageRig(t)
	pk.themeSchema = []byte(richSchemaJSON)
	for i := range fp.list {
		if fp.list[i].Name == "hello" {
			fp.list[i].SettingsSchema = []byte(richSchemaJSON)
		}
	}
	for _, p := range []string{"/admin/theme/themes/sunrise/settings", "/admin/plugins/hello/settings"} {
		body := g.do("GET", p, "owner-token", nil, nil).Body.String()
		for _, n := range markupNeeds[:1] {
			if !strings.Contains(body, n.hook) {
				t.Fatalf("%s: schema did not render %s", p, n.hook)
			}
		}
		if !strings.Contains(body, "data-hm-sortable") || !strings.Contains(body, "data-hm-media-picker") {
			t.Fatalf("%s: schema did not render repeater and image hooks", p)
		}
		assertHooksLoaded(t, p, body)
	}

	// A plugin with an admin UI loads the frame module on its Settings tab.
	body := g.do("GET", "/admin/plugins/board/settings", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "data-hm-pluginframe") {
		t.Fatal("the plugin page rendered no frame host")
	}
	assertHooksLoaded(t, "/admin/plugins/board/settings", body)

	c := newContentRig(t)
	for _, p := range []string{"/admin/entities/" + fid(14).String()} {
		body := c.do("GET", p, "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, `data-hm-editor="tiptap"`) {
			t.Fatalf("%s: no richtext field rendered", p)
		}
		assertHooksLoaded(t, p, body)
	}
}
