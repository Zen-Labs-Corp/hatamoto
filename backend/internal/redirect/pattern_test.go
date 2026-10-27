package redirect

import "testing"

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		source, path string
		ok           bool
		caps         []string
	}{
		{"/blog/seo/*", "/blog/seo/x", true, []string{"x"}},
		{"/blog/seo/*", "/blog/seo", false, nil},
		{"/blog/seo/*", "/blog/seo/x/y", false, nil},
		{"/blog/seo/*", "/news/seo/x", false, nil},
		{"/old/technologies/**", "/old/technologies", true, []string{""}},
		{"/old/technologies/**", "/old/technologies/a/b", true, []string{"a/b"}},
		{"/industries/*/**", "/industries/dental", true, []string{"dental", ""}},
		{"/industries/*/**", "/industries/dental/x/y", true, []string{"dental", "x/y"}},
		{"/industries/*/**", "/industries", false, nil},
		{"/a/*", "/A/x", false, nil},
	}
	for _, c := range cases {
		caps, ok := MatchPattern(c.source, c.path)
		if ok != c.ok || (ok && len(caps) != len(c.caps)) {
			t.Errorf("%s vs %s: got %v %v want %v %v", c.source, c.path, caps, ok, c.caps, c.ok)
			continue
		}
		for i := range caps {
			if caps[i] != c.caps[i] {
				t.Errorf("%s vs %s: cap %d = %q want %q", c.source, c.path, i, caps[i], c.caps[i])
			}
		}
	}
}

// A retired path prefix is dropped with `/<prefix>/**` -> `/$1`; the bare
// prefix lands on the root.
func TestPrefixStripRule(t *testing.T) {
	if err := ValidatePatternRule("/old/**", "/$1"); err != nil {
		t.Fatalf("rule rejected: %v", err)
	}
	rule := []patternRule{newPatternRule(&Redirect{SourcePath: "/old/**", TargetURL: "/$1"})}
	for path, want := range map[string]string{
		"/old/platform":   "/platform",
		"/old/post/a/b":   "/post/a/b",
		"/old":            "/",
		"/old/":           "/",
		"/older/platform": "",
		"/platform":       "",
	} {
		rd := FindPattern(rule, path)
		switch {
		case want == "" && rd != nil:
			t.Errorf("%s: unexpected match %+v", path, rd)
		case want != "" && (rd == nil || rd.TargetURL != want):
			t.Errorf("%s: got %+v want %s", path, rd, want)
		}
	}
}

func TestFindPatternSpecificity(t *testing.T) {
	rules := []*Redirect{
		{SourcePath: "/a/**", TargetURL: "/broad"},
		{SourcePath: "/a/b/*", TargetURL: "/narrow/$1"},
	}
	sortPatterns(rules)
	compiled := make([]patternRule, len(rules))
	for i, r := range rules {
		compiled[i] = newPatternRule(r)
	}
	if rd := FindPattern(compiled, "/a/b/c/"); rd == nil || rd.TargetURL != "/narrow/c" {
		t.Errorf("got %+v", rd)
	}
	if rd := FindPattern(compiled, "/a/z"); rd == nil || rd.TargetURL != "/broad" {
		t.Errorf("got %+v", rd)
	}
}

func TestValidatePatternSource(t *testing.T) {
	for src, ok := range map[string]bool{
		"/plain": true, "/a/*": true, "/a/**": true, "/a/*/b": true,
		"/a/**/b": false, "/a/x*": false, "a/*": false,
	} {
		if (ValidatePatternSource(src) == nil) != ok {
			t.Errorf("%s: want ok=%v", src, ok)
		}
	}
}
