package admin

import (
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// subnavLink returns the subnav anchor for href, or "".
func subnavLink(body, href string) string {
	m := regexp.MustCompile(`<a class="hm-subnav__link[^"]*" href="` + regexp.QuoteMeta(href) + `"[^>]*>.*?</a>`).FindString(body)
	return m
}

func TestPluginsSubnavListsEveryPlugin(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	body := g.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `<nav class="hm-subnav" aria-label="Plugins">`) || !strings.Contains(body, ">All plugins<") {
		t.Fatalf("no Plugins subnav")
	}
	for _, name := range []string{"hello", "quiet", "devonly", "ownerui", "board"} {
		if subnavLink(body, "/admin/plugins/"+name+"/settings") == "" {
			t.Errorf("subnav lacks %s", name)
		}
	}
	if n := strings.Count(body, `class="hm-subnav__link`); n != 6 {
		t.Errorf("%d subnav links, want 6", n)
	}
	if n := strings.Count(body, `aria-current="page"`); n < 1 || !strings.Contains(subnavLink(body, "/admin/plugins/hello/settings"), `aria-current="page"`) {
		t.Errorf("the hello entry must be current")
	}
	if strings.Contains(subnavLink(body, "/admin/plugins"), "aria-current") {
		t.Error("All plugins is not current on a plugin page")
	}
	list := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	if !strings.Contains(subnavLink(list, "/admin/plugins"), `aria-current="page"`) {
		t.Error("All plugins is current on the list")
	}
	// The permissions tab sits in the same frame.
	if !strings.Contains(g.do("GET", "/admin/plugins/hello/permissions", "owner-token", nil, nil).Body.String(), `aria-label="Plugins"`) {
		t.Error("the permissions page has no subnav")
	}
}

// TestPluginsSubnavFramesEveryPluginsPage pins the frame on the pages that are
// not plain GET tabs: the 422 re-render, the confirm and prompt pages and the
// error pages.
func TestPluginsSubnavFramesEveryPluginsPage(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list, PluginInfo{Name: "formy", Version: "1.0.0", Loaded: true, SettingsSchema: []byte(hookSchema)})
	pages := map[string]*httptest.ResponseRecorder{
		"422 settings re-render": g.do("POST", "/admin/plugins/formy/settings", "owner-token", url.Values{"site_id": {""}}, nil),
		"uninstall confirm":      g.do("GET", "/admin/plugins/hello/uninstall", "owner-token", nil, nil),
		"install prompt":         g.do("POST", "/admin/plugins/fetch", "owner-token", url.Values{"name": {"seo-tools"}}, nil),
		"error page":             g.do("GET", "/admin/plugins/ghost/permissions", "owner-token", nil, nil),
	}
	wantCode := map[string]int{"422 settings re-render": 422, "uninstall confirm": 200, "install prompt": 200, "error page": 404}
	for name, rec := range pages {
		body := rec.Body.String()
		if rec.Code != wantCode[name] {
			t.Errorf("%s: status %d, want %d", name, rec.Code, wantCode[name])
		}
		if !strings.Contains(body, `<nav class="hm-subnav" aria-label="Plugins">`) || subnavLink(body, "/admin/plugins/hello/settings") == "" {
			t.Errorf("%s: no Plugins subnav", name)
		}
	}
	if body := pages["422 settings re-render"].Body.String(); !strings.Contains(body, "hm-tabs") || !strings.Contains(body, "hm-pluginhead") {
		t.Error("the 422 re-render lost the plugin header or tabs")
	}
	if !strings.Contains(subnavLink(pages["uninstall confirm"].Body.String(), "/admin/plugins/hello/settings"), `aria-current="page"`) {
		t.Error("the uninstall page must mark hello current")
	}
}

func TestPluginRoutesWithoutAPluginService(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, p := range []string{"/admin/plugins/x/settings", "/admin/plugins/x/permissions"} {
		for _, token := range []string{"owner-token", "editor-token"} {
			rec := g.do("GET", p, token, nil, nil)
			if rec.Code == 503 {
				continue
			}
			if token == "editor-token" && p == "/admin/plugins/x/permissions" && rec.Code == 403 {
				continue // the owner-only group answers before the handler
			}
			t.Errorf("%s as %s: %d, want 503", p, token, rec.Code)
		}
	}
	for token, want := range map[string]string{"owner-token": "/admin/plugins", "editor-token": "/admin"} {
		rec := g.do("GET", "/admin/plugins/go?plugin=x", token, nil, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != want {
			t.Errorf("go as %s: %d %q, want 303 %s", token, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

func TestPluginsSubnavMutesOffAndFailedPlugins(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list, PluginInfo{Name: "crashed", Error: "boom"})
	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	quiet := subnavLink(body, "/admin/plugins/quiet/settings")
	if !strings.Contains(quiet, "hm-subnav__link--muted") || !strings.Contains(quiet, ">Off<") {
		t.Errorf("a disabled plugin is muted with Off: %s", quiet)
	}
	crashed := subnavLink(body, "/admin/plugins/crashed/settings")
	if !strings.Contains(crashed, "hm-subnav__link--muted") || !strings.Contains(crashed, ">Failed<") {
		t.Errorf("a failed plugin is muted with Failed: %s", crashed)
	}
	if hello := subnavLink(body, "/admin/plugins/hello/settings"); strings.Contains(hello, "muted") || strings.Contains(hello, "hm-subnav__marker") {
		t.Errorf("an active plugin is neither muted nor marked: %s", hello)
	}
}

func TestPluginStatusBadges(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list,
		PluginInfo{Name: "crashed", Error: "boom"},
		PluginInfo{Name: "stuck", Package: &PackageMeta{Enabled: true, Blocked: "needs a newer host"}},
		PluginInfo{Name: "leaving", Package: &PackageMeta{Enabled: true, Uninstalling: true}},
	)
	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	for _, want := range []string{">Active<", ">Disabled<", ">Failed<", ">Blocked<", ">Uninstalling<"} {
		if !strings.Contains(body, want) {
			t.Errorf("the list lacks the %s badge", want)
		}
	}
}

func TestPluginWithoutSchemaOrUIShowsAnEmptyState(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := g.do("GET", "/admin/plugins/devonly/settings", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Nothing to configure") {
		t.Errorf("devonly: %d", rec.Code)
	}
}

func TestPluginEmptyStateFollowsTheDeclaredUI(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list,
		PluginInfo{Name: "stuck", Version: "1.0.0", Package: &PackageMeta{Enabled: true, Blocked: "needs a newer host"}},
		PluginInfo{Name: "leaving", Version: "1.0.0", Package: &PackageMeta{Enabled: true, Uninstalling: true}},
		PluginInfo{Name: "edonly", Version: "1.0.0", Loaded: true, UI: &PluginUIInfo{Title: "Editors", Roles: []string{"editor"}}},
	)
	const blocked, leaving = "Policy blocks this plugin", "This plugin is being uninstalled."
	const off, failed, hidden, nothing = "This plugin is turned off. Turn it on to use its settings and admin page.", "This plugin failed to start", "Admin page not available to you", "Nothing to configure"
	for _, c := range []struct {
		name string
		want string
		not  []string
	}{
		{"quiet", off, []string{failed, hidden, nothing}}, // turned off, no UI
		{"stuck", blocked + ", so it is not running. Reason: needs a newer host", []string{off, failed, hidden, nothing, leaving}},
		{"leaving", leaving, []string{off, failed, hidden, nothing, blocked}},
		{"edonly", hidden, []string{off, failed, nothing}},  // UI exists, the owner may not see it
		{"devonly", nothing, []string{off, failed, hidden}}, // loaded, nothing declared
	} {
		body := g.do("GET", "/admin/plugins/"+c.name+"/settings", "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, c.want) {
			t.Errorf("%s: lacks %q", c.name, c.want)
		}
		for _, n := range c.not {
			if strings.Contains(body, n) {
				t.Errorf("%s: must not say %q", c.name, n)
			}
		}
	}
	fp.list = append(fp.list, PluginInfo{Name: "crashed", Error: "boom", UI: &PluginUIInfo{Title: "C"}})
	if body := g.do("GET", "/admin/plugins/crashed/settings", "owner-token", nil, nil).Body.String(); !strings.Contains(body, failed) || strings.Contains(body, off) || strings.Contains(body, "turn it on again") || !strings.Contains(body, "restart the server") {
		t.Error("a plugin with an error says it failed to start and needs a fix and a restart")
	}
}

func TestPluginsSubnavForEditors(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := g.do("GET", "/admin/plugins/board/settings", "editor-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("editor on an editor-open plugin: %d", rec.Code)
	}
	if strings.Contains(body, "All plugins") || strings.Contains(body, "/admin/plugins/ownerui/") || strings.Contains(body, "/admin/plugins/quiet/") {
		t.Error("the editor's subnav lists only plugins open to editors, and no All plugins")
	}
	if subnavLink(body, "/admin/plugins/board/settings") == "" {
		t.Error("the editor's subnav lists board")
	}
	for _, owned := range []string{"hm-tabs", "Turn off", ">Save<", `href="/admin/plugins/board/permissions"`} {
		if strings.Contains(body, owned) {
			t.Errorf("an editor's page must not carry %q", owned)
		}
	}
	for _, p := range []string{"/admin/plugins/ownerui/settings", "/admin/plugins/ghost/settings"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 404 {
			t.Errorf("%s as editor: %d", p, rec.Code)
		}
	}
	for _, p := range []string{"/admin/plugins", "/admin/plugins/board/permissions"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("%s as editor: %d", p, rec.Code)
		}
	}
	// The sidebar offers an editor the first plugin they may open.
	if side := g.do("GET", "/admin/profile", "editor-token", nil, nil).Body.String(); !strings.Contains(side, `href="/admin/plugins/board/settings"`) {
		t.Error("the editor's Plugins item must open the first plugin they may use")
	}
}

func TestPluginsGoSelect(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	go303 := func(token, query string) string {
		t.Helper()
		rec := g.do("GET", "/admin/plugins/go"+query, token, nil, nil)
		if rec.Code != 303 {
			t.Fatalf("go%s as %s: %d", query, token, rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.HasPrefix(loc, "/admin") || strings.HasPrefix(loc, "//") || strings.Contains(loc, "://") {
			t.Fatalf("go%s redirects off the admin: %q", query, loc)
		}
		return loc
	}
	for query, want := range map[string]string{
		"?plugin=hello":             "/admin/plugins/hello/settings",
		"?plugin=":                  "/admin/plugins",
		"":                          "/admin/plugins",
		"?plugin=ghost":             "/admin/plugins",
		"?plugin=//evil.example":    "/admin/plugins",
		"?plugin=hello%2F..%2Fevil": "/admin/plugins",
	} {
		if got := go303("owner-token", query); got != want {
			t.Errorf("owner go%s: %q, want %q", query, got, want)
		}
	}
	for query, want := range map[string]string{
		"?plugin=board":   "/admin/plugins/board/settings",
		"?plugin=ownerui": "/admin",
		"?plugin=":        "/admin",
		"?plugin=//evil":  "/admin",
	} {
		if got := go303("editor-token", query); got != want {
			t.Errorf("editor go%s: %q, want %q", query, got, want)
		}
	}
	// The select's "All plugins" option is the empty value, so a plugin
	// literally named "all" is a normal, selectable entry.
	fp.list = append(fp.list, PluginInfo{Name: "all", Version: "1.0.0", Loaded: true})
	if got := go303("owner-token", "?plugin=all"); got != "/admin/plugins/all/settings" {
		t.Errorf("a plugin named all: %q", got)
	}
	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`<option value="" selected>All plugins</option>`, `<option value="all">all</option>`, `<option value="hello">hello</option>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the select lacks %q", want)
		}
	}
	if strings.Contains(body, `<option value="all">All plugins`) {
		t.Error("All plugins must not use a value a plugin name can take")
	}
}

func TestPluginRedirects(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := g.do("GET", "/admin/plugins/hello", "owner-token", nil, nil)
	if rec.Code != 302 || rec.Header().Get("Location") != "/admin/plugins/hello/settings" {
		t.Errorf("/admin/plugins/hello: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, token := range []string{"owner-token", "editor-token"} {
		rec := g.do("GET", "/admin/plugins/board/ui", token, nil, nil)
		if rec.Code != 301 || rec.Header().Get("Location") != "/admin/plugins/board/settings" {
			t.Errorf("/ui as %s: %d %q", token, rec.Code, rec.Header().Get("Location"))
		}
	}
	// The bridge keeps working (also covered by the bridge tests).
	if rec := bridge(g, "board", "owner-token", map[string]any{"op": "route", "method": "GET", "path": "/a"}); rec.Code != 200 {
		t.Errorf("bridge: %d", rec.Code)
	}
}

func TestPluginPagesPassA11y(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for _, p := range []string{"/admin/plugins", "/admin/plugins/hello/settings", "/admin/plugins/hello/permissions", "/admin/plugins/devonly/settings"} {
		rec := g.do("GET", p, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
		a11yOK(t, rec.Body.String())
	}
}

func TestPluginTabsHaveDistinctTitles(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	title := func(path string) string {
		m := regexp.MustCompile(`<title>(.*?)</title>`).FindStringSubmatch(g.do("GET", path, "owner-token", nil, nil).Body.String())
		if m == nil {
			t.Fatalf("%s has no title", path)
		}
		return m[1]
	}
	settings, perms := title("/admin/plugins/hello/settings"), title("/admin/plugins/hello/permissions")
	if !strings.Contains(settings, "hello settings") || !strings.Contains(perms, "hello permissions") {
		t.Errorf("titles %q and %q", settings, perms)
	}
}

func TestUnknownPluginSettingsLinksBackByRole(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for token, want := range map[string]string{"owner-token": `href="/admin/plugins"`, "editor-token": `href="/admin"`} {
		rec := g.do("GET", "/admin/plugins/ghost/settings", token, nil, nil)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d, no %s back link", token, rec.Code, want)
		}
	}
	if body := g.do("GET", "/admin/plugins/ghost/settings", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Back to plugins") {
		t.Error("the owner's back link says Back to plugins")
	}
}

// A plugin named "go" is legal (sdk.NamePattern). The literal /plugins/go route
// is the select endpoint and wins over /plugins/{name}, so its bare-name
// shortcut is lost; everything under /go/... still reaches the plugin.
func TestPluginNamedGo(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list, PluginInfo{Name: "go", Version: "1.0.0", Loaded: true})
	rec := g.do("GET", "/admin/plugins/go", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/plugins" {
		t.Errorf("/admin/plugins/go is the select endpoint: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = g.do("GET", "/admin/plugins/go?plugin=go", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/plugins/go/settings" {
		t.Errorf("the select reaches go: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = g.do("GET", "/admin/plugins/go/settings", "owner-token", nil, nil)
	if rec.Code != 200 || subnavLink(rec.Body.String(), "/admin/plugins/go/settings") == "" {
		t.Errorf("/admin/plugins/go/settings reaches the plugin: %d", rec.Code)
	}
}

// A widget-only package has no runtime status, and that is not a failure.
func TestWidgetOnlyPluginIsNotFailed(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list,
		PluginInfo{Name: "packonly", Permissions: []string{"builder.island"}, Package: &PackageMeta{Enabled: true, WidgetOnly: true}},
		PluginInfo{Name: "packoff", Package: &PackageMeta{Enabled: false, WidgetOnly: true}},
		PluginInfo{Name: "packbad", Package: &PackageMeta{Enabled: true, Blocked: "no key", WidgetOnly: true}},
	)
	if st := pluginStatus(fp.list[len(fp.list)-3]); !st.Active || st.Badge.Label != "Widgets" || st.Marker != "" {
		t.Errorf("widget-only state = %+v", st)
	}
	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	link := subnavLink(body, "/admin/plugins/packonly/settings")
	if link == "" || strings.Contains(link, "muted") || strings.Contains(link, "Failed") {
		t.Errorf("widget-only pack in the subnav: %q", link)
	}
	if !strings.Contains(body, ">Widgets<") {
		t.Error("the list has no Widgets badge")
	}
	page := g.do("GET", "/admin/plugins/packonly/settings", "owner-token", nil, nil).Body.String()
	if strings.Contains(page, "failed to start") || strings.Contains(page, "not active") || !strings.Contains(page, "Widgets only") {
		t.Errorf("widget-only plugin page: %.400s", page)
	}
	// Disabled and blocked still win over the widget-only state.
	if st := pluginStatus(fp.list[len(fp.list)-2]); st.Badge.Label != "Disabled" {
		t.Errorf("disabled widget-only = %+v", st)
	}
	if st := pluginStatus(fp.list[len(fp.list)-1]); st.Badge.Label != "Blocked" {
		t.Errorf("blocked widget-only = %+v", st)
	}
}

// Browser JavaScript that is stopped for want of site.scripts is flagged on the
// plugin list and on the plugin's own page, with a way to grant it.
func TestPluginWithoutSiteScriptsGrantShowsBanner(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list,
		PluginInfo{Name: "gtmlike", Permissions: []string{"site.scripts"}, Package: &PackageMeta{Enabled: true, ScriptsBlocked: true, AsksScripts: true}},
		PluginInfo{Name: "oldjs", Package: &PackageMeta{Enabled: true, ScriptsBlocked: true}},
		PluginInfo{Name: "quiet", Package: &PackageMeta{Enabled: true}},
		PluginInfo{Name: "sectioned", Permissions: []string{"site.scripts"}, Package: &PackageMeta{Enabled: true, AsksScripts: true, MarkupStopped: []string{"section promo: template section-promo: <script> is not allowed without site.scripts"}}},
	)
	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`href="/admin/plugins/gtmlike/settings#needs-attention"`, `href="/admin/plugins/oldjs/settings#needs-attention"`, `href="/admin/plugins/sectioned/settings#needs-attention"`, "gtmlike needs attention"} {
		if !strings.Contains(body, want) {
			t.Errorf("list missing %q", want)
		}
	}
	if n := strings.Count(body, "hm-badge--warning"); n != 3 {
		t.Errorf("want 3 Needs attention pills (quiet has none), got %d", n)
	}
	if strings.Contains(body, "its JavaScript is not running") || strings.Contains(body, "That is the safe default") {
		t.Error("the issue is broadcast on the plugin list")
	}
	if sp := g.do("GET", "/admin/plugins/sectioned/permissions", "owner-token", nil, nil).Body.String(); !strings.Contains(sp, `id="needs-attention"`) || !strings.Contains(sp, "Some of its page sections are not shown") || !strings.Contains(sp, "section promo") {
		t.Errorf("plugin page does not say why its sections are stopped: %.400s", sp)
	}
	if old := g.do("GET", "/admin/plugins/oldjs/permissions", "owner-token", nil, nil).Body.String(); !strings.Contains(old, "needs an update from its author") || strings.Contains(old, "Review its permissions") {
		t.Errorf("plugin without a site.scripts ask: wrong callout: %.300s", old)
	}
	page := g.do("GET", "/admin/plugins/gtmlike/permissions", "owner-token", nil, nil).Body.String()
	if !strings.Contains(page, `id="needs-attention"`) || !strings.Contains(page, "hm-callout--warning") || !strings.Contains(page, "Its JavaScript is not running on your public pages") || !strings.Contains(page, `href="/admin/plugins/gtmlike/permissions">Review its permissions`) {
		t.Errorf("plugin page has no banner: %.300s", page)
	}
}

func TestPluginPermissionsPageUsesCardsGaugeAndCallout(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list = append(fp.list, PluginInfo{Name: "wide", Version: "1.0.0", Loaded: true, Hooks: []string{"on_x"}, Permissions: []string{"kv", "http.fetch.any"}, Granted: []string{"kv"},
		AllowedHosts: []string{"*", "api.example.com"}, Package: &PackageMeta{Enabled: true, Blocked: "policy says no"}})
	body := g.do("GET", "/admin/plugins/wide/permissions", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`id="hooks-card"`, `id="hosts-card"`, `id="package-card"`, "1 of 2", "hm-meter", "hm-callout--warning", "Any public web address (ports 80 and 443)", "hm-callout--danger", "policy says no", ">api.example.com<"} {
		if !strings.Contains(body, want) {
			t.Errorf("permissions page lacks %q", want)
		}
	}
	if strings.Contains(body, "hm-plugin__error") {
		t.Error("the old error paragraph is back")
	}
	a11yOK(t, body)
	// A plugin without permissions has no gauge and says so in a card.
	quiet := g.do("GET", "/admin/plugins/hello/permissions", "owner-token", nil, nil).Body.String()
	fp.list[0].Permissions, fp.list[0].Granted = nil, nil
	none := g.do("GET", "/admin/plugins/hello/permissions", "owner-token", nil, nil).Body.String()
	if !strings.Contains(quiet, "hm-meter") || strings.Contains(none, "hm-meter") || !strings.Contains(none, "It asks for no permissions.") {
		t.Error("gauge shown without permissions, or the empty text is missing")
	}
}
