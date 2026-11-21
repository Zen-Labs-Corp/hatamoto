package staticexport

import (
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/redirect"
)

func rule(src, dst string, status int) *redirect.Redirect {
	return &redirect.Redirect{SourcePath: src, TargetURL: dst, StatusCode: status}
}

func TestWriteRedirects_Order(t *testing.T) {
	body, warns := WriteRedirects(
		[]Line{{"/z", "/a", 301}, {"/b", "/c", 302}},
		[]*redirect.Redirect{rule("/a/*", "/x/$1", 301), rule("/a/b/*", "/y/$1", 301)},
	)
	want := "/b /c 302\n/z /a 301\n/a/b/:p1 /y/:p1 301\n/a/:p1 /x/:p1 301\n"
	if string(body) != want {
		t.Errorf("body:\n%s\nwant:\n%s", body, want)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
}

func TestWriteRedirects_StatusCodes(t *testing.T) {
	var lines []Line
	for _, s := range []int{301, 302, 307, 308} {
		lines = append(lines, Line{"/s" + string(rune('0'+s%10)), "/t", s})
	}
	body, warns := WriteRedirects(append(lines, Line{"/bad", "/t", 200}), nil)
	for _, s := range []string{" 301\n", " 302\n", " 307\n", " 308\n"} {
		if !strings.Contains(string(body), s) {
			t.Errorf("missing status %q in\n%s", s, body)
		}
	}
	if strings.Contains(string(body), "/bad") || len(warns) != 1 {
		t.Errorf("status 200 must be skipped with a warning: %q %v", body, warns)
	}
}

func TestWriteRedirects_Wildcards(t *testing.T) {
	cases := []struct{ src, dst, from, to string }{
		{"/old/*", "/new/$1", "/old/:p1", "/new/:p1"},
		{"/old/*/x/*", "/n/$2/$1", "/old/:p1/x/:p2", "/n/:p2/:p1"},
		{"/a/**", "/b/$1", "/a/*", "/b/:splat"},
		{"/a/*/c/**", "/b/$1/$2", "/a/:p1/c/*", "/b/:p1/:splat"},
		{"/old/*", "https://example.com/new/$1", "/old/:p1", "https://example.com/new/:p1"},
		{"/old/*", "/fixed", "/old/:p1", "/fixed"},
	}
	for _, c := range cases {
		from, to, ok := translatePattern(c.src, c.dst)
		if !ok || from != c.from || to != c.to {
			t.Errorf("translatePattern(%q, %q) = %q, %q, %v; want %q, %q", c.src, c.dst, from, to, ok, c.from, c.to)
		}
	}
	for _, c := range [][2]string{
		{"/a/**/b", "/x"},    // ** not last
		{"/old/*", "/n/$2"},  // no such capture
		{"/old/*", "/n/$1a"}, // placeholder glued to a word
		{"/*/x", "/y/$1"},    // no literal first segment
		{"/old/*", "//evil.example.com/$1"},
	} {
		if _, _, ok := translatePattern(c[0], c[1]); ok {
			t.Errorf("translatePattern(%q, %q) should fail", c[0], c[1])
		}
	}
}

func TestWriteRedirects_SkipsUntranslatable(t *testing.T) {
	body, warns := WriteRedirects(nil, []*redirect.Redirect{
		rule("/a/**/b", "/x", 301),
		rule("/ok/*", "/n/$1", 301),
	})
	if string(body) != "/ok/:p1 /n/:p1 301\n" {
		t.Errorf("body = %q", body)
	}
	if len(warns) != 1 || !slices.ContainsFunc(warns, func(w string) bool { return strings.Contains(w, "/a/**/b") }) {
		t.Errorf("warnings = %v", warns)
	}
}

func TestStub_MetaRefreshAndCanonical(t *testing.T) {
	s := string(Stub(`https://example.com/new?a=1&b="2"`))
	esc := `https://example.com/new?a=1&amp;b=&#34;2&#34;`
	for _, want := range []string{
		`<meta http-equiv="refresh" content="0; url=` + esc + `">`,
		`<link rel="canonical" href="` + esc + `">`,
		`<meta name="robots" content="noindex">`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stub misses %s:\n%s", want, s)
		}
	}
}
