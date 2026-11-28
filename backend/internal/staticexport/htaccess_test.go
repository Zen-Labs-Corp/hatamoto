package staticexport

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/redirect"
)

func patRule(src, dst string, status int) *redirect.Redirect {
	return &redirect.Redirect{SourcePath: src, TargetURL: dst, StatusCode: status}
}

func render(t *testing.T, exact []Line, pats []*redirect.Redirect) (string, []string) {
	t.Helper()
	b, w := RenderHTAccess(exact, pats)
	return string(b), w
}

func TestRenderHTAccess_ExactLinesUseRedirectMatch(t *testing.T) {
	got, w := render(t, []Line{{From: "/z", To: "/zz", Status: 302}, {From: "/old", To: "/new", Status: 301}}, nil)
	want := "# BEGIN Hatamoto\n" +
		"# Written by the static export. Lines between these markers are replaced on every export.\n" +
		"<IfModule mod_alias.c>\n" +
		"RedirectMatch 301 ^/old/?$ /new\n" +
		"RedirectMatch 302 ^/z/?$ /zz\n" +
		"</IfModule>\n" +
		"# END Hatamoto\n"
	if got != want || len(w) != 0 {
		t.Errorf("block:\n%s\nwant:\n%s\nwarnings %v", got, want, w)
	}
}

func TestRenderHTAccess_ExactDoesNotPrefixMatch(t *testing.T) {
	got, _ := render(t, []Line{{From: "/blog", To: "/news", Status: 301}}, nil)
	m := regexp.MustCompile(`RedirectMatch 301 (\S+) /news`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no rule in %q", got)
	}
	re := regexp.MustCompile(m[1])
	for p, want := range map[string]bool{"/blog": true, "/blog/": true, "/blog/post-1": false, "/blogx": false} {
		if re.MatchString(p) != want {
			t.Errorf("%s matches %s = %v, want %v", m[1], p, !want, want)
		}
	}
}

func TestRenderHTAccess_ExactQuotesRegexpCharacters(t *testing.T) {
	got, _ := render(t, []Line{{From: "/a.b+c/", To: "/x", Status: 301}}, nil)
	if !strings.Contains(got, `RedirectMatch 301 ^/a\.b\+c/?$ /x`) {
		t.Errorf("block = %s", got)
	}
}

func TestRenderHTAccess_RootLine(t *testing.T) {
	got, _ := render(t, []Line{{From: "/", To: "/home", Status: 301}}, []*redirect.Redirect{patRule("/p/*", "/q/$1", 301)})
	if !strings.Contains(got, "RedirectMatch 301 ^/?$ /home") {
		t.Errorf("block = %s", got)
	}
}

func TestRenderHTAccess_PatternsUseRewriteRule(t *testing.T) {
	got, _ := render(t, nil, []*redirect.Redirect{patRule("/a/*", "/b/$1", 301), patRule("/docs/**", "/guide/$1", 308)})
	for _, want := range []string{
		"<IfModule mod_rewrite.c>\nRewriteEngine On\n",
		"RewriteRule ^a/([^/]+)/?$ /b/$1 [R=301,L]\n",
		"RewriteRule ^docs(?:/(.*))?/?$ /guide/$1 [R=308,L]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("block misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "mod_alias") {
		t.Errorf("no exact line, but mod_alias is in the block:\n%s", got)
	}
	re := regexp.MustCompile(`^docs(?:/(.*))?/?$`)
	for _, p := range []string{"docs", "docs/", "docs/a/b"} {
		if !re.MatchString(p) {
			t.Errorf("docs pattern does not match %q", p)
		}
	}
	if re.MatchString("docsx") || re.MatchString("other/docs") {
		t.Error("docs pattern matches too much")
	}
}

func TestRenderHTAccess_NoRewriteEngineWithoutPatterns(t *testing.T) {
	got, _ := render(t, []Line{{From: "/a", To: "/b", Status: 301}}, nil)
	if strings.Contains(got, "RewriteEngine") || strings.Contains(got, "mod_rewrite") {
		t.Errorf("block = %s", got)
	}
}

func TestRenderHTAccess_ShadowedExactRepeated(t *testing.T) {
	got, _ := render(t, []Line{{From: "/a/one", To: "/exact", Status: 301}},
		[]*redirect.Redirect{patRule("/a/*", "/b/$1", 301)})
	i := strings.Index(got, "RewriteRule ^a/one/?$ /exact [R=301,L]")
	j := strings.Index(got, "RewriteRule ^a/([^/]+)/?$")
	if i < 0 || j < 0 || i > j {
		t.Errorf("the shadowed exact rule must come first:\n%s", got)
	}
	if !strings.Contains(got, "RedirectMatch 301 ^/a/one/?$ /exact") {
		t.Errorf("exact line missing from mod_alias:\n%s", got)
	}
}

func TestRenderHTAccess_EscapesTarget(t *testing.T) {
	got, _ := render(t, []Line{{From: "/a", To: `/p$1\x`, Status: 301}}, nil)
	if !strings.Contains(got, `RedirectMatch 301 ^/a/?$ /p\$1\\x`) {
		t.Errorf("block = %s", got)
	}
}

func TestRenderHTAccess_SkipsWithWarnings(t *testing.T) {
	got, w := render(t, []Line{
		{From: "/has space", To: "/x", Status: 301},
		{From: "/q?x=1", To: "/x", Status: 301},
		{From: "/a", To: "/x", Status: 200},
	}, []*redirect.Redirect{
		patRule("/p/*", "/q/$2", 301),    // $2 has no wildcard
		patRule("/p/*", "/q%20/$1", 301), // mod_rewrite would re-escape
		patRule("/p/*", "/q/$1", 200),
		patRule("/**", "/x", 301), // catch-all
	})
	if got != "" {
		t.Errorf("block = %q, want none", got)
	}
	if len(w) != 7 {
		t.Errorf("warnings = %d: %v", len(w), w)
	}
	for _, m := range w {
		if !strings.Contains(m, ".htaccess") {
			t.Errorf("warning %q does not name .htaccess", m)
		}
	}
}

func TestRenderHTAccess_StatusCodes(t *testing.T) {
	for _, st := range []int{301, 302, 307, 308} {
		got, _ := render(t, []Line{{From: "/a", To: "/b", Status: st}}, []*redirect.Redirect{patRule("/p/*", "/q/$1", st)})
		if !strings.Contains(got, "RedirectMatch "+strconv.Itoa(st)+" ^/a/?$ /b") || !strings.Contains(got, "[R="+strconv.Itoa(st)+",L]") {
			t.Errorf("status %d:\n%s", st, got)
		}
	}
}

const sampleBlock = "# BEGIN Hatamoto\nA\n# END Hatamoto\n"

func TestMergeHTAccess_Appends(t *testing.T) {
	got, err := MergeHTAccess([]byte("Options -Indexes"), []byte(sampleBlock))
	if err != nil || string(got) != "Options -Indexes\n"+sampleBlock {
		t.Errorf("got %q, %v", got, err)
	}
	got, _ = MergeHTAccess(nil, []byte(sampleBlock))
	if string(got) != sampleBlock {
		t.Errorf("empty remote: %q", got)
	}
	got, _ = MergeHTAccess([]byte("x\n"), nil)
	if string(got) != "x\n" {
		t.Errorf("empty block, no block: %q", got)
	}
}

func TestMergeHTAccess_Replaces(t *testing.T) {
	remote := "top\n# BEGIN Hatamoto\nold\n# END Hatamoto\nbottom\n"
	got, err := MergeHTAccess([]byte(remote), []byte(sampleBlock))
	if err != nil || string(got) != "top\n"+sampleBlock+"bottom\n" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestMergeHTAccess_RemovesOnEmpty(t *testing.T) {
	remote := "top\n# BEGIN Hatamoto\nold\n# END Hatamoto\nbottom\n"
	got, err := MergeHTAccess([]byte(remote), nil)
	if err != nil || string(got) != "top\nbottom\n" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestMergeHTAccess_KeepsForeignBytes(t *testing.T) {
	remote := "\xef\xbb\xbfRewriteEngine On\r\n\r\n\t# mine  \r\n# BEGIN Hatamoto\r\nold\r\n# END Hatamoto\r\nlast-no-newline"
	got, err := MergeHTAccess([]byte(remote), []byte(sampleBlock))
	want := "\xef\xbb\xbfRewriteEngine On\r\n\r\n\t# mine  \r\n" + sampleBlock + "last-no-newline"
	if err != nil || string(got) != want {
		t.Errorf("got %q, %v", got, err)
	}
	// A block at the very end, without a final newline.
	got, err = MergeHTAccess([]byte("a\n# BEGIN Hatamoto\nold\n# END Hatamoto"), []byte(sampleBlock))
	if err != nil || string(got) != "a\n"+sampleBlock {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestMergeHTAccess_RefusesUnbalanced(t *testing.T) {
	for name, remote := range map[string]string{
		"begin only": "# BEGIN Hatamoto\nold\n",
		"end only":   "old\n# END Hatamoto\n",
		"reversed":   "# END Hatamoto\n# BEGIN Hatamoto\n",
		"two blocks": sampleBlock + sampleBlock,
	} {
		if got, err := MergeHTAccess([]byte(remote), []byte(sampleBlock)); err == nil {
			t.Errorf("%s: merged to %q", name, got)
		}
	}
}

func TestRenderHTAccess_PercentEncodedSourceMatchesDecodedPath(t *testing.T) {
	got, w := render(t, []Line{{From: "/caf%C3%A9", To: "/new", Status: 301}}, nil)
	if len(w) != 0 || !strings.Contains(got, "RedirectMatch 301 ^/café/?$ /new\n") {
		t.Errorf("block = %s warnings %v", got, w)
	}
	if strings.Contains(got, "%C3") {
		t.Errorf("the source is still percent-encoded:\n%s", got)
	}
}

func TestRenderHTAccess_PercentEncodedTargetUsesRewriteRuleWithNE(t *testing.T) {
	got, _ := render(t, []Line{{From: "/old", To: "/caf%C3%A9/%25", Status: 301}}, nil)
	if strings.Contains(got, "RedirectMatch") {
		t.Errorf("mod_alias would double-encode the target:\n%s", got)
	}
	want := `RewriteRule ^old/?$ /caf\%C3\%A9/\%25 [R=301,NE,L]` + "\n"
	if !strings.Contains(got, "RewriteEngine On\n") || !strings.Contains(got, want) {
		t.Errorf("block misses %q:\n%s", want, got)
	}
}

func TestRenderHTAccess_UndecodableOrSpacedSourceSkipped(t *testing.T) {
	for _, from := range []string{"/bad%zz", "/a%20b", "/a%0Ab"} {
		got, w := render(t, []Line{{From: from, To: "/x", Status: 301}}, nil)
		if got != "" || len(w) != 1 {
			t.Errorf("%s: block %q warnings %v", from, got, w)
		}
	}
}

func TestRenderHTAccess_WildcardSourceWithWhitespaceSkipped(t *testing.T) {
	got, w := render(t, nil, []*redirect.Redirect{patRule("/a b/*", "/c/$1", 301)})
	if got != "" || len(w) != 1 {
		t.Errorf("block %q warnings %v", got, w)
	}
}
