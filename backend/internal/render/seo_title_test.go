package render

import "testing"

func TestTitleWithSite(t *testing.T) {
	for _, c := range []struct{ title, site, want string }{
		{"About", "Acme", "About | Acme"},
		{"About | Acme", "Acme", "About | Acme"},
		{"About - Acme", "Acme", "About - Acme"},
		{"About – Acme", "Acme", "About – Acme"},
		{"Acme", "Acme", "Acme"},
		{"About", "", "About"},
		{"Welcome to Acme", "Acme", "Welcome to Acme | Acme"},
	} {
		if got := titleWithSite(c.title, c.site); got != c.want {
			t.Errorf("titleWithSite(%q, %q) = %q, want %q", c.title, c.site, got, c.want)
		}
	}
}
