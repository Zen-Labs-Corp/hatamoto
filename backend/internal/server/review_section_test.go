package server

import "testing"

func TestSectionOf(t *testing.T) {
	for _, c := range []struct{ path, root, want string }{
		{"/pricing/enterprise", "/", "/pricing"},
		{"/pricing", "/", "/pricing"},
		{"/", "/", ""},
		{"/fr/pricing/x", "/fr", "/pricing"},
		{"/fr", "/fr", ""},
		{"/fresh/x", "/fr", "/fresh"},
		{"/blog/a", "", "/blog"},
	} {
		if got := sectionOf(c.path, c.root); got != c.want {
			t.Errorf("sectionOf(%q, %q) = %q, want %q", c.path, c.root, got, c.want)
		}
	}
}
