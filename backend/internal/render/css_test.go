package render

import (
	"html/template"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestSafeCSS(t *testing.T) {
	t.Parallel()
	// Child combinators, quotes and a literal "<" in a string stay usable.
	safe := `a > b{color:red}.c::after{content:"<"}.d::after{content:'>'}@media (min-width:1px){.e{margin:0}}`
	want := `a > b{color:red}.c::after{content:"<"}.d::after{content:'>'}@media (min-width:1px){.e{margin:0}}`
	if got := safeCSS(safe); got != template.CSS(want) {
		t.Errorf("safeCSS(%q) = %q, want %q", safe, got, want)
	}
	for _, bad := range []string{
		`@import url(//evil/x.css);`,
		`@IMPORT "x";`,
		`a{width:expression(alert(1))}`,
		`a{width:EXPRESSION (1)}`,
		`a{background:url(javascript:alert(1))}`,
		`a{behavior:url(x.htc)}`,
		`a{-moz-binding:url(x)}`,
	} {
		if got := safeCSS(bad); got != "" {
			t.Errorf("safeCSS(%q) = %q, want empty", bad, got)
		}
	}
	for _, in := range []string{`<`, `a{}<`, `</style>`, `</STYLE >`, "</style\t", `</style/`, `<!-- x`, `<?x`, `<img src=x onerror=alert(1)>`} {
		if got := safeCSS(in); strings.Contains(string(got), "<") {
			t.Errorf("safeCSS(%q) = %q still has <", in, got)
		}
	}
}

var cssBreakoutTmpl = template.Must(template.New("t").Parse(`<style>{{.A}}{{.B}}</style>`))

// Two safe-looking halves joined in one <style> must not rebuild "</style>".
func TestSafeCSSJoinedValuesCannotCloseStyle(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	err := cssBreakoutTmpl.Execute(&sb, map[string]template.CSS{
		"A": safeCSS("a{}</sty"), "B": safeCSS("le><script>alert(1)</script>"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := sb.String()
	if strings.Count(got, "<") != 2 || strings.Contains(got, "<script") || strings.Contains(strings.ToLower(got), "</style><") {
		t.Errorf("joined values broke out: %s", got)
	}
}

// Browsers parse <svg><style> as markup, so a "<" there would open a tag.
func TestSafeCSSInsideSVGStyleCannotInjectTag(t *testing.T) {
	t.Parallel()
	tmpl := template.Must(template.New("t").Parse(`<svg><style>{{.}}</style></svg>`))
	var sb strings.Builder
	if err := tmpl.Execute(&sb, safeCSS("<img src=x onerror=alert(2)>")); err != nil {
		t.Fatal(err)
	}
	got := sb.String()
	if strings.Contains(got, "<img") || strings.Count(got, "<") != 4 {
		t.Errorf("tag injected inside svg style: %s", got)
	}
}

func TestCSSKindPrepared(t *testing.T) {
	t.Parallel()
	n := &schemaNode{Kind: "css"}
	if got := prepareValue(n, "a>b{c:d}", 0, "", false); got != template.CSS("a>b{c:d}") {
		t.Errorf("got %#v", got)
	}
	if got := prepareValue(n, "@import 'x';", 0, "", false); got != template.CSS("") {
		t.Errorf("unsafe css kept: %#v", got)
	}
}

func TestCSSKindLoadsUnknownStillRejected(t *testing.T) {
	t.Parallel()
	if _, _, _, err := parseSectionSchema([]byte(`{"type":"object","properties":{"c":{"type":"string","x-hm-field":"css"}}}`)); err != nil {
		t.Fatalf("css kind must load: %v", err)
	}
	if _, _, _, err := parseSectionSchema([]byte(`{"type":"object","properties":{"c":{"type":"string","x-hm-field":"cssx"}}}`)); err == nil {
		t.Fatal("unknown kind must still be rejected")
	}
}

// A theme's <style> receives the css field unescaped (child combinators and
// quotes intact), and an unsafe value renders an empty style element.
func TestCSSFieldGolden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "sections/styled.html", `{{define "section-styled"}}<style>{{.Content.css}}</style><div class="x">{{.Content.label}}</div>{{end}}`)
	writeFile(t, dir, "schemas/styled.json", `{"x-hm-section":{"title":"Styled"},"type":"object","properties":{"css":{"type":"string","x-hm-field":"css"},"label":{"type":"string","x-hm-field":"plaintext"}}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})
	render := func(css string) string {
		v := minimalView()
		v.Sections = []Section{{ID: "s1", Type: "styled", Content: map[string]any{"css": css, "label": "L"}}}
		return mainInner(t, renderToString(t, e, v))
	}
	got := render(`.x > p{color:red}.x::after{content:"a"}`)
	if !strings.Contains(got, `<style>.x > p{color:red}.x::after{content:"a"}</style>`) {
		t.Errorf("css not emitted verbatim:\n%s", got)
	}
	compareGolden(t, "css-field", got)
	if bad := render(`@import url(//evil/x.css);`); !strings.Contains(bad, "<style></style>") {
		t.Errorf("unsafe css not dropped:\n%s", bad)
	}
	// End to end: a break-out attempt is escaped, never a live tag.
	esc := render(`.x{}</style><script>alert(1)</script>`)
	if strings.Contains(esc, "<script") || !strings.Contains(esc, `.x{}\3c /style>\3c script>alert(1)\3c /script>`) {
		t.Errorf("break-out not escaped:\n%s", esc)
	}
	if got := render(`.y::after{content:"<b"}`); !strings.Contains(got, `<style>.y::after{content:"\3c b"}</style>`) {
		t.Errorf("literal < not escaped:\n%s", got)
	}
}

func TestSafeCSSMediaRangeSyntaxPreserved(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		`@media (width < 600px){a{b:c}}`,
		`@media (400px <= width <= 700px){a{b:c}}`,
		`@media (width<600px){a{b:c}}`,
		`a::after{content:"<"}`,
		`a::after{content:"1<2"}`,
	} {
		if got := safeCSS(in); string(got) != in {
			t.Errorf("safeCSS(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestSafeCSSEscapesTagStartingLT(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`a<b`: `a\3c b`, `a</b`: `a\3c /b`, `a<!b`: `a\3c !b`, `a<?b`: `a\3c ?b`,
		`<Z`: `\3c Z`, `a{}<`: `a{}\3c `,
	} {
		if got := safeCSS(in); string(got) != want {
			t.Errorf("safeCSS(%q) = %q, want %q", in, got, want)
		}
	}
}

// "<" ending value A is escaped, so "/style>" in value B cannot complete it.
func TestSafeCSSTrailingLTJoinedWithClosingTag(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	if err := cssBreakoutTmpl.Execute(&sb, map[string]template.CSS{"A": safeCSS("a{}<"), "B": safeCSS("/style><script>x</script>")}); err != nil {
		t.Fatal(err)
	}
	if got := sb.String(); strings.Count(strings.ToLower(got), "</style>") != 1 || strings.Contains(got, "<script") {
		t.Errorf("joined values rebuilt a closing tag: %q", got)
	}
	if strings.Count(sb.String(), "<") != 2 {
		t.Errorf("want only the template's own <style> and </style>, got %q", sb.String())
	}
}

func TestSafeCSSSVGJoinedTrailingLT(t *testing.T) {
	t.Parallel()
	tmpl := template.Must(template.New("s").Parse(`<svg><style>{{.A}}{{.B}}</style></svg>`))
	var sb strings.Builder
	if err := tmpl.Execute(&sb, map[string]template.CSS{"A": safeCSS("a{}<"), "B": safeCSS("img src=x onerror=alert(1)>")}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sb.String(), "<img") {
		t.Errorf("svg style produced a tag: %q", sb.String())
	}
}

const urlBodyMsg = "line 1: url( may not contain a comment, quote, parenthesis or backslash unless quoted"

func TestValidateCustomCSS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, css, want string
	}{
		{"import", "a{}\n@import url(x.css);", "line 2: @import is not allowed"},
		{"import upper", "@IMPORT 'x';", "line 1: @import is not allowed"},
		{"expression", "a{\n\nwidth:expression(1)}", "line 3: expression() is not allowed"},
		{"javascript", "a{background:url( \"javascript:alert(1)\" )}", "line 1: javascript: URLs are not allowed"},
		{"javascript unquoted", "a{background:url(javascript:alert)}", "line 1: javascript: URLs are not allowed"},
		{"javascript unquoted call", "a{background:url(javascript:alert(1))}", urlBodyMsg},
		{"vbscript", "a{background:url(vbscript:x)}", "line 1: vbscript: URLs are not allowed"},
		{"behavior", "a{behavior:url(x.htc)}", "line 1: behavior is not allowed"},
		{"moz-binding", "a{-moz-binding:url(x)}", "line 1: -moz-binding is not allowed"},
		{"escaped url hides import", "u\\72l(/*){}@import 'x';/**/", urlBodyMsg},
		{"escaped url hex start", "\\75rl(/*){}@import 'x';/**/", urlBodyMsg},
		{"escaped url char", "ur\\l(/*){}@import 'x';/**/", urlBodyMsg},
		{"review payload xurl", "xurl(/*)'*/'/*'){}@import url(//evil.example/x.css);/**/", "line 1: @import is not allowed"},
		{"dash url function", "-url(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"letter url function", "aurl(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"escaped prefix url function", "\\!url(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"hex escaped prefix url function", "\\61 url(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"digit prefix url function", "1url(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"line continuation in ident", "u\\\nrl(/*)'*/'/*'){}@import 'x';/**/", "line 1: a backslash before a line break is not allowed"},
		{"line continuation before url", "x\\\nurl(/*)'*/'/*'){}@import 'x';/**/", "line 1: a backslash before a line break is not allowed"},
		{"hex escape", "a{background:url(\\6a avascript:alert(1))}", "line 1: url( may not contain a comment, quote, parenthesis or backslash unless quoted"},
		{"hex escape quoted", "a{background:url('\\6a avascript:alert(1)')}", "line 1: javascript: URLs are not allowed"},
		{"review payload hash url", "#url(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"review payload at url", "@url(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"review payload two escapes", "\\21\\75rl(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"escape then letter escape", "\\!\\75rl(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"escapes with spaces", "\\21 \\55 RL(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"escape inside url", "\\21u\\72l(/*)'*/'/*'){}@import 'x';/**/", "line 1: @import is not allowed"},
		{"comment in url body", "a{b:url(/*)'*/'/*'){}@import 'x';/**/", urlBodyMsg},
		{"comment in url body after space", "a{b:url( /* x */ )}", urlBodyMsg},
		{"quote in url body", "a{b:url(a'b)}", urlBodyMsg},
		{"double quote in url body", "a{b:url(a\"b)}", urlBodyMsg},
		{"paren in url body", "a{b:url(a(b)}", urlBodyMsg},
		{"escape in string", "a{b:'\\6A avascript:x'}", "line 1: javascript: URLs are not allowed"},
		{"escaped import", "\n\n@\\69mport 'x';", "line 3: @import is not allowed"},
		{"import after comment", "/* a\nb */\n@import 'x';", "line 3: @import is not allowed"},
		{"unclosed comment", "a{}\n/* x", "line 2: comment is not closed"},
		{"unclosed string", "a{b:'x\n}", "line 1: string is not closed"},
		{"unclosed url", "a{b:url(x", "line 1: url( is not closed"},
		{"nul", "a{}\x00", "line 1: NUL bytes are not allowed"},
	} {
		got := ValidateCustomCSS(tc.css)
		if got == nil || got.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.name, got, tc.want)
		}
	}
	for _, ok := range []string{
		"",
		"@media (width < 600px){a>b{background:url(/x.png)}}",
		"/* @import */ a{color:red}",
		"a{background:url(x.png)}",
		`a{background:url("a.png")}`,
		"a{background:url(a.png)}",
		"a{background:url( a.png )}",
		"a{background:url('a.png')}",
		"a{background:URL(a.png)}",
		"a{b:foo-url(1)}",
		"a{b:'a\\\nb'}",         // line continuation inside a string
		"a{b:java/**/script:x}", // a comment separates tokens: not javascript:
	} {
		if got := ValidateCustomCSS(ok); got != nil {
			t.Errorf("ValidateCustomCSS(%q) = %v, want nil", ok, got)
		}
	}
}

func TestValidateCustomCSSLimit(t *testing.T) {
	t.Parallel()
	pad := func(n int) string { return "/*" + strings.Repeat("x", n-4) + "*/" }
	if got := ValidateCustomCSS(pad(MaxCustomCSS)); got != nil {
		t.Errorf("64 KB: %v, want nil", got)
	}
	got := ValidateCustomCSS(pad(MaxCustomCSS + 1))
	if got == nil || !strings.Contains(got.Error(), "64 KB") {
		t.Errorf("64 KB + 1: %v, want limit error", got)
	}
	if _, ok := SanitizeCustomCSS("  \n"); ok {
		t.Error("blank CSS should not sanitise")
	}
}
