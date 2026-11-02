package admin

import "testing"

func TestStripTitlePrefix(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, title, desc, want string }{
		{"prefix", "Google Analytics", "Google Analytics: adds the tag", "Adds the tag"},
		{"case-insensitive", "Google Analytics", "google analytics: adds the tag", "Adds the tag"},
		{"no space after colon", "SEO", "SEO:links", "Links"},
		{"no match", "Google Analytics", "Adds the Google tag", "Adds the Google tag"},
		{"title elsewhere", "Tags", "Manage Tags: fast", "Manage Tags: fast"},
		{"no colon", "Tags", "Tags manager", "Tags manager"},
		{"empty title", "", ": adds", ": adds"},
		{"only prefix", "Tags", "Tags:", "Tags:"},
		{"trimmed", "Tags", "  Tags:  adds  ", "Adds"},
		{"multibyte", "Éclair", "éclair: ça marche", "Ça marche"},
	} {
		if got := stripTitlePrefix(tc.title, tc.desc); got != tc.want {
			t.Errorf("%s: stripTitlePrefix(%q, %q) = %q, want %q", tc.name, tc.title, tc.desc, got, tc.want)
		}
	}
}
