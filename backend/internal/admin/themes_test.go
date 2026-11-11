package admin

import (
	"net/url"
	"strings"
	"testing"
)

// A theme the registry revoked says so on the Themes page.
func TestRevokedThemeShowsRevocation(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	pk.themes = []ThemeInfo{
		{Name: "default", Builtin: true, Active: true},
		{Name: "gone", Version: "1.0.0", Blocked: "key leaked", Revoked: "key leaked"},
		{Name: "gone-bare", Version: "1.0.0", Blocked: "revoked by the registry", Revoked: "revoked by the registry"},
	}
	body := g.do("GET", "/admin/theme/themes", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Disabled: revoked by the registry (key leaked) The theme is kept, not deleted.", "Disabled: revoked by the registry. The theme is kept, not deleted."} {
		if !strings.Contains(body, want) {
			t.Errorf("themes page lacks %q", want)
		}
	}
	if strings.Contains(body, "()") {
		t.Error("empty parentheses")
	}
}

func TestThemesPageDeveloperBanner(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	if body := g.do("GET", "/admin/theme/themes", "owner-token", nil, nil).Body.String(); strings.Contains(body, "Developer packages are allowed") {
		t.Fatal("banner shown while developer packages are off")
	}
	pk.developerOn = true
	body := g.do("GET", "/admin/theme/themes", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`id="developer-banner"`, "Developer packages are allowed.", `action="/admin/plugins/developer/off"`, `name="return" value="theme"`, ">Turn off<"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	a11yOK(t, body)
	rec := g.do("POST", "/admin/plugins/developer/off", "owner-token", url.Values{"return": {"theme"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/theme/themes" || pk.developerOn {
		t.Fatalf("turn off: status %d, location %q, on %v", rec.Code, rec.Header().Get("Location"), pk.developerOn)
	}
}
