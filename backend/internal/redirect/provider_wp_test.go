package redirect

import (
	"context"
	"testing"
)

func TestWPPatterns(t *testing.T) {
	cases := map[string]string{
		"/page/2":                     "/blog",
		"/2020/05/hello-world":        "/blog/hello-world",
		"/2020/05/17/hello-world":     "/blog/hello-world",
		"/feed":                       "/blog",
		"/comments/feed":              "/blog",
		"/wp-json/wp/v2/posts":        "/blog",
		"/wp-admin":                   "/blog",
		"/wp-content/uploads/a/b.jpg": "/blog",
		"/wp-login.php":               "/blog",
	}
	p := NewWPPatternsProvider(nil)
	for path, want := range cases {
		m, _ := p.Resolve(context.Background(), path)
		if m == nil || m.Target != want || m.Status != 301 {
			t.Errorf("%s: got %+v, want %s", path, m, want)
		}
	}
}

// Taxonomy archives and /blog/page/N are native routes, so the provider
// leaves them to the router.
func TestWPPatternsLeaveNativeRoutes(t *testing.T) {
	p := NewWPPatternsProvider(nil)
	for _, path := range []string{"/author/jane", "/category/news", "/tag/go", "/blog/page/3"} {
		if m, _ := p.Resolve(context.Background(), path); m != nil {
			t.Errorf("%s: native route, got %+v", path, m)
		}
	}
}

// Targets follow the site's post archive and post URL.
func TestWPPatternsUseConfiguredTargets(t *testing.T) {
	p := NewWPPatternsProvider(nil).WithTargets(func(context.Context) PostTargets {
		return PostTargets{Archive: "/post", Post: func(s string) string { return "/post/" + s }}
	})
	for path, want := range map[string]string{"/2020/05/old-post": "/post/old-post", "/feed": "/post"} {
		if m, _ := p.Resolve(context.Background(), path); m == nil || m.Target != want {
			t.Errorf("%s: got %+v, want %s", path, m, want)
		}
	}
}

func TestWPPatternsDeclineSiteSpecificPaths(t *testing.T) {
	p := NewWPPatternsProvider(nil)
	for _, path := range []string{"/", "/about-us", "/seo/audit", "/industries/dental", "/technologies", "/2020", "/page/x"} {
		if m, _ := p.Resolve(context.Background(), path); m != nil {
			t.Errorf("%s: expected decline (site-specific rules live in the DB), got %+v", path, m)
		}
	}
}

func TestWPPatternsGatedBySetting(t *testing.T) {
	off := NewWPPatternsProvider(func(context.Context) bool { return false })
	if m, _ := off.Resolve(context.Background(), "/feed"); m != nil {
		t.Errorf("disabled provider matched: %+v", m)
	}
	on := NewWPPatternsProvider(func(context.Context) bool { return true })
	if m, _ := on.Resolve(context.Background(), "/feed"); m == nil {
		t.Error("enabled provider declined /feed")
	}
}
