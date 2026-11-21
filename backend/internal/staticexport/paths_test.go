package staticexport

import "testing"

func TestFilePath(t *testing.T) {
	cases := []struct {
		in     string
		html   bool
		want   string
		errors bool
	}{
		{"/", true, "index.html", false},
		{"/blog/x", true, "blog/x/index.html", false},
		{"/blog/x/", true, "blog/x/index.html", false},
		{"/feed.xml", false, "feed.xml", false},
		{"/feed.xml", true, "feed.xml", false},
		{"/api/v1/public/knowledge-graph", false, "api/v1/public/knowledge-graph", false},
		{"/caf%C3%A9", true, "café/index.html", false},
		{"/a/../b", true, "", true},
		{"/a%2F..%2Fb", true, "", true},
		{"/a\x00b", true, "", true},
		{"/a%00b", true, "", true},
		{`/a\b`, true, "", true},
		{"/" + ManifestFile, false, "", true},
		{"/%zz", true, "", true},
	}
	for _, c := range cases {
		got, err := FilePath(c.in, c.html)
		if (err != nil) != c.errors || got != c.want {
			t.Errorf("FilePath(%q, %v) = %q, %v; want %q, error=%v", c.in, c.html, got, err, c.want, c.errors)
		}
	}
}

func TestExcluded(t *testing.T) {
	const admin = "/k3x9-admin"
	for p, want := range map[string]bool{
		"/k3x9-admin":                    true,
		"/k3x9-admin/login":              true,
		"/k3x9-administrator":            false,
		"/auth/x":                        true,
		"/api/v1/public/track":           true,
		KnowledgeGraphPath:               false,
		KnowledgeGraphPath + "?schema=x": false,
		"/p/forms/assets/a.js":           false,
		"/p/forms/submit":                true,
		"/p":                             true,
		"/preview/t":                     true,
		"/-/x":                           true,
		"/search":                        true,
		"/.well-known/x":                 true,
		"/blog":                          false,
		"/":                              false,
		"/pages":                         false,
		"/%61uth/x":                      true,
		"/%61pi/v1/x":                    true,
		"/%66orms/x":                     true,
		"/k3x9%2dadmin/login":            true,
		"/%zz":                           true,
		"/bl%6fg":                        false,
	} {
		if got := Excluded(p, admin); got != want {
			t.Errorf("Excluded(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestQueryKind(t *testing.T) {
	for q, want := range map[string]struct {
		skip   bool
		reason string
	}{
		"":         {false, ""},
		"page=2":   {true, "?page=N"},
		"schema=a": {true, "?schema="},
		"utm=x":    {true, "query string"},
	} {
		skip, reason := QueryKind(q)
		if skip != want.skip || reason != want.reason {
			t.Errorf("QueryKind(%q) = %v, %q", q, skip, reason)
		}
	}
}

func TestListingPage(t *testing.T) {
	for q, want := range map[string]int{"page=2": 2, "page=1": 1, "page=10000": 10000} {
		if n, ok := ListingPage(q); !ok || n != want {
			t.Errorf("ListingPage(%q) = %d, %v", q, n, ok)
		}
	}
	for _, q := range []string{"", "page=0", "page=02", "page=+2", "page=2&x=1", "page=2&page=3", "page=", "page=10001", "schema=a", "page=-1", "page=2x"} {
		if n, ok := ListingPage(q); ok {
			t.Errorf("ListingPage(%q) = %d, want not ok", q, n)
		}
	}
}

func TestListingFilePath(t *testing.T) {
	for in, want := range map[string]string{
		"/blog":    "blog/page/2/index.html",
		"/blog/":   "blog/page/2/index.html",
		"/":        "page/2/index.html",
		"/fr/blog": "fr/blog/page/2/index.html",
	} {
		got, err := ListingFilePath(in, 2)
		if err != nil || got != want {
			t.Errorf("ListingFilePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestListingHref(t *testing.T) {
	for _, c := range []struct {
		path string
		n    int
		want string
	}{
		{"/blog", 2, "/blog/page/2/"},
		{"/blog/", 3, "/blog/page/3/"},
		{"/", 2, "/page/2/"},
		{"/blog", 1, "/blog"},
		{"/a%20b", 2, "/a%20b/page/2/"},
	} {
		if got := listingHref(c.path, c.n); got != c.want {
			t.Errorf("listingHref(%q, %d) = %q, want %q", c.path, c.n, got, c.want)
		}
	}
}
