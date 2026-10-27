package agents

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPublicPath(t *testing.T) {
	for _, c := range []struct {
		in, out string
		ok      bool
	}{
		{"/api/v1/public/sitemap.xml", "/sitemap.xml", true},
		{"/api/v1/public/sitemap-au.xml", "/sitemap-au.xml", true},
		{"/api/v1/public/llms.txt", "/llms.txt", true},
		{"/sitemap.xml", "/sitemap.xml", true},
		{"/api/v1/public/resolve", "", false},
		{"/api/v1/public/some/x", "", false},
		{"/api/v1/public/sitemap-au.txt", "", false},
		{"/api/v1/public/robots.txt", "", false},
		{"", "", false},
	} {
		out, ok := PublicPath(c.in)
		if out != c.out || ok != c.ok {
			t.Errorf("PublicPath(%q) = %q,%v want %q,%v", c.in, out, ok, c.out, c.ok)
		}
	}
}

func TestCleanPathSanitises(t *testing.T) {
	if got := cleanPath("/a%00b\x00c"); got != "/a%00bc" {
		t.Errorf("NUL not stripped: %q", got)
	}
	if got := cleanPath("/a\xffb"); got != "/ab" {
		t.Errorf("invalid utf8 kept: %q", got)
	}
	// 3-byte rune straddling the 512 limit must not be split.
	p := "/" + strings.Repeat("a", 510) + "€"
	got := cleanPath(p)
	if !utf8.ValidString(got) || len(got) > maxPathLen || got != p[:511] {
		t.Errorf("bad truncation: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}
