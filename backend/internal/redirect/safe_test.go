package redirect

import "testing"

func TestSafeLocation(t *testing.T) {
	for target, abs := range map[string]bool{
		"/":                     false,
		"/x":                    false,
		"/blog/a%20b?x=1#top":   false,
		"/caf%C3%A9":            false,
		"https://example.com/a": true,
		"http://example.com":    true,
	} {
		if !SafeLocation(target, abs) {
			t.Errorf("%q (absoluteOK=%v) rejected", target, abs)
		}
	}
	for _, target := range []string{
		"", "x", "//evil.com", "/\\evil.com", "/%5Cevil.com", "/%5cevil.com", "/\t/evil.com", "/%09/evil.com",
		"/%2F%2Fevil.com", "/%2Fevil.com", "/\nx", "/%0d%0aSet-Cookie:x", "/x\r\nSet-Cookie: a=b", "/a\\b",
		"/\u2044evil.com", "/\uFF0Fevil.com", "/\uFF3Cevil.com", "/%EF%BC%8Fevil.com", "/%zz",
		"https://evil.com", "javascript:alert(1)", "https://x.com/\\y",
	} {
		if SafeLocation(target, false) {
			t.Errorf("%q accepted", target)
		}
	}
	if SafeLocation("https://x.com/a\r\nb", true) {
		t.Error("absolute target with CRLF accepted")
	}
}

// hostilePaths are request paths (already URL-decoded, as r.URL.Path) that try
// to turn a prefix-strip rule into an open redirect.
var hostilePaths = []string{
	"/xx/\\evil.com",          // /xx/%5Cevil.com
	"/xx/\t/evil.com",         // /xx/%09/evil.com
	"/xx/\u2044evil.com",      // unicode slash
	"/xx/\uFF0Fevil.com",      // fullwidth slash
	"/xx/\uFF3Cevil.com",      // fullwidth backslash
	"/xx/\r\nSet-Cookie: a=b", // CRLF
	"/xx//evil.com",           // /xx/%2F%2Fevil.com collapses to /evil.com
	"/xx///evil.com",
	"/xx/%2F%2Fevil.com", // literal percent signs in the path
}

func TestFindPatternNeverProducesExternalLocation(t *testing.T) {
	rules := []patternRule{
		newPatternRule(&Redirect{SourcePath: "/xx/**", TargetURL: "/$1"}),
		newPatternRule(&Redirect{SourcePath: "/old/*", TargetURL: "/$1"}),
		newPatternRule(&Redirect{SourcePath: "/go/*", TargetURL: "/$1/x"}),
	}
	for _, path := range hostilePaths {
		for _, prefix := range []string{"/xx", "/old", "/go"} {
			p := prefix + path[len("/xx"):]
			if rd := FindPattern(rules, p); rd != nil && !SafeLocation(rd.TargetURL, false) {
				t.Errorf("%q -> unsafe %q", p, rd.TargetURL)
			}
		}
	}
	// The reviewer's two payloads are dropped outright.
	for _, p := range []string{"/xx/\\evil.com", "/xx/\t/evil.com"} {
		if rd := FindPattern(rules, p); rd != nil {
			t.Errorf("%q matched: %+v", p, rd)
		}
	}
	// Benign captures survive, escaped and with their separators.
	for path, want := range map[string]string{
		"/xx/a b/c":     "/a%20b/c",
		"/xx/café":      "/caf%C3%A9",
		"/xx/q?x#y":     "/q%3Fx%23y",
		"/xx//evil.com": "/evil.com",
	} {
		if rd := FindPattern(rules, path); rd == nil || rd.TargetURL != want {
			t.Errorf("%q: got %+v want %s", path, rd, want)
		}
	}
}

// An owner rule may send visitors to an external URL, and a capture can still
// not change the host.
func TestFindPatternAbsoluteOwnerTarget(t *testing.T) {
	rules := []patternRule{newPatternRule(&Redirect{SourcePath: "/docs/**", TargetURL: "https://docs.example.com/$1"})}
	if rd := FindPattern(rules, "/docs/a/b"); rd == nil || rd.TargetURL != "https://docs.example.com/a/b" {
		t.Errorf("got %+v", rd)
	}
	if rd := FindPattern(rules, "/docs/\\evil.com"); rd == nil || rd.TargetURL != "https://docs.example.com/%5Cevil.com" {
		t.Errorf("backslash capture: %+v", rd)
	}
}
