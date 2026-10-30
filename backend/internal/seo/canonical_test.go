package seo

import "testing"

func TestValidCanonical(t *testing.T) {
	for c, want := range map[string]bool{
		"/a": true, "https://x.test/a": true, "/a b": false, "//e.x": false,
		"https:///x": false, "javascript:alert(1)": false, `a\b`: false, "not a url": false,
	} {
		if got := ValidCanonical(c); got != want {
			t.Errorf("ValidCanonical(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestCanonicalOK(t *testing.T) {
	for _, tc := range []struct {
		name, posted, stored string
		want                 bool
	}{
		{"empty", "", "/a b", true},
		{"unchanged bad stored", " /a b ", "/a b", true},
		{"changed bad", "/c d", "/a b", false},
		{"bad with no stored", "https:///x", "", false},
		{"valid", "/ok", "/a b", true},
	} {
		if got := CanonicalOK(tc.posted, tc.stored); got != tc.want {
			t.Errorf("%s: CanonicalOK(%q, %q) = %v, want %v", tc.name, tc.posted, tc.stored, got, tc.want)
		}
	}
}
