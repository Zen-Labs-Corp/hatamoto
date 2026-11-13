package adminpath

import "testing"

func TestRedirectPathRefusal(t *testing.T) {
	for _, p := range []string{"/", "/go", "/go/", "/start/x", "/a/key.txt"} {
		if r := RedirectPathRefusal(p); r != "" {
			t.Errorf("%q refused: %s", p, r)
		}
	}
	for _, p := range []string{
		"/admin", "/admin/x", "/api", "/p/x", "/media/a", "/.well-known/x", "/health", "/robots.txt",
		"/sitemap-a.xml", "/key.txt", "/feed", "/plugin-ui", "/auth", "/-/edit", "/favicon.ico",
		"/icon-192.png", "/forms/x", "/mcp", "/uploads/a", "/sitemap.xsl",
		"/locale", "/locale/x",
	} {
		if RedirectPathRefusal(p) == "" {
			t.Errorf("%q allowed", p)
		}
	}
}
