package admin

import (
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
