package render

import (
	"strings"
	"testing"
)

func TestRobotsGolden(t *testing.T) {
	want := "User-agent: *\nAllow: /\nDisallow: /admin\nDisallow: /auth\nDisallow: /api\n" +
		"Disallow: /client\nDisallow: /*?*_rsc=\n" +
		"Sitemap: https://example.com/sitemap.xml\n"
	if got := Robots("https://example.com/", nil); got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRobotsExtraDisallow(t *testing.T) {
	got := Robots("https://x.io", []string{"/private", " "})
	if !strings.Contains(got, "Disallow: /private\n") || strings.Contains(got, "Disallow: \n") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRobotsSitemapXSLEmbedded(t *testing.T) {
	if len(SitemapXSL()) == 0 {
		t.Fatal("sitemap.xsl not embedded")
	}
}

func TestRobotsWithDenyEmptyIsByteIdentical(t *testing.T) {
	if RobotsWithDeny("https://x.io", nil, nil) != Robots("https://x.io", nil) {
		t.Fatal("no tokens must equal Robots")
	}
	got := RobotsWithDeny("https://x.io", nil, []string{"GPTBot", " "})
	if !strings.Contains(got, "Disallow: /*?*_rsc=\nUser-agent: GPTBot\nDisallow: /\n\nSitemap: https://x.io/sitemap.xml\n") {
		t.Fatalf("got:\n%s", got)
	}
}

// A configured admin address is never written to robots.txt: the admin line
// is only for the default address.
func TestRobotsForAdminOnlyNamesTheDefault(t *testing.T) {
	if got := RobotsForAdmin("https://x.test", nil, nil, "/admin"); !strings.Contains(got, "Disallow: /admin\n") {
		t.Errorf("default address lost its line:\n%s", got)
	}
	if got := RobotsForAdmin("https://x.test", nil, nil, "/k7qm-x2rf-9tdc"); strings.Contains(got, "k7qm") || strings.Contains(got, "admin") || !strings.Contains(got, "Disallow: /api\n") {
		t.Errorf("a non-default address must not be named and the rest stays:\n%s", got)
	}
	if RobotsWithDeny("https://x.test", nil, nil) != RobotsForAdmin("https://x.test", nil, nil, "/admin") {
		t.Error("RobotsWithDeny must be the default-address form")
	}
}
