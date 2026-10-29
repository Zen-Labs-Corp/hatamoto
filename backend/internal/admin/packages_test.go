package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/siteconfig"
)

func init() {
	areaPages["packages"] = []string{"settings/pluginprompt", "settings/packageconfirm", "settings/pluginuninstall", "settings/theme", "settings/themeactivate", "settings/themesettings", "settings/themedesign", "settings/designtokens", "settings/designpresets"}
}

type installCall struct {
	source  string
	grant   []string
	data    string
	confirm bool
}

type fakePackages struct {
	registry    RegistryView
	unsigned    bool
	unsignedEnd *time.Time // end of a timed opt-in, with unsigned
	signers     []SignerInfo
	refreshed   int // RefreshRegistry calls
	installed   []installCall
	enabled     map[string]bool
	removed     []string
	dropped     bool
	themes      []ThemeInfo
	themeSchema []byte // overrides themeSchemaJSON when set
	activateErr error  // makes Activate fail
	missing     []MissingSection
	activated   string
	settings    map[string]any
	saved       []map[string]any
	// plugin ui
	uiFiles   map[string]string
	routeCall []string
	hostCall  []string
	problem   string
	// fetchAs makes Fetch return this data for a plugin name (to reach a canned Inspect case).
	fetchAs map[string]string
	// installFails makes Install refuse the package whose data is this.
	installFails map[string]string
	// fetchFails makes Fetch fail for a plugin name with this message.
	fetchFails map[string]string
}

const themeSchemaJSON = `{"type":"object","properties":{"accent":{"type":"string","title":"Accent word","default":"teal"}}}`

func (f *fakePackages) Registry(context.Context) RegistryView { return f.registry }
func (f *fakePackages) RefreshRegistry(context.Context) RegistryView {
	f.refreshed++
	return f.registry
}
func (f *fakePackages) Fetch(_ context.Context, kind, name string) ([]byte, error) {
	if name == "nope" {
		return nil, &Refusal{Msg: "the registry has no plugin \"nope\""}
	}
	if msg, ok := f.fetchFails[name]; ok {
		return nil, &Refusal{Msg: msg}
	}
	if d, ok := f.fetchAs[name]; ok {
		return []byte(d), nil
	}
	return []byte(kind + ":" + name), nil
}
func (f *fakePackages) Inspect(_ context.Context, data []byte) (PackageSummary, error) {
	s := string(data)
	switch {
	case strings.HasPrefix(s, "garbage"):
		return PackageSummary{}, &Refusal{Msg: "package: not a valid zip archive"}
	case strings.HasPrefix(s, "theme-old:"): // an older version over an installed theme, by another signer, losing a section
		name := strings.TrimPrefix(s, "theme-old:")
		return PackageSummary{Kind: "theme", Name: name, Version: "1.0.0", Signed: true, Signer: "newkey", Updating: true, InstalledVersion: "2.0.0",
			Confirm: &ConfirmInfo{Name: name, Downgrade: true, SignerChange: true, OldVersion: "2.0.0", NewVersion: "1.0.0", OldSigner: "oldkey", NewSigner: "newkey",
				Missing: []MissingSection{{Type: "promo", Uses: 3}}}}, nil
	case strings.HasPrefix(s, "plugin-old:"):
		name := strings.TrimPrefix(s, "plugin-old:")
		return PackageSummary{Kind: "plugin", Name: name, Version: "1.0.0", Permissions: []string{"kv", "mail.send"}, Signed: true, Signer: "newkey",
			Updating: true, InstalledVersion: "2.0.0", Granted: []string{"kv"},
			Confirm: &ConfirmInfo{Name: name, Downgrade: true, SignerChange: true, OldVersion: "2.0.0", NewVersion: "1.0.0", OldSigner: "oldkey", NewSigner: "newkey"}}, nil
	case strings.HasPrefix(s, "plugin-any:"): // an update by the same signer that newly asks for any address
		name := strings.TrimPrefix(s, "plugin-any:")
		return PackageSummary{Kind: "plugin", Name: name, Version: "2.1.0", Permissions: []string{"http.fetch", "http.fetch.any"}, AllowedHosts: []string{"*"},
			Signed: true, Signer: "abc123", Updating: true, InstalledVersion: "2.0.0", Granted: []string{"http.fetch"},
			Confirm: &ConfirmInfo{Name: name, WidenedAccess: true, NewFetchAny: true, NewHosts: []string{"*"}, OldVersion: "2.0.0", NewVersion: "2.1.0", OldSigner: "abc123", NewSigner: "abc123"}}, nil
	case strings.HasPrefix(s, "plugin-scripts:"): // an update that newly asks for site.scripts
		name := strings.TrimPrefix(s, "plugin-scripts:")
		return PackageSummary{Kind: "plugin", Name: name, Version: "2.1.0", Permissions: []string{"kv", "site.scripts"},
			Signed: true, Signer: "abc123", Updating: true, InstalledVersion: "2.0.0", Granted: []string{"kv"},
			Confirm: &ConfirmInfo{Name: name, WidenedAccess: true, NewScripts: true, OldVersion: "2.0.0", NewVersion: "2.1.0", OldSigner: "abc123", NewSigner: "abc123"}}, nil
	case strings.HasPrefix(s, "plugin-fresh-scripts:"): // a first install that asks for site.scripts
		return PackageSummary{Kind: "plugin", Name: strings.TrimPrefix(s, "plugin-fresh-scripts:"), Version: "1.0.0", Permissions: []string{"kv", "site.scripts"}, Signed: true, Signer: "abc123"}, nil
	case strings.HasPrefix(s, "theme:"):
		return PackageSummary{Kind: "theme", Name: strings.TrimPrefix(s, "theme:"), Version: "1.0.0", Signed: true, Signer: "abc123"}, nil
	}
	name := strings.TrimPrefix(strings.TrimPrefix(s, "plugin:"), "upload:")
	sum := PackageSummary{Kind: "plugin", Name: name, Version: "2.0.0", Description: "Does <things>", Permissions: []string{"kv", "mail.send"}, AllowedHosts: []string{"api.example.com"}, Problem: f.problem}
	if name == "hello" {
		sum.Updating, sum.InstalledVersion, sum.Granted = true, "1.2.0", []string{"kv"}
	}
	if !strings.HasPrefix(s, "upload:") {
		sum.Signed, sum.Signer = true, "abc123"
	}
	return sum, nil
}
func (f *fakePackages) Install(_ context.Context, data []byte, source string, grant []string, confirm bool) error {
	if strings.Contains(string(data), "evil") {
		return &Refusal{Msg: "package signature does not match its contents"}
	}
	if msg, ok := f.installFails[string(data)]; ok {
		return &Refusal{Msg: msg}
	}
	f.installed = append(f.installed, installCall{source, grant, string(data), confirm})
	return nil
}
func (f *fakePackages) SetEnabled(_ context.Context, name string, on bool) error {
	if name == "nope" {
		return errors.New("not installed")
	}
	f.enabled[name] = on
	return nil
}
func (f *fakePackages) Uninstall(_ context.Context, kind, name string, drop bool) error {
	if kind == "theme" && name == "sunrise" {
		return ErrThemeActive
	}
	f.removed, f.dropped = append(f.removed, kind+"/"+name), drop
	return nil
}
func (f *fakePackages) AllowUnsigned(context.Context) bool { return f.unsigned }
func (f *fakePackages) UnsignedState(context.Context) UnsignedState {
	return UnsignedState{On: f.unsigned, Until: f.unsignedEnd}
}
func (f *fakePackages) SetAllowUnsigned(_ context.Context, on bool, until *time.Time, _ string) error {
	f.unsigned, f.unsignedEnd = on, until
	return nil
}
func (f *fakePackages) Signers(context.Context) []SignerInfo        { return f.signers }
func (f *fakePackages) Themes(context.Context) ([]ThemeInfo, error) { return f.themes, nil }
func (f *fakePackages) MissingSections(_ context.Context, theme string) ([]MissingSection, error) {
	if theme == "ghost" {
		return nil, errors.New("not installed")
	}
	return f.missing, nil
}
func (f *fakePackages) Activate(_ context.Context, theme string) error {
	if f.activateErr != nil {
		return f.activateErr
	}
	f.activated = theme
	return nil
}
func (f *fakePackages) ThemeSettingsSchema(_ context.Context, theme string) []byte {
	if theme == "sunrise" {
		if f.themeSchema != nil {
			return f.themeSchema
		}
		return []byte(themeSchemaJSON)
	}
	return nil
}
func (f *fakePackages) ThemeSettings(context.Context, string) (map[string]any, error) {
	return f.settings, nil
}
func (f *fakePackages) SaveThemeSettings(_ context.Context, _ string, v map[string]any) (schemaform.Errors, error) {
	if v["accent"] == "bad" {
		return schemaform.Errors{"accent": {"Not an accent we know"}}, nil
	}
	f.saved = append(f.saved, v)
	return nil, nil
}
func (f *fakePackages) PreviewURL(theme string) string { return "/?hm_theme_preview=tok-" + theme }
func (f *fakePackages) UIFile(w http.ResponseWriter, _ *http.Request, plugin, file, csp, cache string) bool {
	body, ok := f.uiFiles[plugin+"/"+file]
	if !ok {
		return false
	}
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Cache-Control", cache)
	_, _ = w.Write([]byte(body))
	return true
}
func (f *fakePackages) CallRoute(_ context.Context, plugin, method, path string, q url.Values, body []byte, u CurrentUser) RouteResult {
	f.routeCall = append(f.routeCall, plugin+" "+method+" "+path+"?"+q.Encode()+" body="+string(body)+" as="+u.Role)
	return RouteResult{Status: 200, ContentType: "application/json", Body: []byte(`{"echo":true}`)}
}
func (f *fakePackages) CallHost(_ context.Context, plugin, fn string, _ json.RawMessage) json.RawMessage {
	f.hostCall = append(f.hostCall, plugin+" "+fn)
	return json.RawMessage(`{"ok":true,"result":{}}`)
}

func newPackageRig(t *testing.T) (*rig, *fakePackages, *fakePlugins) {
	t.Helper()
	fp := &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{
		{Name: "hello", Version: "1.2.0", Loaded: true, Permissions: []string{"kv", "mail.send"}, Granted: []string{"kv"},
			Package: &PackageMeta{Enabled: true, Signed: true, Signer: "abc123", Source: "registry", Update: "2.0.0"},
			UI:      &PluginUIInfo{Title: "Hello board", Entry: "index.html", Functions: []string{"kv.get", "log"}}},
		{Name: "quiet", Version: "0.1.0", Package: &PackageMeta{Enabled: false, Source: "upload"}},
		{Name: "devonly", Version: "0.0.1", Loaded: true},
		{Name: "ownerui", Version: "1.0.0", Loaded: true, UI: &PluginUIInfo{Title: "Owner tool", Roles: []string{"owner"}}},
		{Name: "board", Version: "1.0.0", Loaded: true, UI: &PluginUIInfo{Title: "Board", Roles: []string{"owner", "editor"}, Functions: []string{"settings.get", "mail.send"}}},
	}}
	pk := &fakePackages{
		enabled: map[string]bool{},
		registry: RegistryView{Entries: []RegistryEntry{
			{Kind: "plugin", Name: "hello", Version: "2.0.0", Installed: true, InstalledVersion: "1.2.0", Update: true},
			{Kind: "plugin", Name: "seo-tools", Version: "1.0.0", Description: "SEO <extras>"},
			{Kind: "theme", Name: "sunrise", Version: "1.1.0", Installed: true, InstalledVersion: "1.0.0", Update: true},
			{Kind: "theme", Name: "dusk", Version: "0.3.0", Description: "A dark theme"},
		}},
		themes: []ThemeInfo{
			{Name: "default", Builtin: true},
			{Name: "sunrise", Version: "1.0.0", Description: "Warm", Active: true, Signed: true, Signer: "abc123", Source: "registry", HasSettings: true, Update: "1.1.0"},
			{Name: "dusk", Version: "0.3.0", Source: "upload"},
		},
		settings: map[string]any{"accent": "teal"},
		uiFiles:  map[string]string{"hello/ui/index.html": "<h1>hi</h1>", "hello/ui/app.js": "console.log(1)"},
	}
	g := newRig(t, func(d *Deps) { d.Growth.Plugins, d.Growth.Packages = fp, pk })
	return g, pk, fp
}

func a11yOK(t *testing.T, body string) {
	t.Helper()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func postMultipart(g *rig, target, token, filename, content string) *httptest.ResponseRecorder {
	return postUpload(g, target, token, csrfValue, true, filename, content)
}

// postUpload posts the upload form the way the page does: the _csrf field is
// the body's first part. firstCSRF false puts it after the file; an empty
// csrf sends none (no header, no field).
func postUpload(g *rig, target, token, csrf string, firstCSRF bool, filename, content string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	field := func() {
		if csrf != "" {
			_ = mw.WriteField("_csrf", csrf)
		}
	}
	if firstCSRF {
		field()
	}
	if filename != "" {
		fw, _ := mw.CreateFormFile("package", filename)
		_, _ = fw.Write([]byte(content))
	}
	if !firstCSRF {
		field()
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	// The token travels in the body (first part) or not at all, never in the header.
	g.addFetchMeta(req, token, map[string]string{"X-CSRF-Token": ""})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func TestPluginsPageShowsPackageControls(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := g.do("GET", "/admin/plugins", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		">Disabled<", `href="/admin/plugins/upload"`, `data-hm-open="upload-pkg"`, `href="/admin/plugins/browse"`, `id="upload-pkg"`,
		"Upload package", `enctype="multipart/form-data"`, "Unsigned packages are refused unless an owner allows them.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Per-plugin controls live on the plugin's own pages, not in the list.
	for _, gone := range []string{"Turn off", "Turn on", "Signed by", "Open Hello board", "/admin/plugins/hello/ui"} {
		if strings.Contains(body, gone) {
			t.Errorf("the list still carries %q", gone)
		}
	}
	if strings.Contains(body, "Install</span>") && strings.Contains(body, `value="hello">`+"\n") {
		t.Error("an installed plugin must not be offered for install")
	}
	a11yOK(t, body)
}

func TestPluginPageShowsPackageControls(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	body := g.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Update to 2.0.0", "Turn off", `name="name" value="hello"`, `href="/admin/plugins/hello/permissions"`, `data-src="/plugin-ui/hello/index.html?t=`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings tab missing %q", want)
		}
	}
	if strings.Contains(body, "Uninstall") {
		t.Error("Uninstall lives on the Permissions tab")
	}
	quiet := g.do("GET", "/admin/plugins/quiet/settings", "owner-token", nil, nil).Body.String()
	if !regexp.MustCompile(`<form method="post" action="/admin/plugins/quiet/enable">.*?class="hm-btn hm-btn--secondary"[^>]*><span>Turn on</span>`).MatchString(quiet) || strings.Contains(quiet, "hm-overflow") {
		t.Error("a disabled package offers a visible secondary Turn on, not an overflow item")
	}
	if strings.Contains(g.do("GET", "/admin/plugins/devonly/settings", "owner-token", nil, nil).Body.String(), "Turn o") {
		t.Error("a plugin without a package has no toggle")
	}
	perms := g.do("GET", "/admin/plugins/hello/permissions", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Signed by <code>abc123</code>", "Uninstall", `href="/admin/plugins/hello/uninstall"`} {
		if !strings.Contains(perms, want) {
			t.Errorf("permissions tab missing %q", want)
		}
	}
	// Hooks, hosts and routes are identifiers: monospace badges that wrap, never nowrap status badges.
	if strings.Contains(perms, `hm-badgelist`) && (!strings.Contains(perms, `<li><span class="hm-badge hm-badge--neutral hm-badge--wrap hm-badge--mono">`) || strings.Contains(perms, `<li><span class="hm-badge hm-badge--neutral">`)) {
		t.Errorf("capability lists must be wrapping mono badges:\n%s", perms)
	}
	a11yOK(t, body)
	a11yOK(t, perms)
}

func TestUnsignedOptInNeedsAcknowledgement(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	if rec := g.do("POST", "/admin/plugins/unsigned", "owner-token", url.Values{"allow": {"1"}}, nil); rec.Code != 422 || pk.unsigned {
		t.Fatalf("without the tick: %d unsigned=%v", rec.Code, pk.unsigned)
	}
	if rec := g.do("POST", "/admin/plugins/unsigned", "owner-token", url.Values{"allow": {"1"}, "understand": {"1"}}, nil); rec.Code != 303 || !pk.unsigned {
		t.Fatalf("with the tick: %d unsigned=%v", rec.Code, pk.unsigned)
	}
	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Unsigned packages are allowed") || !strings.Contains(body, "Turn off now") {
		t.Error("the page must warn clearly while unsigned packages are allowed")
	}
	if rec := g.do("POST", "/admin/plugins/unsigned", "owner-token", url.Values{"allow": {"0"}}, nil); rec.Code != 303 || pk.unsigned {
		t.Fatalf("switch off: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/plugins/unsigned", "editor-token", url.Values{"allow": {"1"}, "understand": {"1"}}, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestInstallShowsThePermissionPromptThenGrantsTheTicked(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	rec := g.do("POST", "/admin/plugins/fetch", "owner-token", url.Values{"name": {"seo-tools"}}, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("prompt: %d %s", rec.Code, body)
	}
	for _, want := range []string{"Install seo-tools", "Does &lt;things&gt;", "<code>kv</code>", "<code>mail.send</code>", "api.example.com", "Signed by <code>abc123</code>"} {
		if !strings.Contains(body, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if len(pk.installed) != 0 {
		t.Fatal("nothing may be installed before the owner confirms")
	}
	a11yOK(t, body)
	id := hiddenValue(t, body, "staged")

	// Another user cannot take the staged package.
	if rec := g.do("POST", "/admin/plugins/install", "editor-token", url.Values{"staged": {id}}, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/plugins/install", "owner-token", url.Values{"staged": {id}, "permission": {"kv"}}, nil)
	if rec.Code != 303 || len(pk.installed) != 1 || !slices.Equal(pk.installed[0].grant, []string{"kv"}) || pk.installed[0].source != "registry" {
		t.Fatalf("confirm: %d %+v", rec.Code, pk.installed)
	}
	// Single use.
	if rec := g.do("POST", "/admin/plugins/install", "owner-token", url.Values{"staged": {id}}, nil); rec.Code != http.StatusGone || len(pk.installed) != 1 {
		t.Errorf("replay: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/plugins/fetch", "owner-token", url.Values{"name": {"nope"}}, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "registry has no plugin") {
		t.Errorf("unknown plugin: %d", rec.Code)
	}
}

func hiddenValue(t *testing.T, body, name string) string {
	t.Helper()
	i := strings.Index(body, `name="`+name+`" value="`)
	if i < 0 {
		t.Fatalf("no hidden %s field", name)
	}
	rest := body[i+len(`name="`+name+`" value="`):]
	return rest[:strings.Index(rest, `"`)]
}

func TestUpdatePromptMarksNewPermissions(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	body := g.do("POST", "/admin/plugins/fetch", "owner-token", url.Values{"name": {"hello"}}, nil).Body.String()
	if !strings.Contains(body, "replacing v1.2.0") || !strings.Contains(body, "(new in this version)") || !strings.Contains(body, "Update plugin") {
		t.Errorf("update prompt:\n%s", body)
	}
	if !strings.Contains(body, `value="kv" checked`) || strings.Contains(body, `value="mail.send" checked`) {
		t.Error("an update keeps earlier grants ticked and leaves new permissions unticked")
	}
}

func TestUnsignedPackagePromptWarnsAndShowsProblem(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	pk.problem = "this package is not signed; installing unsigned packages is switched off"
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "x.hmp", "upload:homebrew")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "This package is not signed") || !strings.Contains(body, "It will not be installed") {
		t.Fatalf("%d\n%s", rec.Code, body)
	}
	if strings.Contains(body, "Install plugin</span>") {
		t.Error("there is no install button for a package that would be refused")
	}
	a11yOK(t, body)
}

func TestUploadInstallsThemesAndRefusesGarbage(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	if rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "t.hmp", "theme:dusk"); rec.Code != 303 || len(pk.installed) != 1 || pk.installed[0].source != "upload" || pk.installed[0].grant != nil {
		t.Fatalf("theme upload: %d %+v", rec.Code, pk.installed)
	}
	if rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "x.hmp", "garbage"); rec.Code != 422 || !strings.Contains(rec.Body.String(), "not a valid zip") {
		t.Errorf("garbage: %d", rec.Code)
	}
	if rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "", ""); rec.Code != 400 {
		t.Errorf("no file: %d", rec.Code)
	}
	if rec := postMultipart(g, "/admin/plugins/upload", "editor-token", "t.hmp", "theme:dusk"); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestInstallRefusalIsShownNotLost(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "e.hmp", "theme:evil")
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "signature does not match") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestPluginEnableDisableUninstall(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	if rec := g.do("POST", "/admin/plugins/hello/disable", "owner-token", url.Values{}, nil); rec.Code != 303 || pk.enabled["hello"] {
		t.Fatalf("disable: %d %v", rec.Code, pk.enabled)
	}
	if rec := g.do("POST", "/admin/plugins/hello/enable", "owner-token", url.Values{}, nil); rec.Code != 303 || !pk.enabled["hello"] {
		t.Fatalf("enable: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/plugins/nope/enable", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("unknown: %d", rec.Code)
	}

	rec := g.do("GET", "/admin/plugins/hello/uninstall", "owner-token", nil, nil)
	body := rec.Body.String()
	for _, want := range []string{"Uninstall hello?", "Keep its data", "Delete its data too", "database schema", "not touched"} {
		if !strings.Contains(body, want) {
			t.Errorf("uninstall page missing %q", want)
		}
	}
	a11yOK(t, body)
	for _, want := range []string{`class="hm-confirmcard"`, `name="drop_data" value="0" checked`, `name="drop_data" value="1"`, `hm-btn--danger`} {
		if !strings.Contains(body, want) {
			t.Errorf("uninstall confirm card lacks %q", want)
		}
	}
	if len(pk.removed) != 0 {
		t.Fatal("a GET must not uninstall")
	}
	if rec := g.do("POST", "/admin/plugins/hello/uninstall", "owner-token", url.Values{"drop_data": {"0"}}, nil); rec.Code != 303 || pk.dropped || !slices.Equal(pk.removed, []string{"plugin/hello"}) {
		t.Fatalf("keep data: %d %v dropped=%v", rec.Code, pk.removed, pk.dropped)
	}
	if rec := g.do("POST", "/admin/plugins/hello/uninstall", "owner-token", url.Values{"drop_data": {"1"}}, nil); rec.Code != 303 || !pk.dropped {
		t.Fatalf("drop data: %d dropped=%v", rec.Code, pk.dropped)
	}
	if rec := g.do("POST", "/admin/plugins/hello/uninstall", "editor-token", url.Values{"drop_data": {"1"}}, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

// ---- themes ----

func TestThemesPageListsEverything(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := g.do("GET", "/admin/theme/themes", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"Built in", "sunrise", "Live", "Update to 1.1.0", "Preview", "Activate", "/admin/preview/theme/dusk", "Available themes", "A dark theme", "Upload theme", "Colours and fonts", "/admin/theme/themes/sunrise/settings"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "/admin/theme/themes/sunrise/uninstall") {
		t.Error("the active theme cannot be uninstalled")
	}
	a11yOK(t, body)
	// One primary action per card; the rest sit in the card menu, danger last.
	if n := strings.Count(body, `id="menu-theme-`); n != 3 {
		t.Errorf("theme menus = %d, want one per card", n)
	}
	dusk := body[strings.Index(body, `id="theme-dusk"`):]
	if !strings.Contains(dusk[:strings.Index(dusk, "</section>")], `href="/admin/theme/themes/dusk/uninstall" data-tone="danger"`) {
		t.Error("an inactive theme lacks its Uninstall menu item")
	}
	if !strings.Contains(dusk, `<a role="menuitem" href="/admin/preview/theme/dusk" target="_blank" rel="noopener">`) {
		t.Error("Preview must stay a new-tab menu item")
	}
	if strings.Count(body, "hm-theme--active") != 1 {
		t.Error("exactly one card carries the Live ring")
	}
	if rec := g.do("GET", "/admin/theme/themes", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestThemePreviewIsForOwnersAndEditors(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for _, tok := range []string{"owner-token", "editor-token"} {
		rec := g.do("GET", "/admin/preview/theme/dusk", tok, nil, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/?hm_theme_preview=tok-dusk" {
			t.Errorf("%s: %d %q", tok, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := g.do("GET", "/admin/preview/theme/dusk", "", nil, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("anonymous: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestActivationListsMissingSectionTypes(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	pk.missing = []MissingSection{{Type: "galaxy", Uses: 2}, {Type: "pricing", Uses: 1}}
	rec := g.do("GET", "/admin/theme/themes/dusk/activate", "owner-token", nil, nil)
	body := rec.Body.String()
	for _, want := range []string{"Activate dusk?", "<code>galaxy</code>", "2 times", "<code>pricing</code>", "1 time", "Activate anyway"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	a11yOK(t, body)
	if pk.activated != "" {
		t.Fatal("a GET must not activate")
	}
	pk.missing = nil
	body = g.do("GET", "/admin/theme/themes/dusk/activate", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Every section type your content uses is available") || strings.Contains(body, "Activate anyway") {
		t.Errorf("nothing missing:\n%s", body)
	}
	a11yOK(t, body)
	if rec := g.do("POST", "/admin/theme/themes/dusk/activate", "owner-token", url.Values{}, nil); rec.Code != 303 || pk.activated != "dusk" {
		t.Fatalf("activate: %d %q", rec.Code, pk.activated)
	}
	if rec := g.do("GET", "/admin/theme/themes/ghost/activate", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown theme: %d", rec.Code)
	}
}

func TestThemeUninstall(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	body := g.do("GET", "/admin/theme/themes/dusk/uninstall", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Uninstall dusk?") {
		t.Errorf("confirm page:\n%s", body)
	}
	if rec := g.do("POST", "/admin/theme/themes/dusk/uninstall", "owner-token", url.Values{}, nil); rec.Code != 303 || !slices.Equal(pk.removed, []string{"theme/dusk"}) {
		t.Fatalf("%d %v", rec.Code, pk.removed)
	}
	if rec := g.do("POST", "/admin/theme/themes/sunrise/uninstall", "owner-token", url.Values{}, nil); rec.Code != 409 {
		t.Errorf("active theme: %d", rec.Code)
	}
}

func TestThemeSettingsFormFromSchema(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	rec := g.do("GET", "/admin/theme/themes/sunrise/settings", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Accent word") || !strings.Contains(body, `value="teal"`) {
		t.Fatalf("form: %d\n%s", rec.Code, body)
	}
	a11yOK(t, body)
	if rec := g.do("POST", "/admin/theme/themes/sunrise/settings", "owner-token", url.Values{"accent": {"rose"}}, nil); rec.Code != 303 || len(pk.saved) != 1 || pk.saved[0]["accent"] != "rose" {
		t.Fatalf("save: %d %v", rec.Code, pk.saved)
	}
	rec = g.do("POST", "/admin/theme/themes/sunrise/settings", "owner-token", url.Values{"accent": {"bad"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Not an accent we know") || len(pk.saved) != 1 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	a11yOK(t, rec.Body.String())
	if rec := g.do("GET", "/admin/theme/themes/dusk/settings", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("theme without settings: %d", rec.Code)
	}
}

// themeSite is a fakeSite that also stores theme_extra.
type themeSite struct{ *fakeSite }

func (s themeSite) Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error) {
	if len(in.ThemeExtra) > 0 {
		s.mu.Lock()
		s.cfg.ThemeExtra = in.ThemeExtra
		s.mu.Unlock()
	}
	return s.fakeSite.Update(ctx, in)
}

func TestDesignFormMergesIntoThemeExtra(t *testing.T) {
	t.Parallel()

	_, pk, fp := newPackageRig(t)
	site := newFakeSite("Acme Site")
	g := newRig(t, func(d *Deps) { d.Site = themeSite{site}; d.Growth.Plugins, d.Growth.Packages = fp, pk })
	site.cfg.ThemeExtra = json.RawMessage(`{"headerCta":{"text":"Hi","url":"/x"},"socialLinks":{"github":"https://g.example"}}`)
	rec := g.do("GET", "/admin/theme/colours", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Brand colour") {
		t.Fatalf("form: %d", rec.Code)
	}
	a11yOK(t, rec.Body.String())
	compareGolden(t, "pages/settings-design.html", normalizePage(rec.Body.String()))
	form := url.Values{"primary": {"#112233"}, "secondary": {"#445566"}, "accent": {"#778899"}, "background": {"#ffffff"}, "text": {"#000000"},
		"font": {"Inter, sans-serif"}, "heading_font": {"Georgia, serif"}, "radius_sm": {"2px"}, "radius_md": {"6px"}, "radius_lg": {"1rem"}}
	if rec := g.do("POST", "/admin/theme/colours", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if site.cfg.PrimaryColor != "#112233" {
		t.Errorf("primary colour = %q", site.cfg.PrimaryColor)
	}
	var got map[string]json.RawMessage
	_ = json.Unmarshal(site.cfg.ThemeExtra, &got)
	for _, key := range []string{"headerCta", "socialLinks", "colors", "typography", "borderRadius"} {
		if _, ok := got[key]; !ok {
			t.Errorf("theme object lost or missed %q: %s", key, site.cfg.ThemeExtra)
		}
	}
	if strings.Contains(string(got["colors"]), "primary") {
		t.Errorf("the primary colour lives in its own column: %s", got["colors"])
	}
	bad := url.Values{"primary": {"red"}, "secondary": {"#445566"}, "accent": {"#778899"}, "background": {"#ffffff"}, "text": {"#000000"},
		"font": {"<script>"}, "heading_font": {"Georgia"}, "radius_sm": {"2px"}, "radius_md": {"huge"}, "radius_lg": {"1rem"}}
	rec = g.do("POST", "/admin/theme/colours", "owner-token", bad, nil)
	if rec.Code != 422 || strings.Count(rec.Body.String(), "hm-field__error") != 3 {
		t.Fatalf("invalid: %d, errors %d", rec.Code, strings.Count(rec.Body.String(), "hm-field__error"))
	}
}

// ---- plugin admin pages ----

func TestPluginUIPageIsASandboxedIframe(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := g.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(body, `sandbox="allow-scripts"`) || strings.Contains(body, "allow-same-origin") {
		t.Errorf("the frame must be sandbox=allow-scripts and nothing more:\n%s", body)
	}
	for _, want := range []string{`data-src="/plugin-ui/hello/index.html?t=`, `data-bridge="/admin/plugins/hello/bridge"`, `data-functions="kv.get,log"`, `referrerpolicy="no-referrer"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !regexp.MustCompile(`data-src="/plugin-ui/hello/index\.html\?t=[A-Za-z0-9_-]{32}"`).MatchString(body) {
		t.Errorf("the frame URL carries no 32 character ticket:\n%s", body)
	}
	a11yOK(t, body)
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src 'self'") {
		t.Errorf("admin CSP must let the page frame its own origin only: %q", csp)
	}
	// The old /ui address redirects for both roles.
	for _, token := range []string{"owner-token", "editor-token"} {
		rec := g.do("GET", "/admin/plugins/board/ui", token, nil, nil)
		if rec.Code != 301 || rec.Header().Get("Location") != "/admin/plugins/board/settings" {
			t.Errorf("/ui as %s: %d %q", token, rec.Code, rec.Header().Get("Location"))
		}
	}
	// A role the plugin's page excludes, or an unknown plugin, is a 404 page.
	for _, c := range []struct{ plugin, token string }{{"ownerui", "editor-token"}, {"devonly", "editor-token"}, {"ghost", "owner-token"}} {
		if rec := g.do("GET", "/admin/plugins/"+c.plugin+"/settings", c.token, nil, nil); rec.Code != 404 {
			t.Errorf("%s as %s: %d", c.plugin, c.token, rec.Code)
		}
	}
	// Every installed plugin has a page for the owner, UI or not.
	for _, name := range []string{"devonly", "quiet", "ownerui"} {
		if rec := g.do("GET", "/admin/plugins/"+name+"/settings", "owner-token", nil, nil); rec.Code != 200 {
			t.Errorf("%s as owner: %d", name, rec.Code)
		}
	}
}

func TestPluginUIFilesAreServedUnderAStrictCSP(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := g.do("GET", pluginDoc(g, "owner-token", "hello", "index.html"), "owner-token", nil, pluginFrame)
	if rec.Code != 200 || rec.Body.String() != "<h1>hi</h1>" {
		t.Fatalf("document: %d %q", rec.Code, rec.Body.String())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts", "default-src 'none'", "script-src 'self'", "connect-src 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP is too loose: %q", csp)
	}
	// The document needs a session, a role the page is open to and a ticket...
	if rec := g.do("GET", "/plugin-ui/hello/index.html", "", nil, pluginFrame); rec.Code != 404 {
		t.Errorf("anonymous document: %d", rec.Code)
	}
	if rec := g.do("GET", "/plugin-ui/hello/index.html", "owner-token", nil, pluginFrame); rec.Code != 404 {
		t.Errorf("document without a ticket: %d", rec.Code)
	}
	if rec := g.do("GET", pluginDoc(g, "editor-token", "ownerui", "index.html"), "editor-token", nil, pluginFrame); rec.Code != 404 {
		t.Errorf("editor on an owner-only page: %d", rec.Code)
	}
	// ...scripts and styles are static files (a sandboxed frame sends no cookies).
	if rec := g.do("GET", "/plugin-ui/hello/app.js", "", nil, pluginScript); rec.Code != 200 {
		t.Errorf("script: %d", rec.Code)
	}
	if rec := g.do("GET", "/plugin-ui/hello/missing.js", "", nil, pluginScript); rec.Code != 404 {
		t.Errorf("missing: %d", rec.Code)
	}
	// The client library the page includes.
	rec = g.do("GET", "/plugin-ui/_sdk/bridge.js", "", nil, pluginScript)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "window.hatamoto") || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Errorf("sdk: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func bridge(g *rig, plugin, token string, req map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(req)
	r := httptest.NewRequest("POST", "/admin/plugins/"+plugin+"/bridge", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	g.addFetchMeta(r, token, nil)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	return rec
}

func TestBridgeCarriesOnlyGrantedCapabilities(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)

	rec := bridge(g, "hello", "owner-token", map[string]any{"op": "route", "method": "POST", "path": "/leads/7", "query": map[string]any{"q": "a b"}, "body": `{"x":1}`})
	var rep bridgeRouteReply
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rep) != nil || rep.Status != 200 || rep.Body != `{"echo":true}` {
		t.Fatalf("route: %d %s", rec.Code, rec.Body.String())
	}
	if len(pk.routeCall) != 1 || pk.routeCall[0] != `hello POST leads/7?q=a+b body={"x":1} as=owner` {
		t.Errorf("the route was called as %v", pk.routeCall)
	}

	if rec := bridge(g, "hello", "owner-token", map[string]any{"op": "host", "fn": "kv.get", "args": map[string]any{"key": "k"}}); rec.Code != 200 || len(pk.hostCall) != 1 || pk.hostCall[0] != "hello kv.get" {
		t.Fatalf("granted host call: %d %v", rec.Code, pk.hostCall)
	}
	// A function the plugin holds no permission for never reaches the host.
	if rec := bridge(g, "hello", "owner-token", map[string]any{"op": "host", "fn": "mail.send", "args": map[string]any{}}); rec.Code != 403 || len(pk.hostCall) != 1 {
		t.Errorf("ungranted host call: %d %v", rec.Code, pk.hostCall)
	}

	for name, req := range map[string]map[string]any{
		"unknown op":    {"op": "eval"},
		"relative path": {"op": "route", "method": "GET", "path": "leads"},
		"query in path": {"op": "route", "method": "GET", "path": "/a?b=c"},
		"bad method":    {"op": "route", "method": "TRACE", "path": "/a"},
		"backslash":     {"op": "route", "method": "GET", "path": `/a\b`},
	} {
		if rec := bridge(g, "hello", "owner-token", req); rec.Code != 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// Not another plugin's page, not a role the page excludes, not anonymous.
	if rec := bridge(g, "devonly", "owner-token", map[string]any{"op": "route", "method": "GET", "path": "/a"}); rec.Code != 404 {
		t.Errorf("plugin without a page: %d", rec.Code)
	}
	if rec := bridge(g, "ownerui", "editor-token", map[string]any{"op": "route", "method": "GET", "path": "/a"}); rec.Code != 404 {
		t.Errorf("editor on an owner-only page: %d", rec.Code)
	}
	if rec := bridge(g, "hello", "", map[string]any{"op": "route", "method": "GET", "path": "/a"}); rec.Code != 303 {
		t.Errorf("anonymous: %d", rec.Code)
	}
	// CSRF: a request without the token header is refused.
	r := httptest.NewRequest("POST", "/admin/plugins/hello/bridge", strings.NewReader(`{"op":"route","method":"GET","path":"/a"}`))
	r.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(r, "owner-token", map[string]string{"X-CSRF-Token": ""}) // deliberately no token header
	rec2 := httptest.NewRecorder()
	g.h.ServeHTTP(rec2, r)
	if rec2.Code != 403 {
		t.Errorf("no CSRF header: %d", rec2.Code)
	}
}

func TestPluginPagesJoinTheSubnavByRole(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	owner := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	for _, name := range []string{"hello", "ownerui", "board", "quiet", "devonly"} {
		if !strings.Contains(owner, `href="/admin/plugins/`+name+`/settings"`) {
			t.Errorf("the owner's subnav lacks %s", name)
		}
	}
	editor := g.do("GET", "/admin/plugins/board/settings", "editor-token", nil, nil).Body.String()
	if strings.Contains(editor, "/admin/plugins/ownerui/settings") || strings.Contains(editor, "All plugins") {
		t.Error("an owner-only plugin and All plugins must not be in the editor's subnav")
	}
	if !strings.Contains(editor, `href="/admin/plugins/board/settings"`) {
		t.Error("the editor's subnav lists the plugins open to them")
	}
}

func TestPackageScreensAnswerWithoutAManager(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, p := range []string{"/admin/theme/themes", "/admin/theme/colours", "/admin/theme/themes/x/activate", "/admin/plugins/x/ui"} {
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code == 502 || rec.Code == 0 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestRegistryErrorShowsAFriendlyEmptyState(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	pk.registry = RegistryView{Err: "registry: https://hatamoto.dev/registry.json answered 404 Not Found", Host: "hatamoto.dev"}
	for path, kind := range map[string]string{"/admin/theme/themes": "theme"} {
		rec := g.do("GET", path, "owner-token", nil, nil)
		body := rec.Body.String()
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		for _, want := range []string{
			"No registry is available right now.",
			"Hatamoto could not load the " + kind + " list from hatamoto.dev. You can still upload a signed .hmp.",
			"Upload .hmp", "<details><summary>Details</summary><code>registry: https://hatamoto.dev/registry.json answered 404 Not Found</code></details>",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
		if strings.Count(body, "answered 404") != 1 || strings.Contains(body, "The registry could not be read") {
			t.Errorf("%s: the raw error must appear once, inside the details", path)
		}
		a11yOK(t, body)
	}
}

func TestThemeCardPaletteStripNeutralWithoutColours(t *testing.T) {
	t.Parallel()

	live := ThemeInfo{Name: "sunrise", Active: true}
	other := ThemeInfo{Name: "dusk"}
	if p := themePalette(other, &siteconfig.Config{}); len(p) != 5 || p[0].Hex != "" || p[4].X != 80 {
		t.Errorf("inactive theme palette %+v", p)
	}
	if p := themePalette(live, nil); p[0].Hex != "" {
		t.Errorf("no site config gives colours: %+v", p)
	}
	cfg := &siteconfig.Config{PrimaryColor: "#112233", ThemeExtra: json.RawMessage(`{"colors":{"secondary":"nonsense"}}`)}
	if p := themePalette(live, cfg); p[0].Hex != "" {
		t.Errorf("a broken colour must leave the strip neutral: %+v", p)
	}
	good := &siteconfig.Config{PrimaryColor: "#112233"}
	if p := themePalette(live, good); p[0].Hex != "#112233" || p[1].Hex == "" {
		t.Errorf("live theme palette %+v", p)
	}
}

func TestPluginsUnsignedAllowedIsACallout(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	pk.unsigned = true
	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"hm-callout--danger", "Unsigned packages are allowed until you turn it off.", "Turn off now", `action="/admin/plugins/unsigned/off"`, `id="installed"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "hm-plugin__error") {
		t.Error("the old error paragraph is back")
	}
	a11yOK(t, body)
	pk.unsigned = false
	if body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String(); strings.Contains(body, "unsigned-banner") || strings.Contains(body, "Available plugins") || strings.Contains(body, "/plugins#unsigned") {
		t.Error("the banner shows while the opt-in is off, or an old section is back")
	}
}

// The Browse, detail, check-updates and Package security routes are owner only,
// and mounting them next to the /plugins/{name}/... routes does not conflict
// (the mux panics at mount otherwise, so any rig proves it).
func TestPluginAdminRoutesAreOwnerOnly(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) {
		d.Growth.Plugins = &fakePlugins{settings: map[string]map[string]any{}}
		d.Growth.Packages = &fakePackages{enabled: map[string]bool{}}
	})
	for _, tc := range []struct{ method, path string }{
		{"GET", "/admin/plugins/browse"},
		{"GET", "/admin/plugins/hello/about"},
		{"GET", "/admin/plugins/security"},
		{"POST", "/admin/plugins/check-updates"},
		{"POST", "/admin/plugins/unsigned/off"},
	} {
		if rec := g.do(tc.method, tc.path, "editor-token", nil, nil); rec.Code != http.StatusForbidden {
			t.Errorf("editor %s %s = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
}
