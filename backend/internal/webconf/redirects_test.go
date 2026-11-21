package webconf

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestParseRedirects(t *testing.T) {
	t.Parallel()
	body := "# comment\n/a /b 301\n\n/c /d 302\n/e/:p1 /f/:p1 301\n/g/* /h/:splat 308\nbroken line\n/x /y notanumber\n"
	rules, exact, wildcard := ParseRedirects([]byte(body))
	if exact != 2 || wildcard != 2 || len(rules) != 4 {
		t.Fatalf("exact %d wildcard %d rules %v", exact, wildcard, rules)
	}
	if want := (Redirect{From: "/g/*", To: "/h/:splat", Status: 308}); rules[3] != want {
		t.Errorf("rule 3 = %+v", rules[3])
	}
	if r, e, w := ParseRedirects(nil); len(r) != 0 || e != 0 || w != 0 {
		t.Error("empty body must give nothing")
	}
}

func TestParseRedirectsBoundsBody(t *testing.T) {
	t.Parallel()
	line := "/a /b 301\n"
	rules, _, _ := ParseRedirects([]byte(strings.Repeat(line, maxRedirectsBody/len(line)*2)))
	if len(rules) > maxRedirectsBody/len(line)+1 {
		t.Errorf("read %d rules past the body limit", len(rules))
	}
}

func TestRedirectTranslation(t *testing.T) {
	t.Parallel()
	in := []Redirect{
		{"/old", "/new", 301},
		{"/blog/:p1/x/:p2", "/b/:p2/:p1", 302},
		{"/docs/*", "/help/:splat", 308},
	}
	rules, skipped := planRedirects(in)
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}
	wantCaddy := []string{
		"redir /old /new 301",
		"@r1 path_regexp r1 ^/blog/([^/[:cntrl:]]+)/x/([^/[:cntrl:]]+)$",
		"redir @r1 /b/{re.r1.2}/{re.r1.1} 302",
		"@r2 path_regexp r2 ^/docs/([^[:cntrl:]]*)$",
		"redir @r2 /help/{re.r2.1} 308",
	}
	if got := caddyRedirects(rules); !reflect.DeepEqual(got, wantCaddy) {
		t.Errorf("caddy:\n%q\nwant\n%q", got, wantCaddy)
	}
	wantNginx := []string{
		"location = /old {", "\treturn 301 /new;", "}",
		"location ~ ^/blog/([^/[:cntrl:]]+)/x/([^/[:cntrl:]]+)$ {", "\treturn 302 /b/$2/$1;", "}",
		"location ~ ^/docs/([^[:cntrl:]]*)$ {", "\treturn 308 /help/$1;", "}",
	}
	if got := nginxRedirects(rules); !reflect.DeepEqual(got, wantNginx) {
		t.Errorf("nginx:\n%q\nwant\n%q", got, wantNginx)
	}
}

func TestRedirectSkipped(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]Redirect{
		"status":           {"/a", "/b", 410},
		"no leading slash": {"a", "/b", 301},
		"empty target":     {"/a", "", 301},
		"brace":            {"/a{b", "/c", 301},
		"semicolon":        {"/a", "/b;c", 301},
		"variable":         {"/a", "/b$host", 301},
		"hash":             {"/a", "/b#c", 301},
		"quote":            {"/a", `/b"c`, 301},
		"stray star":       {"/a*b", "/c", 301},
		"glued star":       {"/a/x*", "/c", 301},
		"out of order":     {"/a/:p2", "/c", 301},
		"star not last":    {"/a/*/b", "/c/:splat", 301},
		"unknown capture":  {"/a/:p1", "/c/:p2", 301},
		"splat no star":    {"/a/:p1", "/c/:splat", 301},
		"glued capture":    {"/a/:p1", "/c/:p1x", 301},
		"ten captures":     {"/:p1/:p2/:p3/:p4/:p5/:p6/:p7/:p8/:p9/:p10", "/c", 301},
	} {
		rules, skipped := planRedirects([]Redirect{r})
		if len(skipped) != 1 || len(rules) != 1 || rules[0].skip == "" {
			t.Errorf("%s: not skipped: %+v %v", name, rules, skipped)
		}
	}
	rules, skipped := planRedirects([]Redirect{{"/a", "/b", 410}})
	if got := caddyRedirects(rules); len(got) != 1 || got[0] != "# skipped: /a /b 410" || skipped[0] != "/a /b 410" {
		t.Errorf("caddy comment %q skipped %q", got, skipped)
	}
	if got := nginxRedirects(rules); len(got) != 1 || got[0] != "# skipped: /a /b 410" {
		t.Errorf("nginx comment %q", got)
	}
	// A control character can never end the comment line.
	_, sk := planRedirects([]Redirect{{"/a\x1b", "/b\x00", 999}})
	if len(sk) != 1 || strings.ContainsAny(sk[0], "\x1b\x00\n") {
		t.Errorf("skipped text %q", sk)
	}
}

func TestRedirectLiteralsAreEscaped(t *testing.T) {
	t.Parallel()
	rules, _ := planRedirects([]Redirect{{"/a.b/:p1", "/c", 301}})
	if got := caddyRedirects(rules); !strings.Contains(got[0], `^/a\.b/([^/[:cntrl:]]+)$`) {
		t.Errorf("literal dot not escaped: %q", got)
	}
}

func TestGenerateReportsSkipped(t *testing.T) {
	t.Parallel()
	out, err := Generate(sameHostInput())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Skipped, []string{"/gone /elsewhere 410"}) {
		t.Errorf("Skipped = %q", out.Skipped)
	}
	for name, cfg := range map[string]string{"caddy": out.Caddy, "nginx": out.Nginx} {
		if !strings.Contains(cfg, "# skipped: /gone /elsewhere 410") {
			t.Errorf("%s config lacks the skipped comment", name)
		}
	}
}

func TestRedirectCapturesRejectControlChars(t *testing.T) {
	t.Parallel()
	rules, skipped := planRedirects([]Redirect{
		{"/blog/:p1/amp", "/articles/:p1", 301},
		{"/docs/*", "/help/:splat", 308},
	})
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}
	cases := []struct {
		rule int
		path string
		want bool
	}{
		{0, "/blog/ok/amp", true},
		{0, "/blog/x\r/amp", false},
		{0, "/blog/x\n/amp", false},
		{0, "/blog/x\r\nSet-Cookie: a=b/amp", false},
		{0, "/blog/\x7f/amp", false},
		{1, "/docs/a/b", true},
		{1, "/docs/a\r", false},
		{1, "/docs/a\nb", false},
		{1, "/docs/a\r\nb", false},
	}
	for _, c := range cases {
		if got := regexp.MustCompile(rules[c.rule].from).MatchString(c.path); got != c.want {
			t.Errorf("%s on %q = %v, want %v", rules[c.rule].from, c.path, got, c.want)
		}
	}
}

// A visitor must not be able to steer a wildcard redirect to another host.
func TestRedirectSkipsTargetsThatCanLeaveTheSite(t *testing.T) {
	t.Parallel()
	for _, r := range []Redirect{
		{"/*", "/:splat", 301},
		{"/a/:p1", ":p1/x", 301},
		{"/a/:p1", "//evil.example/:p1", 301},
		{"/a/*", "//:splat", 301},
		{"/old/:p1", "/:p1", 301},
	} {
		rules, skipped := planRedirects([]Redirect{r})
		if len(skipped) != 1 || rules[0].skip == "" {
			t.Errorf("%+v must be skipped, got %+v", r, rules)
		}
	}
	rules, skipped := planRedirects([]Redirect{{"/a/*", "/b/:splat", 301}, {"/a/:p1", "/b/:p1", 301}, {"//x", "/y", 301}})
	if len(skipped) != 0 || len(rules) != 3 {
		t.Errorf("ordinary rules were skipped: %v", skipped)
	}
}
