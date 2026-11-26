package admin

import (
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func TestLaunchHidden(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		extra string
		nilC  bool
		want  bool
	}{
		{"nil config", "", true, false},
		{"empty", "", false, false},
		{"other keys only", `{"additional_css":"a{}"}`, false, false},
		{"true", `{"launch_checklist_hidden":true}`, false, true},
		{"true among others", `{"additional_css":"a{}","launch_checklist_hidden":true}`, false, true},
		{"false", `{"launch_checklist_hidden":false}`, false, false},
		{"wrong type", `{"launch_checklist_hidden":"yes"}`, false, false},
		{"invalid JSON", `{"launch_checklist_hidden":`, false, false},
	} {
		var cfg *siteconfig.Config
		if !tc.nilC {
			cfg = &siteconfig.Config{ThemeExtra: []byte(tc.extra)}
		}
		if got := launchHidden(cfg); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func newLaunchHideRig(t *testing.T) (*rig, *fakeSite) {
	t.Helper()
	site := newFakeSite("Acme Site")
	site.cfg.ThemeExtra = []byte(`{"additional_css":"a{}"}`)
	return newRig(t, func(d *Deps) { d.Site = site }), site
}

func TestLaunchChecklistHideOwner(t *testing.T) {
	t.Parallel()
	g, site := newLaunchHideRig(t)
	rec := g.do("POST", "/admin/launch-checklist/hide", "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/" {
		t.Fatalf("hide = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if !launchHidden(&site.cfg) {
		t.Errorf("flag not stored: %s", site.cfg.ThemeExtra)
	}
	if got := string(site.cfg.ThemeExtra); !strings.Contains(got, `"additional_css":"a{}"`) {
		t.Errorf("other keys lost: %s", got)
	}
}

func TestLaunchChecklistHideRefusals(t *testing.T) {
	t.Parallel()
	g, site := newLaunchHideRig(t)
	before := string(site.cfg.ThemeExtra)
	if rec := g.do("POST", "/admin/launch-checklist/hide", "editor-token", nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	g.realTokens()
	if rec := g.do("POST", "/admin/launch-checklist/hide", "owner-token", nil, map[string]string{"X-CSRF-Token": ""}); rec.Code == http.StatusSeeOther {
		t.Errorf("no token = %d, want a refusal", rec.Code)
	}
	if got := string(site.cfg.ThemeExtra); got != before {
		t.Errorf("a refused request wrote: %s", got)
	}
}

func TestLaunchChecklistHideWithoutSite(t *testing.T) {
	t.Parallel()
	g, _ := newLaunchHideRig(t)
	g.a.deps.Site = nil
	if rec := g.do("POST", "/admin/launch-checklist/hide", "owner-token", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no site = %d, want 503", rec.Code)
	}
}
