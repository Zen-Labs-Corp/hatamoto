package admin

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var controlName = regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*\bname="([^"]+)"`)

// controlNames lists the sorted, distinct names of the form controls in html,
// leaving out the CSRF token.
func controlNames(html string) []string {
	var out []string
	for _, m := range controlName.FindAllStringSubmatch(html, -1) {
		if m[1] != "_csrf" && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

// dialogOf returns the markup of the <dialog> with the given id.
func dialogOf(t *testing.T, page, id string) string {
	t.Helper()
	i := strings.Index(page, `<dialog class="hm-dialog" id="`+id+`"`)
	if i < 0 {
		t.Fatalf("no dialog %q on the page", id)
	}
	j := strings.Index(page[i:], "</dialog>")
	return page[i : i+j]
}

// pageOf returns the page body of a full document, without the sidebar and
// the settings or plugin navigation around it.
func pageOf(t *testing.T, page string) string {
	t.Helper()
	i, j := strings.Index(page, `<div class="hm-page"`), strings.Index(page, "</main>")
	if i < 0 || j < i {
		t.Fatal("no page region")
	}
	return page[i:j]
}

// The dialog on a list and the /new page after it are the same fields: both
// include one shared partial, and this guards that they stay that way.
func TestCreateDialogAndNewPageHaveTheSameFields(t *testing.T) {
	t.Parallel()

	owner := newOwnerRig(t, true)
	menus, _ := newMenuRig(t)
	reds, _ := newRedirectRig(t)
	content := newContentRig(t)
	pkgs, _, _ := newPackageRig(t)
	people, _ := peopleRig(t)
	cases := []struct {
		name, dialog, list, page string
		get                      func(path string) string
	}{
		{"api keys", "new-key", "/admin/settings/api-keys", "/admin/settings/api-keys/new", func(p string) string { return owner.do("GET", p, "owner-token", nil, nil).Body.String() }},
		{"invite", "invite", "/admin/settings/users", "/admin/settings/users/invite", func(p string) string { return owner.do("GET", p, "owner-token", nil, nil).Body.String() }},
		{"menus", "new-menu", "/admin/theme/menus", "/admin/theme/menus/new", func(p string) string { return menus.do("GET", p, "owner-token", nil, nil).Body.String() }},
		{"people", "new-person", "/admin/people", "/admin/people/new", func(p string) string { return people.do("GET", p, "owner-token", nil, nil).Body.String() }},
		{"redirects", "new-redirect", "/admin/settings/redirects", "/admin/settings/redirects/new", func(p string) string { return reds.do("GET", p, "owner-token", nil, nil).Body.String() }},
		{"terms", "new-term", "/admin/settings/taxonomies?kind=category", "/admin/settings/taxonomies/new?kind=category", func(p string) string { return content.get(t, "owner-token", p, "") }},
		{"media", "upload", "/admin/settings/media", "/admin/settings/media/new", func(p string) string { return content.get(t, "owner-token", p, "") }},
		{"plugins", "upload-pkg", "/admin/plugins", "/admin/plugins/upload", func(p string) string { return pkgs.do("GET", p, "owner-token", nil, nil).Body.String() }},
		{"themes", "upload-pkg", "/admin/theme/themes", "/admin/plugins/upload?for=theme", func(p string) string { return pkgs.do("GET", p, "owner-token", nil, nil).Body.String() }},
	}
	for _, tc := range cases {
		dialog, page := controlNames(dialogOf(t, tc.get(tc.list), tc.dialog)), controlNames(pageOf(t, tc.get(tc.page)))
		if len(dialog) == 0 {
			t.Errorf("%s: the dialog has no fields", tc.name)
		}
		if !slices.Equal(dialog, page) {
			t.Errorf("%s: dialog fields %v differ from the /new page fields %v", tc.name, dialog, page)
		}
	}
}
