package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The Theme area: one tab per section, in the order of the owner's brief, and
// each tab shows exactly when its route answers the role.
func TestThemeForRoles(t *testing.T) {
	t.Parallel()

	joined := func(role string) string {
		var ids []string
		for _, s := range ThemeFor(role) {
			ids = append(ids, s.ID)
		}
		return strings.Join(ids, " ")
	}
	if got, want := joined("owner"), "themes colours design-system presets parts blocks templates menus"; got != want {
		t.Errorf("owner Theme sections %q, want %q", got, want)
	}
	if got, want := joined("editor"), "blocks templates menus"; got != want {
		t.Errorf("editor Theme sections %q, want %q", got, want)
	}
	if got, want := joined("nobody-role"), "templates menus"; got != want {
		t.Errorf("other roles Theme sections %q, want %q", got, want)
	}
	if secs := ThemeFor("editor"); secs[0].Href != "/admin/theme/blocks" {
		t.Errorf("an editor's Theme entry is %q", secs[0].Href)
	}
}

func TestThemeEntryAndOwnerOnlyTabs(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, tc := range []struct {
		token, path string
		code        int
		to          string
	}{
		{"owner-token", "/admin/theme", 303, "/admin/theme/themes"},
		{"owner-token", "/admin/theme?section=menus", 303, "/admin/theme/menus"},
		{"editor-token", "/admin/theme", 303, "/admin/theme/blocks"},
		{"editor-token", "/admin/theme?section=themes", 303, "/admin/theme/blocks"},
		{"editor-token", "/admin/theme?section=menus", 303, "/admin/theme/menus"},
		{"editor-token", "/admin/theme?section=blocks", 303, "/admin/theme/blocks"},
	} {
		rec := g.do("GET", tc.path, tc.token, nil, nil)
		if rec.Code != tc.code || rec.Header().Get("Location") != tc.to {
			t.Errorf("%s %s: %d %q, want %d %q", tc.token, tc.path, rec.Code, rec.Header().Get("Location"), tc.code, tc.to)
		}
	}
	for _, p := range []string{"/admin/theme/themes", "/admin/theme/colours", "/admin/theme/design-system", "/admin/theme/presets", "/admin/theme/parts", "/admin/theme/themes/paper/activate"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != http.StatusForbidden {
			t.Errorf("editor %s: %d, want 403", p, rec.Code)
		}
	}
	for _, p := range []string{"/admin/theme/templates", "/admin/theme/menus"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code == http.StatusForbidden {
			t.Errorf("editor %s: 403, want it open to editors", p)
		}
	}
}

func TestThemeFrameRendersForThemePages(t *testing.T) {
	t.Parallel()

	g, _ := newMenuRig(t)
	rec := g.do("GET", "/admin/theme/menus", "editor-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	has(t, body,
		`<nav class="hm-subnav" aria-label="Theme">`, `<h2 class="hm-subnav__title">Theme</h2>`,
		`<a class="hm-subnav__link" href="/admin/theme/menus" aria-current="page">`,
		`<a class="hm-subnav__link" href="/admin/theme/templates">`,
		`<form class="hm-subnav-select" method="get" action="/admin/theme">`, `id="hm-theme-section"`,
		`<a class="hm-nav-link" href="/admin/theme" aria-current="page">`,
		`<h1>Menus</h1>`)
	lacks(t, body, `href="/admin/theme/themes"`, `aria-label="Settings"`)
}

// The Theme area sits under /admin/theme, which is also the exact path of the
// colour-scheme switch: only that one path skips the complete-profile gate.
func TestProfileGateExemptsOnlyTheSchemeSwitch(t *testing.T) {
	t.Parallel()

	if !profileGateExempted("/admin/theme") {
		t.Error("the colour scheme switch must stay reachable")
	}
	for _, p := range []string{"/admin/theme/themes", "/admin/theme/menus", "/admin/theme/blocks"} {
		if profileGateExempted(p) {
			t.Errorf("%s skips the profile gate", p)
		}
	}
}

// Old Appearance and Menus URLs keep working for every method: GET lands with a
// 301, a POST with a 308 so its body is sent again to the new path.
func TestOldThemeURLsRedirectWithMethodAndQuery(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", "/admin/settings/themes/presets?x=1", "owner-token", nil, nil)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/admin/theme/presets?x=1" {
		t.Errorf("GET: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = g.do("POST", "/admin/settings/themes/tokens/reset", "owner-token", url.Values{"a": {"b"}}, nil)
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/admin/theme/design-system/reset" {
		t.Errorf("POST: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
