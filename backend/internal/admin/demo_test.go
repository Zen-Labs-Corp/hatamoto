package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
)

func demoRig(t *testing.T) *rig {
	t.Helper()
	return newRig(t, func(d *Deps) {
		d.Demo = config.DemoConfig{Enabled: true, LoginEmail: "demo@example.test", LoginPassword: "pw-from-env"}
	})
}

var demoPosts = []struct{ name, path string }{
	{"upload", "/admin/plugins/upload"},
	{"allow unsigned", "/admin/plugins/unsigned"},
	{"stop unsigned", "/admin/plugins/unsigned/off"},
	{"check plugin updates", "/admin/plugins/check-updates"},
	{"create user", "/admin/settings/users"},
	{"invite", "/admin/settings/users/invite"},
	{"change user", "/admin/settings/users/7"},
	{"reset user password", "/admin/settings/users/7/reset"},
	{"own password", "/admin/profile/password"},
	{"forgot password", "/admin/forgot"},
	{"create api key", "/admin/settings/api-keys"},
	{"regenerate api key", "/admin/settings/api-keys/7/regenerate"},
	{"webhooks", "/admin/settings/webhooks"},
	{"smtp", "/admin/settings/email"},
	{"send test email", "/admin/settings/email/test"},
	{"captcha keys", "/admin/forms/spam"},
	{"wordpress import", "/admin/settings/import/wordpress"},
	{"update check", "/admin/settings/updates/check"},
	{"apply update", "/admin/settings/updates/apply"},
}

// Every refusal answers 403 with a message and never reaches the handler.
func TestDemoRefusesWith403AndAMessage(t *testing.T) {
	t.Parallel()

	g := demoRig(t)
	for _, tc := range demoPosts {
		t.Run(tc.name, func(t *testing.T) {
			rec := g.do("POST", tc.path, "owner-token", url.Values{"x": {"1"}}, nil)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "public demo") {
				t.Errorf("the page does not say why: %s", rec.Body.String())
			}
		})
	}
}

func TestDemoRefusalIsAToastForHTMX(t *testing.T) {
	t.Parallel()

	g := demoRig(t)
	rec := g.do("POST", "/admin/settings/users", "owner-token", url.Values{"x": {"1"}}, map[string]string{"HX-Request": "true", "HX-Target": "main"})
	if rec.Code != http.StatusForbidden || rec.Header().Get("HX-Reswap") != "none" {
		t.Fatalf("htmx: %d, reswap %q", rec.Code, rec.Header().Get("HX-Reswap"))
	}
}

func TestDemoBannerAndLogin(t *testing.T) {
	t.Parallel()

	g := demoRig(t)
	login := g.do("GET", "/admin/login", "", nil, nil).Body.String()
	for _, want := range []string{config.DemoBanner, "demo@example.test", "pw-from-env"} {
		if !strings.Contains(login, want) {
			t.Errorf("sign-in page misses %q", want)
		}
	}
	if home := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); !strings.Contains(home, config.DemoBanner) {
		t.Error("the admin shell misses the demo banner")
	}
	// a failed sign-in keeps showing the login
	bad := g.do("POST", "/admin/login", "", url.Values{"email": {"x@example.test"}, "password": {"no"}}, nil)
	if bad.Code == http.StatusForbidden {
		t.Errorf("sign-in must stay open in demo mode: %d", bad.Code)
	}
}

var demoPitches = map[string]config.DemoPitch{
	"home":               {Title: "One program", Body: "Home pitch."},
	"analytics":          {Title: "AI analytics", Body: "Section pitch <b>escaped</b>."},
	"analytics/crawlers": {Title: "Crawlers", Body: "Page pitch."},
}

// Each admin section shows its pitch with the demo-only footnote; a section
// without one shows none.
func TestDemoPitchPerSection(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) {
		d.Demo = config.DemoConfig{Enabled: true, Pitches: demoPitches}
	})
	for path, want := range map[string]string{
		"/admin/analytics/traffic":  "Section pitch",
		"/admin/analytics/crawlers": "Page pitch.",
	} {
		page := g.do("GET", path, "owner-token", nil, nil).Body.String()
		if !strings.Contains(page, want) || !strings.Contains(page, config.DemoPitchFootnote) || !strings.Contains(page, "data-hm-demo-pitch") {
			t.Errorf("%s: no pitch %q with the footnote", path, want)
		}
	}
	if page := g.do("GET", "/admin/analytics/traffic", "owner-token", nil, nil).Body.String(); strings.Contains(page, "<b>escaped</b>") {
		t.Error("pitch text is not escaped")
	}
	if page := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(page, "data-hm-demo-pitch") {
		t.Error("a section without a pitch shows one")
	}
}

// A pitch file means nothing without HATAMOTO_DEMO.
func TestDemoPitchOffShowsNothing(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) {
		d.Demo = config.DemoConfig{Pitches: demoPitches}
	})
	if page := g.do("GET", "/admin/analytics/traffic", "owner-token", nil, nil).Body.String(); strings.Contains(page, "data-hm-demo-pitch") || strings.Contains(page, config.DemoPitchFootnote) {
		t.Error("pitch with the demo off")
	}
}

// Without HATAMOTO_DEMO nothing changes: no banner, no login hint, no refusal.
func TestDemoOffChangesNothing(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if login := g.do("GET", "/admin/login", "", nil, nil).Body.String(); strings.Contains(login, "Public demo") || strings.Contains(login, "<code>") {
		t.Error("sign-in page shows demo content with the demo off")
	}
	if page := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(page, config.DemoBanner) {
		t.Error("banner with the demo off")
	}
	for _, tc := range demoPosts {
		rec := g.do("POST", tc.path, "owner-token", url.Values{"x": {"1"}}, nil)
		if strings.Contains(rec.Body.String(), "public demo") {
			t.Errorf("%s: refused as a demo with the demo off (%d)", tc.name, rec.Code)
		}
	}
}
