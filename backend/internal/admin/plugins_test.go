package admin

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/schemaform"
)

type fakePlugins struct {
	list     []PluginInfo
	settings map[string]map[string]any
	saved    []map[string]any
	secrets  map[string]map[string]bool
}

func (f *fakePlugins) Plugins() []PluginInfo { return f.list }
func (f *fakePlugins) Grant(_ context.Context, name string, perms []string) error {
	for i := range f.list {
		if f.list[i].Name != name {
			continue
		}
		for _, p := range perms {
			if p == "site.scripts" {
				return ErrCommunityGrant
			}
			if !slices.Contains(f.list[i].Permissions, p) {
				return ErrBadGrant
			}
		}
		f.list[i].Granted = perms
		return nil
	}
	return ErrPluginNotFound
}
func (f *fakePlugins) Settings(_ context.Context, name string) (map[string]any, error) {
	return f.settings[name], nil
}
func (f *fakePlugins) StoredSecrets(_ context.Context, name string) (map[string]bool, error) {
	return f.secrets[name], nil
}
func (f *fakePlugins) SaveSettings(_ context.Context, name string, v map[string]any) (schemaform.Errors, error) {
	// The real one validates after restoring write-only values; the form
	// handler no longer pre-checks the schema, so "required" comes from here.
	if s, _ := v["site_id"].(string); s == "" {
		return schemaform.Errors{"site_id": {"This is required"}}, nil
	}
	if s, _ := v["api_key"].(string); s == "bad" {
		return schemaform.Errors{"api_key": {"This key is not valid"}}, nil
	}
	f.saved = append(f.saved, v)
	f.settings[name] = v
	return nil, nil
}

const hookSchema = `{"type":"object","required":["site_id"],"properties":{
 "site_id":{"type":"string","title":"Site ID"},
 "api_key":{"type":"string","title":"API key"},
 "enabled":{"type":"boolean","title":"Enabled"}}}`

func newPluginRig(t *testing.T) (*rig, *fakePlugins) {
	t.Helper()
	fp := &fakePlugins{settings: map[string]map[string]any{"hello": {"site_id": "abc"}}, list: []PluginInfo{
		{Name: "hello", Version: "1.2.0", SDK: "1", Description: "Says <hello>", Loaded: true, Hooks: []string{"on_entity_published"}, Permissions: []string{"kv", "mail.send"}, Granted: []string{"kv"},
			Routes: []string{"GET /p/hello/ping (public)"}, Cron: []string{"sync every 1h"}, AllowedHosts: []string{"api.example.com"}, SettingsSchema: []byte(hookSchema)},
		{Name: "broken", Error: "wasm: bad module"},
	}}
	return newRig(t, func(d *Deps) { d.Growth.Plugins = fp }), fp
}

func TestPluginsPage(t *testing.T) {
	t.Parallel()

	g, _ := newPluginRig(t)
	rec := g.do("GET", "/admin/plugins", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"hello", "Says &lt;hello&gt;", "Active", "Failed", `href="/admin/plugins/hello/settings"`, `Settings<span class="hm-visually-hidden"> for hello</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, gone := range []string{"hm-stats", "hm-plugin ", "on_entity_published", "api.example.com"} {
		if strings.Contains(body, gone) {
			t.Errorf("the list still carries %q", gone)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if rec := g.do("GET", "/admin/plugins", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

// The old webhooks plugin row carries a notice and a Remove link to the owner-only
// uninstall confirmation; the wording differs per case and claims nothing it cannot know.
func TestPluginsPageLegacyWebhooksRow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		pkg     *PackageMeta
		want    []string
		notWant []string
	}{
		{"first-party signed, import recorded", &PackageMeta{Signed: true, Signer: "adea8e00f5ac74ca", FirstParty: true, WebhooksImported: true},
			[]string{"Webhooks are now built in under", `href="/admin/settings/webhooks">Settings &gt; Webhooks</a>`, "This old plugin is not needed any more. Until you remove it, both it and core may send each webhook."},
			[]string{"not moved over automatically", "were imported", "Until then both may send", "has not finished"}},
		{"first-party signed, import not proven", &PackageMeta{Signed: true, Signer: "adea8e00f5ac74ca", FirstParty: true},
			[]string{"has not finished. Restart the server to retry; remove the plugin only after its webhooks appear there.", `href="/admin/settings/webhooks">Settings &gt; Webhooks</a>`},
			[]string{"not needed any more", "not moved over automatically", "Until then both may send", ">Remove<", `href="/admin/plugins/webhooks/uninstall"`}},
		{"other package", &PackageMeta{Signed: true, Signer: "ffff0000ffff0000"},
			[]string{"A plugin named webhooks is still installed", "this plugin was not moved over automatically", `href="/admin/settings/webhooks">Settings &gt; Webhooks</a>`, "Until then both may send."},
			[]string{"This old plugin is not needed any more", "were imported"}},
		{"not a package", nil,
			[]string{"this plugin was not moved over automatically"},
			[]string{"This old plugin is not needed any more"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, fp := newPluginRig(t)
			fp.list = append(fp.list, PluginInfo{Name: "webhooks", Version: "1.0.0", Description: "Old webhooks", Loaded: true, Package: tc.pkg})
			rec := g.do("GET", "/admin/plugins", "owner-token", nil, nil)
			body := rec.Body.String()
			if rec.Code != 200 {
				t.Fatalf("status %d", rec.Code)
			}
			common := []string{`id="plugin-webhooks-builtin"`, `<span class="hm-muted">v1.0.0</span>`}
			if !strings.Contains(tc.name, "not proven") {
				common = append(common, `href="/admin/plugins/webhooks/uninstall"`, `aria-label="Remove the webhooks plugin"`)
			}
			for _, want := range append(tc.want, common...) {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, gone := range tc.notWant {
				if strings.Contains(body, gone) {
					t.Errorf("unexpected %q", gone)
				}
			}
			// Only the webhooks row is marked: one notice, one Remove; every row shows its version.
			if n := strings.Count(body, "hm-callout--warning"); n != 1 {
				t.Errorf("%d warning notices, want 1", n)
			}
			wantRemove := 1
			if strings.Contains(tc.name, "not proven") {
				wantRemove = 0
			}
			if n := strings.Count(body, ">Remove<"); n != wantRemove {
				t.Errorf("%d Remove links, want %d", n, wantRemove)
			}
			if !strings.Contains(body, `<span class="hm-muted">v1.2.0</span>`) {
				t.Error("a normal row does not show its version")
			}
			if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
				t.Errorf("a11y: %v", issues)
			}
		})
	}
	// The uninstall confirmation the link points at exists for the owner only.
	g, _ := newPluginRig(t)
	if rec := g.do("GET", "/admin/plugins/hello/uninstall", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor on the uninstall page: %d", rec.Code)
	}
}

// Without a webhooks plugin the list has no notice at all.
func TestPluginsPageHasNoLegacyNoticeWithoutWebhooks(t *testing.T) {
	t.Parallel()

	g, _ := newPluginRig(t)
	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "plugin-webhooks-builtin") || strings.Contains(body, "hm-callout--warning") {
		t.Error("notice shown without a webhooks plugin")
	}
}

func TestPluginPermissionsPage(t *testing.T) {
	t.Parallel()

	g, _ := newPluginRig(t)
	rec := g.do("GET", "/admin/plugins/hello/permissions", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"on_entity_published", "api.example.com", "1 of 2 allowed", "GET /p/hello/ping (public)", "sync every 1h", `value="kv" checked`, `action="/admin/plugins/hello/grant"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `value="mail.send" checked`) {
		t.Error("an ungranted permission must be unticked")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if rec := g.do("GET", "/admin/plugins/nope/permissions", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown plugin: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/plugins/hello/permissions", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

// A permission a community plugin may never hold shows unticked and disabled
// with a note, stays out of the "N of M allowed" count, and saving the form
// (which then omits it) still revokes a grant that was stored earlier.
func TestPluginPermissionsRefused(t *testing.T) {
	t.Parallel()

	g, fp := newPluginRig(t)
	fp.list[0].Permissions = []string{"kv", "site.scripts"}
	fp.list[0].Granted = []string{"kv", "site.scripts"}
	fp.list[0].Refused = []string{"site.scripts"}
	body := g.do("GET", "/admin/plugins/hello/permissions", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, `value="site.scripts" checked`) || !strings.Contains(body, `value="kv" checked`) {
		t.Errorf("refused row must be unticked and the other kept:\n%s", body)
	}
	if !strings.Contains(body, `value="site.scripts" disabled`) || !strings.Contains(body, "Not allowed for community plugins") {
		t.Error("refused row must be disabled and carry the note")
	}
	if !strings.Contains(body, "1 of 1 allowed") {
		t.Error("refused rows must not count towards the total")
	}
	// Every permission refused: no "0 of 0" gauge text.
	fp.list[0].Permissions = []string{"site.scripts"}
	fp.list[0].Refused = []string{"site.scripts"}
	all := g.do("GET", "/admin/plugins/hello/permissions", "owner-token", nil, nil).Body.String()
	if strings.Contains(all, "0 of 0") || strings.Contains(all, "until you tick it") || !strings.Contains(all, "Community plugins cannot use these permissions yet.") {
		t.Errorf("all refused: wrong foot note:\n%s", all)
	}
	fp.list[0].Permissions = []string{"kv", "site.scripts"}
	fp.list[0].Refused = []string{"site.scripts"}
	if rec := g.do("POST", "/admin/plugins/hello/grant", "owner-token", url.Values{"permission": {"kv"}}, nil); rec.Code != 303 || slices.Contains(fp.list[0].Granted, "site.scripts") {
		t.Errorf("saving must revoke the stored grant: %d %v", rec.Code, fp.list[0].Granted)
	}
}

func TestPluginGrant(t *testing.T) {
	t.Parallel()

	g, fp := newPluginRig(t)
	rec := g.do("POST", "/admin/plugins/hello/grant", "owner-token", url.Values{"permission": {"kv", "mail.send"}}, nil)
	if rec.Code != 303 || len(fp.list[0].Granted) != 2 {
		t.Fatalf("grant: %d %v", rec.Code, fp.list[0].Granted)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/plugins/hello/permissions" {
		t.Errorf("grant redirects to %q", loc)
	}
	if rec := g.do("POST", "/admin/plugins/hello/grant", "owner-token", url.Values{"permission": {"entities.write"}}, nil); rec.Code != 400 || len(fp.list[0].Granted) != 2 {
		t.Errorf("unrequested permission: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/plugins/hello/grant", "owner-token", url.Values{"permission": {"site.scripts"}}, nil)
	if body := rec.Body.String(); rec.Code != 400 || !strings.Contains(body, "community registry") || strings.Contains(body, "does not ask") || strings.Contains(body, "Untick") || !strings.Contains(body, "/admin/plugins/hello/permissions") {
		t.Errorf("community grant: %d %s", rec.Code, body)
	}
	// an empty form revokes everything
	if rec := g.do("POST", "/admin/plugins/hello/grant", "owner-token", url.Values{}, nil); rec.Code != 303 || len(fp.list[0].Granted) != 0 {
		t.Errorf("revoke: %d %v", rec.Code, fp.list[0].Granted)
	}
	if rec := g.do("POST", "/admin/plugins/nope/grant", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("unknown plugin: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/plugins/hello/grant", "editor-token", url.Values{"permission": {"kv"}}, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestPluginSettingsForm(t *testing.T) {
	t.Parallel()

	g, fp := newPluginRig(t)
	rec := g.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `value="abc"`) || !strings.Contains(body, "Site ID") {
		t.Fatalf("form: %d %s", rec.Code, body)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	rec = g.do("POST", "/admin/plugins/hello/settings", "owner-token", url.Values{"site_id": {"xyz"}, "api_key": {"k"}, "enabled": {"true"}}, nil)
	if rec.Code != 303 || len(fp.saved) != 1 || fp.saved[0]["site_id"] != "xyz" {
		t.Fatalf("save: %d %v", rec.Code, fp.saved)
	}
	// required field missing: reported by SaveSettings, nothing saved
	rec = g.do("POST", "/admin/plugins/hello/settings", "owner-token", url.Values{"site_id": {""}}, nil)
	if rec.Code != 422 || len(fp.saved) != 1 {
		t.Fatalf("required: %d", rec.Code)
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("error a11y: %v", issues)
	}
	// a rejection by the plugin runtime's own schema check
	rec = g.do("POST", "/admin/plugins/hello/settings", "owner-token", url.Values{"site_id": {"xyz"}, "api_key": {"bad"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "This key is not valid") || len(fp.saved) != 1 {
		t.Fatalf("runtime validation: %d", rec.Code)
	}
	// A plugin without a schema or UI still has a page, with the error shown.
	rec = g.do("GET", "/admin/plugins/broken/settings", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "wasm: bad module") || !strings.Contains(rec.Body.String(), "This plugin failed to start") {
		t.Errorf("a plugin without settings: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/plugins/broken/settings", "owner-token", url.Values{"site_id": {"x"}}, nil); rec.Code != 404 {
		t.Errorf("saving a plugin without settings: %d", rec.Code)
	}
}

const secretSchema = `{"type":"object","properties":{
 "site_id":{"type":"string","title":"Site ID"},
 "api_key":{"type":"string","title":"API key","writeOnly":true}}}`

func TestPluginSettingsNeverEchoWriteOnlyValues(t *testing.T) {
	t.Parallel()

	g, fp := newPluginRig(t)
	fp.list = append(fp.list, PluginInfo{Name: "vault", Version: "1.0.0", Loaded: true, SettingsSchema: []byte(secretSchema)})
	// Even a Settings() that handed back the stored secret must not reach the page.
	fp.settings["vault"] = map[string]any{"site_id": "abc", "api_key": "hunter2-secret"}
	fp.secrets = map[string]map[string]bool{"vault": {"api_key": true}}

	rec := g.do("GET", "/admin/plugins/vault/settings", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "hunter2-secret") {
		t.Fatalf("the secret reached the page (%d)", rec.Code)
	}
	for _, want := range []string{`type="password"`, `autocomplete="new-password"`, "Stored. Leave blank to keep it.", `name="api_key__clear"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
	// A typed secret that fails validation is not echoed back either.
	rec = g.do("POST", "/admin/plugins/vault/settings", "owner-token", url.Values{"site_id": {""}, "api_key": {"typed-secret"}}, nil)
	if rec.Code != 422 || strings.Contains(rec.Body.String(), "typed-secret") {
		t.Fatalf("422 re-render echoed the typed secret (%d)", rec.Code)
	}
	// Blank keeps (the field arrives empty), the clear box sends null.
	fp.saved = nil
	g.do("POST", "/admin/plugins/vault/settings", "owner-token", url.Values{"site_id": {"x"}, "api_key": {""}}, nil)
	if v, ok := fp.saved[0]["api_key"]; ok {
		t.Errorf("an empty field sends nothing, so the stored value is kept: %v", v)
	}
	g.do("POST", "/admin/plugins/vault/settings", "owner-token", url.Values{"site_id": {"x"}, "api_key": {""}, "api_key__clear": {"1"}}, nil)
	if v, ok := fp.saved[1]["api_key"]; !ok || v != nil {
		t.Errorf("the clear box must send null: %v", fp.saved[1])
	}
}

func TestPluginsPageTabsFilterAndEmpty(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	get := func(q string) string {
		return g.do("GET", "/admin/plugins"+q, "owner-token", nil, nil).Body.String()
	}
	all := get("")
	for _, want := range []string{"Needs attention", `href="/admin/plugins?view=updates"`, `href="/admin/plugins?view=disabled"`, "ready to install.", `id="updates-strip"`, "Check for updates", "Package security", `href="/admin/plugins/security"`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, gone := range []string{"Available plugins", "/plugins#unsigned", "unsigned-banner"} {
		if strings.Contains(all, gone) {
			t.Errorf("unexpected %q", gone)
		}
	}
	dis := get("?view=disabled")
	if !strings.Contains(dis, "<strong>quiet</strong>") || strings.Contains(dis, "<strong>hello</strong>") {
		t.Error("?view=disabled must list only disabled plugins")
	}
	if upd := get("?view=updates"); !strings.Contains(upd, "<strong>hello</strong>") || strings.Contains(upd, "<strong>quiet</strong>") {
		t.Error("?view=updates must list only plugins with an update")
	}
	if q := get("?q=QUIE"); !strings.Contains(q, "<strong>quiet</strong>") || strings.Contains(q, "<strong>hello</strong>") {
		t.Error("?q= must filter case-insensitively")
	}
	if none := get("?q=zzzz"); !strings.Contains(none, "No plugins match") {
		t.Error("a search with no hit needs its empty state")
	}
	pk.unsigned = true
	if on := get(""); !strings.Contains(on, "unsigned-banner") || !strings.Contains(on, "Unsigned packages are allowed until you turn it off.") {
		t.Error("banner missing while the opt-in is on")
	}
}
