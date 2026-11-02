package admin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// flashText is the message of the flash cookie a response set.
func flashText(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name != flashCookie || c.MaxAge < 0 {
			continue
		}
		raw, _, _ := strings.Cut(c.Value, ".")
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			t.Fatal(err)
		}
		var f Flash
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatal(err)
		}
		return f.Message
	}
	t.Fatal("no flash cookie set")
	return ""
}

func TestInviteResendFlashSaysSentAgain(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	first := g.do("POST", "/admin/settings/users/invite", "owner-token", inviteForm("new@example.com"), nil)
	if got := flashText(t, first); got != "Invite created" {
		t.Errorf("create flash %q", got)
	}
	var id string
	for _, inv := range g.tokens.invites {
		id = inv.ID.String()
	}
	rec := g.do("POST", "/admin/settings/users/invites/"+id+"/resend", "owner-token", url.Values{}, nil)
	if got := flashText(t, rec); got != "Invite sent again" {
		t.Errorf("resend flash %q", got)
	}
}

func TestTermCreateFlashNamesTheTerm(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rec := c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"category"}, "name": {"Zebra"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", rec.Code)
	}
	if got := flashText(t, rec); got != `Added “Zebra”.` {
		t.Errorf("flash %q", got)
	}
}

// The 422 re-render of a create form shows the same trail as its /new page.
func TestCreateRerenderKeepsTheNewPageTrail(t *testing.T) {
	t.Parallel()

	owner := newOwnerRig(t, true)
	menus, _ := newMenuRig(t)
	cases := []struct {
		name string
		do   func(method, path, token string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder
		page string
		post string
		form url.Values
	}{
		{"api keys", owner.do, "/admin/settings/api-keys/new", "/admin/settings/api-keys", url.Values{"scopes": {"read:entities"}}},
		{"menus", menus.do, "/admin/theme/menus/new", "/admin/theme/menus", url.Values{"name": {" "}}},
	}
	for _, tc := range cases {
		want := strings.Join(trailLabels(t, tc.do("GET", tc.page, "owner-token", nil, nil).Body.String()), " > ")
		rec := tc.do("POST", tc.post, "owner-token", tc.form, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d", tc.name, rec.Code)
		}
		if got := strings.Join(trailLabels(t, rec.Body.String()), " > "); got != want || !strings.HasPrefix(got, "Home > Settings > ") && !strings.HasPrefix(got, "Home > Theme > ") {
			t.Errorf("%s: 422 trail %q, /new trail %q", tc.name, got, want)
		}
	}
}

// The no-JS twin of the Upload theme dialog is about themes, not packages.
func TestThemeUploadFallbackIsAThemePage(t *testing.T) {
	t.Parallel()

	pkgs, _, _ := newPackageRig(t)
	list := pkgs.do("GET", "/admin/theme/themes", "owner-token", nil, nil).Body.String()
	has(t, list, `href="/admin/plugins/upload?for=theme"`)
	body := pkgs.do("GET", "/admin/plugins/upload?for=theme", "owner-token", nil, nil).Body.String()
	has(t, body, "<h1", "Upload theme", `href="/admin/theme/themes"`)
	lacks(t, body, "Upload package")
	if got := strings.Join(trailLabels(t, body), " > "); got != "Home > Theme > Themes > Upload theme" {
		t.Errorf("trail %q", got)
	}
	plain := pkgs.do("GET", "/admin/plugins/upload", "owner-token", nil, nil).Body.String()
	has(t, plain, "Upload package", `href="/admin/plugins"`)
}
