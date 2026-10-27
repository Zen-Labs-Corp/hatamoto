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
