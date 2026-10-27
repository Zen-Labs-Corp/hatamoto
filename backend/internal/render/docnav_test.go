package render

import "testing"

func docMenu() []NavItem {
	return []NavItem{
		{Label: "Intro", Href: "/docs/intro"},
		{Label: "Themes", Children: []NavItem{
			{Label: "Overview", Href: "/docs/themes/overview"},
			{Label: "Layouts", Href: "/docs/themes/layouts", Children: []NavItem{
				{Label: "Single", Href: "/docs/themes/layouts/single"},
			}},
		}},
		{Label: "Ops", Href: "/docs/ops"},
	}
}

func label(n *NavItem) string {
	if n == nil {
		return ""
	}
	return n.Label
}

func TestNavPos(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path           string
		menu                 []NavItem
		cur, prev, next, trl string
	}{
		{"nested three levels", "/docs/themes/layouts/single", docMenu(), "Single", "Layouts", "Ops", "Themes>Layouts"},
		{"first item", "/docs/intro", docMenu(), "Intro", "", "Overview", ""},
		{"last item", "/docs/ops", docMenu(), "Ops", "Single", "", ""},
		{"trailing slash and query", "/docs/themes/overview/?q=1#x", docMenu(), "Overview", "Intro", "Layouts", "Themes"},
		{"parent without href skipped", "/docs/themes/overview", docMenu(), "Overview", "Intro", "Layouts", "Themes"},
		{"absolute href in menu", "/docs/a", []NavItem{{Label: "A", Href: "https://example.com/docs/a/"}}, "A", "", "", ""},
		{"group label in trail, skipped as neighbour", "/docs/g/a", []NavItem{
			{Label: "G", Href: "/docs/g", Children: []NavItem{
				{Label: "Guides", Href: "#", Children: []NavItem{{Label: "A", Href: "/docs/g/a"}}},
				{Label: "Reference", Href: "#", Children: []NavItem{{Label: "B", Href: "/docs/g/b"}}},
			}}}, "A", "G", "B", "G>Guides"},
		{"missing path", "/nope", docMenu(), "", "", "", ""},
		{"empty path", "", docMenu(), "", "", "", ""},
		{"empty menu", "/docs/intro", nil, "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := navPos(tc.menu, tc.path)
			trail := ""
			for i, a := range got.Trail {
				if i > 0 {
					trail += ">"
				}
				trail += a.Label
			}
			if label(got.Current) != tc.cur || label(got.Prev) != tc.prev || label(got.Next) != tc.next || trail != tc.trl {
				t.Errorf("navPos(%q) = cur %q prev %q next %q trail %q; want %q %q %q %q",
					tc.path, label(got.Current), label(got.Prev), label(got.Next), trail, tc.cur, tc.prev, tc.next, tc.trl)
			}
		})
	}
}
