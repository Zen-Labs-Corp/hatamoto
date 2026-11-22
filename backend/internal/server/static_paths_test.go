package server

import (
	"testing"

	"hatamoto.dev/core/internal/config"
)

func TestStaticPathNoRedirect(t *testing.T) {
	t.Parallel()
	s := &Server{cfg: &config.Config{}}
	s.cfg.PublicSite.APIPrefix = "/x-api"
	s.cfg.Admin.Resolved = "/secret-door-7"
	for _, p := range []string{"/", "/about", "/fr/about", "/blog/x"} {
		if s.staticNoRedirect(p) {
			t.Errorf("%q must redirect", p)
		}
	}
	for _, p := range []string{
		"/search", "/fr/search", "/forms/x/submit", "/-/comments/form/x", "/preview/t",
		"/api/v1/public/x", "/media/a.jpg", "/theme/assets/x.css", "/fonts/x.woff2",
		"/p/plug/x", "/f/x", "/feed.xml", "/sitemap-en.xml", "/robots.txt", "/llms.txt", "/llms-full.txt",
		"/admin", "/mcp", "/locale/fr", "/favicon.ico", "/secret-door-7", "/secret-door-7/pages",
		"/x-api", "/x-api/things",
	} {
		if !s.staticNoRedirect(p) {
			t.Errorf("%q must not redirect", p)
		}
	}
}

func TestStaticPathLivePath(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/forms/a/submit", "/search", "/fr/search", "/f/x", "/-/comments/1", "/x-api/y"} {
		if !staticLivePath(p, "/x-api") {
			t.Errorf("%q must be a live path", p)
		}
	}
	for _, p := range []string{"/about", "/media/a.jpg", "/theme/assets/x", "/", "/x-api"} {
		if staticLivePath(p, "") {
			t.Errorf("%q must not be a live path", p)
		}
	}
}
