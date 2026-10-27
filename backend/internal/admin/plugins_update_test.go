package admin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// updateRig has: alpha and beta with plain updates, gtm whose update widens
// access (site.scripts), perms whose update asks for a new permission, fine
// up to date, and rows that must never show update UI.
func updateRig(t *testing.T) (*rig, *fakePackages, *fakePlugins) {
	t.Helper()
	pkg := func(update string, newPerms ...string) *PackageMeta {
		return &PackageMeta{Enabled: true, Signed: true, Signer: "abc123", Source: "registry", Update: update, UpdateNew: newPerms}
	}
	fp := &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{
		{Name: "beta", Version: "1.0.0", Loaded: true, Package: pkg("1.1.0")},
		{Name: "alpha", Version: "0.1.0", Loaded: true, Package: pkg("0.2.0")},
		{Name: "gtm", Version: "0.1.0", Loaded: true, Package: pkg("0.2.0", "site.scripts")},
		{Name: "fine", Version: "3.0.0", Loaded: true, Package: pkg("")},
		{Name: "dirplugin", Version: "0.0.1", Loaded: true},
		{Name: "bad", Version: "1.0.0", Package: &PackageMeta{Enabled: true, Blocked: "untrusted signer", Update: "2.0.0"}},
		{Name: "leaving", Version: "1.0.0", Package: &PackageMeta{Enabled: true, Uninstalling: true, Update: "2.0.0"}},
		{Name: "webhooks", Version: "1.0.0", Loaded: true, Package: &PackageMeta{Enabled: true, Update: "2.0.0"}},
	}}
	pk := &fakePackages{enabled: map[string]bool{}, settings: map[string]any{},
		fetchAs: map[string]string{"gtm": "plugin-scripts:gtm"},
		registry: RegistryView{Entries: []RegistryEntry{
			{Kind: "plugin", Name: "beta", Version: "1.1.0", Installed: true, Update: true},
			{Kind: "plugin", Name: "alpha", Version: "0.2.0", Installed: true, Update: true},
			{Kind: "plugin", Name: "gtm", Version: "0.2.0", Installed: true, Update: true},
			{Kind: "plugin", Name: "fine", Version: "3.0.0", Installed: true},
			// Listed at their current version, yet they must not say "Up to date".
			{Kind: "plugin", Name: "bad", Version: "1.0.0", Installed: true},
			{Kind: "plugin", Name: "leaving", Version: "1.0.0", Installed: true},
			{Kind: "plugin", Name: "webhooks", Version: "1.0.0", Installed: true},
			{Kind: "plugin", Name: "dirplugin", Version: "0.0.1", Installed: true},
		}}}
	g := newRig(t, func(d *Deps) { d.Growth.Plugins, d.Growth.Packages = fp, pk })
	return g, pk, fp
}

var tagRE = regexp.MustCompile(`<[^>]*>`)

// visibleText is the page as a reader sees it: tags dropped, white space
// collapsed. Tests that check wording use it so a class name or wrapper
// element can change without breaking them.
func visibleText(html string) string {
	return strings.Join(strings.Fields(tagRE.ReplaceAllString(html, " ")), " ")
}

// updateFlash reads the message the response left for the next page.
func updateFlash(t *testing.T, rec *httptest.ResponseRecorder) Flash {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name != flashCookie {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.SplitN(c.Value, ".", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		var f Flash
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	t.Fatalf("no flash set (status %d)", rec.Code)
	return Flash{}
}

func TestPluginsListShowsVersionsAndUpdates(t *testing.T) {
	t.Parallel()

	g, _, _ := updateRig(t)
	rec := g.do("GET", "/admin/plugins", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		`action="/admin/plugins/alpha/update"`, `aria-label="Update alpha to v0.2.0"`,
		`aria-label="Update beta to v1.1.0"`, `aria-label="Review the update of gtm to v0.2.0"`,
		`action="/admin/plugins/update-all"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	text := visibleText(body)
	for _, want := range []string{"v1.0.0", "v3.0.0", "v0.0.1", "Update to v0.2.0", "Review update to v0.2.0", "new: site.scripts", "Up to date", "Update all (2)"} {
		if !strings.Contains(text, want) {
			t.Errorf("page text is missing %q", want)
		}
	}
	if n := strings.Count(text, "Up to date"); n != 1 {
		t.Errorf("Up to date shown %d times, want 1 (only fine, not blocked, uninstalling, legacy or directory rows)", n)
	}
	// Review goes through the existing prompt flow, never the one-click route.
	if strings.Contains(body, `action="/admin/plugins/gtm/update"`) {
		t.Error("a review update has a one-click form")
	}
	for _, name := range []string{"bad", "leaving", "webhooks", "dirplugin"} {
		if strings.Contains(body, `/admin/plugins/`+name+`/update`) || strings.Contains(body, `Review the update of `+name) {
			t.Errorf("%s shows update UI", name)
		}
	}
	a11yOK(t, body)
}

func TestPluginsListQuietWithoutRegistry(t *testing.T) {
	t.Parallel()

	for name, view := range map[string]RegistryView{"off": {Off: true}, "failed": {Err: "dial tcp: refused", Host: "hatamoto.dev"}} {
		t.Run(name, func(t *testing.T) {
			g, pk, fp := updateRig(t)
			pk.registry = view
			// With no registry the adapter reports no update either.
			for i := range fp.list {
				if fp.list[i].Package != nil {
					fp.list[i].Package.Update, fp.list[i].Package.UpdateNew = "", nil
				}
			}
			rec := g.do("GET", "/admin/plugins", "owner-token", nil, nil)
			body := rec.Body.String()
			if rec.Code != 200 {
				t.Fatalf("status %d", rec.Code)
			}
			for _, gone := range []string{"Up to date", "Update all", "Update to v", "Review update", "/update\""} {
				if strings.Contains(body, gone) {
					t.Errorf("registry %s: page still has %q", name, gone)
				}
			}
			if !strings.Contains(visibleText(body), "v1.0.0") {
				t.Error("versions are missing")
			}
		})
	}
}

func TestPluginUpdateOneClick(t *testing.T) {
	t.Parallel()

	g, pk, _ := updateRig(t)
	rec := g.do("POST", "/admin/plugins/alpha/update", "owner-token", url.Values{"version": {"9.9.9"}, "grant": {"everything"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/plugins" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(pk.installed) != 1 {
		t.Fatalf("installs: %+v", pk.installed)
	}
	c := pk.installed[0]
	if c.data != "plugin:alpha" || c.source != "registry" || c.grant != nil || c.confirm {
		t.Errorf("install call %+v: want registry data, nil grant, no confirm", c)
	}
	if f := updateFlash(t, rec); f.Kind != "success" || f.Message != "Updated alpha to v2.0.0." {
		t.Errorf("flash %+v", f)
	}
}

// An update that needs confirmation (or adds permissions) is shown, not installed.
func TestPluginUpdateNeedingReviewShowsThePrompt(t *testing.T) {
	t.Parallel()

	g, pk, fp := updateRig(t)
	rec := g.do("POST", "/admin/plugins/gtm/update", "owner-token", url.Values{}, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="staged"`) || !strings.Contains(body, "site.scripts") {
		t.Fatalf("want the permission prompt, got %d %s", rec.Code, body)
	}
	if len(pk.installed) != 0 {
		t.Fatalf("installed without review: %+v", pk.installed)
	}
	// New non-gated permissions: Inspect says no confirmation, still review.
	fp.list[0].Package.UpdateNew = []string{"mail.send"}
	rec = g.do("POST", "/admin/plugins/beta/update", "owner-token", url.Values{}, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="staged"`) || len(pk.installed) != 0 {
		t.Fatalf("new permission: %d installed=%+v", rec.Code, pk.installed)
	}
}

func TestPluginUpdateRefusals(t *testing.T) {
	t.Parallel()

	g, pk, _ := updateRig(t)
	for _, tc := range []struct {
		name string
		code int
	}{{"ghost", 404}, {"fine", 422}, {"dirplugin", 422}, {"bad", 422}, {"leaving", 422}, {"webhooks", 422}} {
		if rec := g.do("POST", "/admin/plugins/"+tc.name+"/update", "owner-token", url.Values{}, nil); rec.Code != tc.code {
			t.Errorf("%s: %d, want %d", tc.name, rec.Code, tc.code)
		}
	}
	pk.problem = "signer is not trusted"
	if rec := g.do("POST", "/admin/plugins/alpha/update", "owner-token", url.Values{}, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "signer is not trusted") {
		t.Errorf("problem: %d %s", rec.Code, rec.Body.String())
	}
	if len(pk.installed) != 0 {
		t.Errorf("installed: %+v", pk.installed)
	}
}

func TestPluginUpdateAllMixed(t *testing.T) {
	t.Parallel()

	g, pk, _ := updateRig(t)
	rec := g.do("POST", "/admin/plugins/update-all", "owner-token", url.Values{"name": {"gtm"}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(pk.installed) != 2 || pk.installed[0].data != "plugin:alpha" || pk.installed[1].data != "plugin:beta" {
		t.Fatalf("installs (name order, safe only): %+v", pk.installed)
	}
	for _, c := range pk.installed {
		if c.grant != nil || c.confirm {
			t.Errorf("bulk install passed grant/confirm: %+v", c)
		}
	}
	f := updateFlash(t, rec)
	if f.Kind != "success" || f.Message != "Updated 2 plugins (alpha, beta). 1 needs review: gtm." {
		t.Errorf("flash %+v", f)
	}
}

func TestPluginUpdateAllSkipsConfirmationFromInspect(t *testing.T) {
	t.Parallel()

	g, pk, _ := updateRig(t)
	pk.fetchAs["alpha"] = "plugin-any:alpha" // the registry copy widens access, though the list did not know
	rec := g.do("POST", "/admin/plugins/update-all", "owner-token", url.Values{}, nil)
	if len(pk.installed) != 1 || pk.installed[0].data != "plugin:beta" {
		t.Fatalf("installs: %+v", pk.installed)
	}
	if f := updateFlash(t, rec); f.Message != "Updated 1 plugin (beta). 2 need review: alpha, gtm." {
		t.Errorf("flash %q", f.Message)
	}
}

// A review-only plugin is never downloaded, so its failing download cannot
// stop the safe updates.
func TestPluginUpdateAllReviewOnlyFetchFailureDoesNotStop(t *testing.T) {
	t.Parallel()

	g, pk, _ := updateRig(t)
	pk.fetchFails = map[string]string{"gtm": "download timed out"}
	rec := g.do("POST", "/admin/plugins/update-all", "owner-token", url.Values{}, nil)
	if len(pk.installed) != 2 || pk.installed[0].data != "plugin:alpha" || pk.installed[1].data != "plugin:beta" {
		t.Fatalf("installs: %+v", pk.installed)
	}
	if f := updateFlash(t, rec); f.Kind != "success" || f.Message != "Updated 2 plugins (alpha, beta). 1 needs review: gtm." {
		t.Errorf("flash %+v", f)
	}
}

// When the run stops early, plugins that need review are still named.
func TestPluginUpdateAllEarlyStopNamesReviewPlugins(t *testing.T) {
	t.Parallel()

	g, pk, _ := updateRig(t)
	pk.installFails = map[string]string{"plugin:alpha": "boom"}
	rec := g.do("POST", "/admin/plugins/update-all", "owner-token", url.Values{}, nil)
	f := updateFlash(t, rec)
	if f.Kind != "error" || !strings.Contains(f.Message, "1 needs review: gtm.") || !strings.Contains(f.Message, "Stopped; 1 more not tried.") {
		t.Errorf("flash %+v", f)
	}
}

func TestPluginUpdateAllStopsAtFirstError(t *testing.T) {
	t.Parallel()

	g, pk, _ := updateRig(t)
	pk.installFails = map[string]string{"plugin:alpha": "package signature does not match its contents"}
	rec := g.do("POST", "/admin/plugins/update-all", "owner-token", url.Values{}, nil)
	if len(pk.installed) != 0 {
		t.Fatalf("beta was tried after alpha failed: %+v", pk.installed)
	}
	f := updateFlash(t, rec)
	if f.Kind != "error" || !strings.Contains(f.Message, "Could not update alpha: package signature does not match") || !strings.Contains(f.Message, "Stopped; 1 more not tried.") {
		t.Errorf("flash %+v", f)
	}
	// An earlier success is still reported.
	pk.installFails = map[string]string{"plugin:beta": "boom"}
	rec = g.do("POST", "/admin/plugins/update-all", "owner-token", url.Values{}, nil)
	if f := updateFlash(t, rec); !strings.HasPrefix(f.Message, "Updated 1 plugin (alpha).") || !strings.Contains(f.Message, "Could not update beta: boom") {
		t.Errorf("flash %q", f.Message)
	}
}

func TestPluginUpdateAuth(t *testing.T) {
	t.Parallel()

	g, pk, _ := updateRig(t)
	for _, target := range []string{"/admin/plugins/alpha/update", "/admin/plugins/update-all"} {
		if rec := g.do("POST", target, "editor-token", url.Values{}, nil); rec.Code != 403 {
			t.Errorf("%s editor: %d", target, rec.Code)
		}
		req := httptest.NewRequest("POST", target, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
		req.Header.Set("Sec-Fetch-Site", "same-origin") // deliberately no token: it is the token that refuses
		rec := httptest.NewRecorder()
		g.h.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Errorf("%s without CSRF: %d", target, rec.Code)
		}
	}
	if len(pk.installed) != 0 {
		t.Errorf("installed: %+v", pk.installed)
	}
}

// The registry listed no new permissions (UpdateNew is empty), but the
// downloaded package needs confirmation: one click shows the prompt and does
// not install. This is the Inspect-only path, and the downgrade race (the
// registry entry is older than what is installed by the time of the click).
func TestPluginUpdateOneClickInspectOnlyConfirm(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, fetch string
		want        []string
	}{
		{"new allowed host", "plugin-any:alpha", []string{"needs your confirmation", "any public web address"}},
		{"downgrade race", "plugin-old:alpha", []string{"needs your confirmation", "is not newer than the installed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, pk, fp := updateRig(t)
			if len(fp.list[1].Package.UpdateNew) != 0 {
				t.Fatal("alpha must have no UpdateNew in the list")
			}
			pk.fetchAs["alpha"] = tc.fetch
			rec := g.do("POST", "/admin/plugins/alpha/update", "owner-token", url.Values{}, nil)
			body := rec.Body.String()
			if rec.Code != 200 || !strings.Contains(body, `name="staged"`) {
				t.Fatalf("want the permission prompt, got %d %s", rec.Code, body)
			}
			text := visibleText(body)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("prompt text is missing %q", want)
				}
			}
			if len(pk.installed) != 0 {
				t.Errorf("installed without review: %+v", pk.installed)
			}
		})
	}
}
