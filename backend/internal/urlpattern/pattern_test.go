package urlpattern

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	bad := []string{"", "/", "post/{slug}", "/post", "/{slug}/x", "/a//{slug}", "/a/{slug}/", "/x-{slug}",
		"/{city}/{slug}", "/{slug}{slug}", "/{lang}/{slug}", "/{parent}/{lang}/{slug}", "/a/{slug", "/a}/{slug}", "/{parent}/{parent}/{slug}"}
	for _, in := range bad {
		if _, err := Parse(in); !errors.Is(err, ErrInvalidPattern) {
			t.Errorf("Parse(%q) err=%v, want ErrInvalidPattern", in, err)
		}
	}
	good := []string{"/{slug}", "/post/{slug}", "/blog/{slug}", "/docs/{parent}/{slug}", "/a/b/c/{slug}"}
	for _, in := range good {
		p, err := Parse(in)
		if err != nil || p.String() != in {
			t.Errorf("Parse(%q) = %v, %v", in, p, err)
		}
	}
}

func TestPatternProperties(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in           string
		first        string
		lits, tokens int
		parent       bool
	}{
		{"/{slug}", "", 0, 1, false},
		{"/post/{slug}", "post", 1, 1, false},
		{"/blog/{slug}", "blog", 1, 1, false},
		{"/docs/{parent}/{slug}", "docs", 1, 2, true},
	}
	for _, tc := range tests {
		p, err := Parse(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if p.FirstLiteral() != tc.first ||
			p.LiteralCount() != tc.lits || p.TokenCount() != tc.tokens || p.HasParent() != tc.parent {
			t.Errorf("%s: first=%q lits=%d tokens=%d parent=%v", tc.in,
				p.FirstLiteral(), p.LiteralCount(), p.TokenCount(), p.HasParent())
		}
	}
}

func TestRender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern string
		vars    Vars
		want    string
		wantErr bool
	}{
		{"/post/{slug}", Vars{Slug: "x"}, "/post/x", false},
		{"/{slug}", Vars{Slug: "x"}, "/x", false},
		{"/{slug}", Vars{}, "", true},
		{"/docs/{parent}/{slug}", Vars{Parent: "p", Slug: "x"}, "/docs/p/x", false},
		{"/docs/{parent}/{slug}", Vars{Slug: "x"}, "/docs/x", false},
		{"/{slug}", Vars{Slug: "a/b"}, "", true},
	}
	for _, tc := range tests {
		p, err := Parse(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Render(p, tc.vars)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("Render(%s,%+v) = %q, %v", tc.pattern, tc.vars, got, err)
		}
		if tc.wantErr && !errors.Is(err, ErrMissingVar) {
			t.Errorf("error should wrap ErrMissingVar: %v", err)
		}
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern string
		path    string
		want    Vars
		ok      bool
	}{
		{"/post/{slug}", "/post/x", Vars{Slug: "x"}, true},
		{"/post/{slug}", "/post/x/", Vars{Slug: "x"}, true},
		{"/post/{slug}", "/blog/x", Vars{}, false},
		{"/post/{slug}", "/post", Vars{}, false},
		{"/post/{slug}", "/post/x/y", Vars{}, false},
		{"/post/{slug}", "post/x", Vars{}, false},
		{"/post/{slug}", "/post//x", Vars{}, false},
		{"/{slug}", "/blog", Vars{Slug: "blog"}, true},
		{"/docs/{parent}/{slug}", "/docs/p/x", Vars{Parent: "p", Slug: "x"}, true},
		{"/docs/{parent}/{slug}", "/docs/x", Vars{Slug: "x"}, true},
		{"/docs/{parent}/{slug}", "/other/p/x", Vars{}, false},
		{"/docs/{parent}/{slug}", "/docs", Vars{}, false},
	}
	for _, tc := range tests {
		p, err := Parse(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := Match(p, tc.path)
		if ok != tc.ok || got != tc.want {
			t.Errorf("Match(%s,%q) = %+v,%v want %+v,%v", tc.pattern, tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRenderMatchRoundTrip(t *testing.T) {
	t.Parallel()
	for _, pat := range []string{"/post/{slug}", "/g/{parent}/{slug}"} {
		p, _ := Parse(pat)
		in := Vars{Slug: "s", Parent: "par"}
		if !p.HasParent() {
			in.Parent = ""
		}
		u, err := Render(p, in)
		if err != nil {
			t.Fatal(err)
		}
		out, ok := Match(p, u)
		if !ok || out != in {
			t.Errorf("%s: %q -> %+v ok=%v, want %+v", pat, u, out, ok, in)
		}
	}
}

func TestParseDateAndPathTokens(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"/{path}", "/{year}/{month}/{slug}", "/{year}/{month}/{day}/{slug}", "/blog/{path}", "/docs/{path}"} {
		if _, err := Parse(in); err != nil {
			t.Errorf("Parse(%q): %v", in, err)
		}
	}
	for _, in := range []string{"/{path}/x", "/{path}/{slug}", "/{parent}/{path}", "/{year}/{year}/{slug}", "/{year}/{month}", "/{path}/{path}"} {
		if _, err := Parse(in); !errors.Is(err, ErrInvalidPattern) {
			t.Errorf("Parse(%q) err=%v, want ErrInvalidPattern", in, err)
		}
	}
}

func TestRenderDateAndPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern string
		vars    Vars
		want    string
		wantErr bool
	}{
		{"/{year}/{month}/{slug}", Vars{Slug: "hello-world", Year: "2024", Month: "05"}, "/2024/05/hello-world", false},
		{"/{year}/{month}/{day}/{slug}", Vars{Slug: "x", Year: "2024", Month: "05", Day: "09"}, "/2024/05/09/x", false},
		{"/{year}/{slug}", Vars{Slug: "x"}, "", true},
		{"/{path}", Vars{Slug: "local", Path: "services/seo/local"}, "/services/seo/local", false},
		{"/{path}", Vars{Slug: "about"}, "/about", false},
		{"/docs/{path}", Vars{Slug: "b", Path: "a/b"}, "/docs/a/b", false},
		{"/{path}", Vars{Path: "a//b"}, "", true},
		{"/{path}", Vars{Path: "a/b/c/d/e/f/g/h/i"}, "", true},
		{"/{slug}", Vars{Slug: "a/b"}, "", true},
	}
	for _, tc := range tests {
		p, err := Parse(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Render(p, tc.vars)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("Render(%s, %+v) = %q, %v; want %q (err %v)", tc.pattern, tc.vars, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestMatchDateAndPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern, path string
		want          Vars
		ok            bool
	}{
		{"/{year}/{month}/{slug}", "/2024/05/hello-world", Vars{Slug: "hello-world", Year: "2024", Month: "05"}, true},
		{"/{year}/{month}/{slug}", "/2024/5/hello-world", Vars{}, false},
		{"/{year}/{month}/{slug}", "/blog/05/hello-world", Vars{}, false},
		{"/{year}/{month}/{day}/{slug}", "/2024/05/09/x/", Vars{Slug: "x", Year: "2024", Month: "05", Day: "09"}, true},
		{"/{path}", "/services/seo/local", Vars{Slug: "local", Path: "services/seo/local"}, true},
		{"/{path}", "/about", Vars{Slug: "about", Path: "about"}, true},
		{"/blog/{path}", "/blog/a/b", Vars{Slug: "b", Path: "a/b"}, true},
		{"/blog/{path}", "/blog", Vars{}, false},
		{"/{path}", "/a/b/c/d/e/f/g/h/i", Vars{}, false},
		{"/{path}", "/a//b", Vars{}, false},
	}
	for _, tc := range tests {
		p, err := Parse(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := Match(p, tc.path)
		if ok != tc.ok || got != tc.want {
			t.Errorf("Match(%s, %q) = %+v, %v; want %+v, %v", tc.pattern, tc.path, got, ok, tc.want, tc.ok)
		}
	}
}
