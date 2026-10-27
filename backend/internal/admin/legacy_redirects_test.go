package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// legacyMap is every row of the v1-23a old to new URL map with a concrete
// example. Query strings are appended by the test.
var legacyMap = []struct{ old, to string }{
	{"/admin/analytics/agents", "/admin/analytics/crawlers"},
	{"/admin/settings/site", "/admin/settings/general"},
	{"/admin/settings/branding", "/admin/theme/colours"},
	{"/admin/settings/homepage", "/admin/settings/general"},
	{"/admin/settings/contact", "/admin/settings/general"},
	{"/admin/settings/notifications", "/admin/settings/general"},
	{"/admin/settings/api", "/admin/settings/api-keys"},
	{"/admin/settings/api/secret", "/admin/settings/api-keys/secret"},
	{"/admin/settings/api/7/revoke", "/admin/settings/api-keys/7/revoke"},
	{"/admin/settings/plugins", "/admin/plugins"},
	{"/admin/settings/plugins/fetch", "/admin/plugins/fetch"},
	{"/admin/settings/plugins/upload", "/admin/plugins/upload"},
	{"/admin/settings/plugins/hreflang/settings", "/admin/plugins/hreflang/settings"},
	{"/admin/settings/theme", "/admin/theme/themes"},
	{"/admin/settings/theme/design", "/admin/theme/colours"},
	{"/admin/settings/theme/install", "/admin/theme/themes/install"},
	{"/admin/settings/theme/paper/activate", "/admin/theme/themes/paper/activate"},
	// Appearance and Menus left Settings for the Theme area.
	{"/admin/settings/themes", "/admin/theme/themes"},
	{"/admin/settings/themes/fetch", "/admin/theme/themes/fetch"},
	{"/admin/settings/themes/install", "/admin/theme/themes/install"},
	{"/admin/settings/themes/paper/activate", "/admin/theme/themes/paper/activate"},
	{"/admin/settings/themes/paper/settings", "/admin/theme/themes/paper/settings"},
	{"/admin/settings/themes/paper/uninstall", "/admin/theme/themes/paper/uninstall"},
	{"/admin/settings/themes/design", "/admin/theme/colours"},
	{"/admin/settings/themes/tokens", "/admin/theme/design-system"},
	{"/admin/settings/themes/tokens/reset", "/admin/theme/design-system/reset"},
	{"/admin/settings/themes/presets", "/admin/theme/presets"},
	{"/admin/settings/themes/presets/new", "/admin/theme/presets/new"},
	{"/admin/settings/themes/presets/bold/delete", "/admin/theme/presets/bold/delete"},
	{"/admin/settings/themes/parts", "/admin/theme/parts"},
	{"/admin/settings/sections", "/admin/theme/templates"},
	{"/admin/settings/sections/6", "/admin/theme/templates/6"},
	{"/admin/settings/sections/6/delete", "/admin/theme/templates/6/delete"},
	{"/admin/settings/menus", "/admin/theme/menus"},
	{"/admin/settings/menus/new", "/admin/theme/menus/new"},
	{"/admin/settings/menus/2", "/admin/theme/menus/2"},
	{"/admin/settings/menus/2/delete", "/admin/theme/menus/2/delete"},
	{"/admin/settings/appearance", "/admin/theme"},
	{"/admin/settings/menus-forms", "/admin/theme/menus"},
	{"/admin/schemas", "/admin/settings/types"},
	{"/admin/schemas/new", "/admin/settings/types/new"},
	{"/admin/schemas/preview-pattern", "/admin/settings/types/preview-pattern"},
	{"/admin/schemas/3/delete", "/admin/settings/types/3/delete"},
	{"/admin/taxonomies", "/admin/settings/taxonomies"},
	{"/admin/taxonomies/terms/4", "/admin/settings/taxonomies/terms/4"},
	{"/admin/taxonomies/merge", "/admin/settings/taxonomies/merge"},
	{"/admin/menus", "/admin/theme/menus"},
	{"/admin/menus/2/items", "/admin/theme/menus/2/items"},
	{"/admin/forms", "/admin/settings/forms"},
	{"/admin/forms/new", "/admin/settings/forms/new"},
	{"/admin/forms/7/edit", "/admin/settings/forms/7/edit"},
	{"/admin/forms/7/delete", "/admin/settings/forms/7/delete"},
	{"/admin/forms/7/submissions", "/admin/settings/submissions?form=7"},
	{"/admin/forms/7/submissions/bulk", "/admin/settings/submissions/bulk?form=7"},
	{"/admin/forms/7/submissions/export.csv", "/admin/settings/submissions/export.csv?form=7"},
	{"/admin/forms/7/submissions/3", "/admin/settings/submissions/3"},
	{"/admin/forms/7/submissions/3/status", "/admin/settings/submissions/3/status"},
	{"/admin/forms/7/submissions/3/email", "/admin/settings/submissions/3/email"},
	{"/admin/forms/7/submissions/3/delete", "/admin/settings/submissions/3/delete"},
	{"/admin/redirects", "/admin/settings/redirects"},
	{"/admin/redirects/new", "/admin/settings/redirects/new"},
	{"/admin/redirects/bulk", "/admin/settings/redirects/bulk"},
	{"/admin/redirects/5/toggle", "/admin/settings/redirects/5/toggle"},
	{"/admin/redirects/404", "/admin/settings/redirects/404"},
	{"/admin/redirects/404/5/ignore", "/admin/settings/redirects/404/5/ignore"},
	{"/admin/redirects/explain", "/admin/settings/redirects/explain"},
	{"/admin/linking", "/admin/settings/linking"},
	{"/admin/linking/rules/bulk", "/admin/settings/linking/rules/bulk"},
	{"/admin/linking/suggestions/insert", "/admin/settings/linking/suggestions/insert"},
	{"/admin/linking/equity/9", "/admin/settings/linking/equity/9"},
	{"/admin/sections", "/admin/theme/templates"},
	{"/admin/sections/6/delete", "/admin/theme/templates/6/delete"},
	{"/admin/users", "/admin/settings/users"},
	{"/admin/users/invite", "/admin/settings/users/invite"},
	{"/admin/users/invites/2/resend", "/admin/settings/users/invites/2/resend"},
	{"/admin/users/8/delete", "/admin/settings/users/8/delete"},
	{"/admin/import", "/admin/settings/import"},
	{"/admin/import/wordpress", "/admin/settings/import/wordpress"},
	{"/admin/import/wordpress/4/start", "/admin/settings/import/wordpress/4/start"},
	{"/admin/media", "/admin/settings/media"},
}

// legacyPass are paths that must never be redirected: pass rows of the map,
// new paths that share a prefix with an old one, and the media endpoints that
// stay put.
var legacyPass = []string{
	"/admin", "/admin/", "/admin/entities", "/admin/entities/3/edit", "/admin/analytics/referrals",
	"/admin/analytics/crawlers", "/admin/analytics/health", "/admin/analytics/content", "/admin/analytics/data/referrals.json",
	"/admin/settings", "/admin/settings/general", "/admin/theme", "/admin/theme/themes", "/admin/theme/colours", "/admin/theme/design-system", "/admin/theme/blocks", "/admin/theme/menus",
	"/admin/settings/api-keys", "/admin/settings/api-keys/secret", "/admin/settings/redirects",
	"/admin/settings/submissions", "/admin/settings/media", "/admin/settings/import/wordpress",
	"/admin/plugins", "/admin/plugins/hreflang/ui", "/admin/plugins/hreflang/bridge",
	"/admin/preview/theme/paper", "/admin/pick/entities", "/admin/pick/terms",
	"/admin/media/picker", "/admin/media/upload", "/admin/media/3", "/admin/media/3/delete",
	"/admin/profile", "/admin/profile/password", "/admin/login", "/admin/setup/site",
	"/admin/_/admin.css", "/admin/formsx", "/admin/usersettings", "/admin/media2",
}

func TestLegacyRedirectTable(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("old path reached the next handler") })
	h := legacyRedirect(next)
	for _, tc := range legacyMap {
		for _, m := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
			for _, q := range []string{"", "a=1&b=2"} {
				target := tc.old
				if q != "" {
					target += "?" + q
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(m, target, nil))
				want := tc.to
				if q != "" {
					sep := "?"
					if strings.Contains(want, "?") {
						sep = "&"
					}
					want += sep + q
				}
				wantCode := http.StatusMovedPermanently // GET and HEAD; the others keep method and body with 308
				if m != "GET" && m != "HEAD" {
					wantCode = http.StatusPermanentRedirect
				}
				if rec.Code != wantCode {
					t.Errorf("%s %s: %d, want %d", m, target, rec.Code, wantCode)
					continue
				}
				if got := rec.Header().Get("Location"); got != want && !sameQuery(got, want) {
					t.Errorf("%s %s: Location %q, want %q", m, target, got, want)
				}
			}
		}
	}
}

// sameQuery accepts a reordered query (form= is encoded with the rest).
func sameQuery(got, want string) bool {
	gu, err1 := http.NewRequest("GET", got, nil)
	wu, err2 := http.NewRequest("GET", want, nil)
	if err1 != nil || err2 != nil || gu.URL.Path != wu.URL.Path {
		return false
	}
	return gu.URL.Query().Encode() == wu.URL.Query().Encode()
}

func TestLegacyRedirectLeavesRealPathsAlone(t *testing.T) {
	t.Parallel()

	for _, p := range legacyPass {
		if to, ok := legacyTarget(mustURL(t, p)); ok {
			t.Errorf("%s redirects to %s", p, to)
		}
	}
}

// Crafted paths never become a redirect that leaves /admin or puts raw control
// bytes in Location: climbing or control input falls through (404), and an
// encoded slash stays encoded in the target.
func TestLegacyRedirectRejectsCraftedPaths(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"/admin/users/..%2F..%2F..%2F%2Fevil.com",
		"/admin/users/../../evil.com",
		"/admin/forms/x/submissions/..%2F..%2F..%2F..%2F%2Fevil.com%01",
		"/admin/users/x%01",
		"/admin/users/x%0D%0ASet-Cookie:a=b",
		"/admin/users/x%00",
		"/admin/users/x%7F",
		"/admin/users/%5Cevil.com",
		"/admin/users/a%5C..%5Cb",
		"/admin/schemas/%2e%2e/%2e%2e/evil",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if to, ok := legacyTarget(u); ok {
			t.Errorf("%q redirects to %q, want fall through", raw, to)
		}
	}
	// a raw control byte, which url.Parse refuses but a URL value can carry
	if to, ok := legacyTarget(&url.URL{Path: "/admin/users/x\x01"}); ok {
		t.Errorf("control byte redirects to %q", to)
	}
	u := mustURL(t, "/admin/users/%2F%2Fevil.com")
	if to, ok := legacyTarget(u); !ok || to != "/admin/settings/users/%2F%2Fevil.com" {
		t.Errorf("encoded slashes: %q %v", to, ok)
	}
}

func TestLegacyRedirectSegmentBoundaries(t *testing.T) {
	t.Parallel()

	for old, to := range map[string]string{
		"/admin/settings/theme":               "/admin/theme/themes",
		"/admin/settings/themes/tokens/reset": "/admin/theme/design-system/reset",
		"/admin/settings/api/1/reset":         "/admin/settings/api-keys/1/reset",
	} {
		if got, ok := legacyTarget(mustURL(t, old)); !ok || got != to {
			t.Errorf("%s -> %q %v, want %s", old, got, ok, to)
		}
	}
	for _, p := range []string{"/admin/settings/themesx", "/admin/settings/api-keys", "/admin/media/picker", "/admin/settingsx", "/admin/theme/themes"} {
		if _, ok := legacyTarget(mustURL(t, p)); ok {
			t.Errorf("%s was matched by a string prefix", p)
		}
	}
}

func TestLegacyRedirectBeatsSessionAndLosesToRealRoutes(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	// No session and a path with no real route: 301, not /admin/login?next=.
	for _, m := range []string{"GET", "POST"} {
		rec := g.do(m, "/admin/schemas/1/2/3?x=1", "", nil, nil)
		want := http.StatusMovedPermanently
		if m == "POST" {
			want = http.StatusPermanentRedirect
		}
		if rec.Code != want || rec.Header().Get("Location") != "/admin/settings/types/1/2/3?x=1" {
			t.Errorf("%s: %d %q, want %d", m, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	// A real route wins over the table: the picker is not touched, and an
	// unknown path outside the table is still the signed-in 404.
	if rec := g.do("GET", "/admin/media/picker", "owner-token", nil, nil); rec.Code == http.StatusMovedPermanently {
		t.Errorf("media picker redirected to %q", rec.Header().Get("Location"))
	}
	if rec := g.do("GET", "/admin/nothing-here", "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown path: %d", rec.Code)
	}
}

func mustURL(t *testing.T, p string) *url.URL {
	t.Helper()
	u, err := url.Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Content model and Team: the new paths are real, owner-only routes; the old
// ones redirect for every method.
func TestMovedContentModelAndTeamRoutes(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	for _, p := range []string{"/admin/settings/types", "/admin/settings/taxonomies", "/admin/settings/users"} {
		// types and taxonomies need the content service this rig lacks; the
		// owner pages are covered by TestSchemaScreens and TestTaxonomyScreens.
		if rec := g.do("GET", p, "owner-token", nil, nil); strings.HasSuffix(p, "users") && rec.Code != http.StatusOK {
			t.Errorf("owner %s: %d", p, rec.Code)
		}
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != http.StatusForbidden {
			t.Errorf("editor %s: %d, want 403", p, rec.Code)
		}
		if rec := g.do("GET", p, "", nil, nil); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Errorf("anonymous %s: %d, want a sign-in redirect", p, rec.Code)
		}
	}
	for old, to := range map[string]string{
		"/admin/schemas?x=1":       "/admin/settings/types?x=1",
		"/admin/taxonomies?kind=a": "/admin/settings/taxonomies?kind=a",
		"/admin/users/invite":      "/admin/settings/users/invite",
	} {
		for _, m := range []string{"GET", "POST"} {
			rec := g.do(m, old, "owner-token", nil, nil)
			want := http.StatusMovedPermanently
			if m == "POST" {
				want = http.StatusPermanentRedirect
			}
			if rec.Code != want || rec.Header().Get("Location") != to {
				t.Errorf("%s %s: %d %q, want %d %q", m, old, rec.Code, rec.Header().Get("Location"), want, to)
			}
		}
	}
}

// Menus and forms are open to editors; old form URLs carry the form id into
// the query of the new submission list.
func TestMovedMenusAndFormsRoutes(t *testing.T) {
	t.Parallel()

	g, _, _, fm, s := submissionRig(t)
	for _, p := range []string{"/admin/settings/forms", "/admin/settings/submissions", "/admin/theme/menus"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code == http.StatusForbidden || rec.Code == http.StatusMovedPermanently {
			t.Errorf("editor %s: %d", p, rec.Code)
		}
	}
	id := fm.ID.String()
	for old, to := range map[string]string{
		"/admin/forms":                                               "/admin/settings/forms",
		"/admin/forms/" + id + "/submissions":                        "/admin/settings/submissions?form=" + id,
		"/admin/forms/" + id + "/submissions/" + s.ID.String():       "/admin/settings/submissions/" + s.ID.String(),
		"/admin/forms/" + id + "/submissions/export.csv?status=read": "/admin/settings/submissions/export.csv?form=" + id + "&status=read",
		"/admin/menus/" + id:                                         "/admin/theme/menus/" + id,
	} {
		rec := g.do("GET", old, "editor-token", nil, nil)
		if rec.Code != http.StatusMovedPermanently || !sameQuery(rec.Header().Get("Location"), to) {
			t.Errorf("GET %s: %d %q, want 301 %q", old, rec.Code, rec.Header().Get("Location"), to)
		}
	}
}
