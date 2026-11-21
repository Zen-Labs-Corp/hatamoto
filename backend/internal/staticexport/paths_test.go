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
