package a11y

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func has(issues []string, sub string) bool {
	for _, i := range issues {
		if strings.Contains(i, sub) {
			return true
		}
	}
	return false
}

// The checker must flag each class of mistake it claims to catch.
func TestCheckFlagsViolations(t *testing.T) {
	cases := map[string]struct{ html, want string }{
		"unlabelled input":    {`<form><input name="a" id="a"></form>`, "no accessible name"},
		"dup id":              {`<p id="x"></p><p id="x"></p>`, "duplicate id"},
		"missing ref":         {`<p aria-describedby="nope">x</p>`, "missing id"},
		"empty button":        {`<button type="button"></button>`, "no accessible text"},
		"icon link":           {`<a href="/x"><svg aria-hidden="true"></svg></a>`, "no accessible text"},
		"img alt":             {`<img src="/a.png">`, "no alt"},
		"bare svg":            {`<svg></svg>`, "svg"},
		"nav label":           {`<nav><a href="/">x</a></nav>`, "nav without"},
		"onclick":             {`<button onclick="x()">go</button>`, "inline handler"},
		"style attr":          {`<p style="color:red">x</p>`, "inline style"},
		"js href":             {`<a href="javascript:x()">go</a>`, "javascript:"},
		"hx-on":               {`<button hx-on:click="x()">go</button>`, "hx-on"},
		"hx-trigger filter":   {`<button hx-get="/x" hx-trigger="click[ctrlKey]">go</button>`, "hx-trigger filter"},
		"hx-headers js":       {`<button hx-headers="js:{a:1}">go</button>`, "js:"},
		"table caption":       {`<table><tr><th scope="col">a</th></tr></table>`, "caption"},
		"th scope":            {`<table aria-label="t"><tr><th>a</th></tr></table>`, "no scope"},
		"sort header":         {`<table aria-label="t"><tr><th scope="col" aria-sort="none">a</th></tr></table>`, "no link or button"},
		"dialog label":        {`<dialog id="d"><p>x</p></dialog>`, "labelled by a heading"},
		"tabindex":            {`<div tabindex="3">x</div>`, "positive tabindex"},
		"nameless in form":    {`<form><label>x <input></label></form>`, "no name"},
		"csrf":                {`<form method="post" action="/a"><button type="submit">go</button></form>`, "_csrf"},
		"invalid undescribed": {`<label for="a">A</label><input id="a" name="a" aria-invalid="true">`, "aria-invalid"},
		"error not invalid":   {`<label for="a">A</label><input id="a" name="a" aria-describedby="e"><p id="e" class="hm-field__error">bad</p>`, "not aria-invalid"},
	}
	for name, c := range cases {
		if got := Check(c.html, Options{}); !has(got, c.want) {
			t.Errorf("%s: want an issue containing %q, got %v", name, c.want, got)
		}
	}
	page := map[string]struct{ html, want string }{
		"no h1":    {`<html lang="en"><head><title>x · Hatamoto</title></head><body><a href="#hm-main">Skip</a><main id="hm-main"><h2>x</h2></main></body></html>`, "<h1>"},
		"skip":     {`<html lang="en"><head><title>x · Hatamoto</title></head><body><main id="hm-main"><h1>x</h1></main></body></html>`, "skip link"},
		"lang":     {`<html><head><title>x · Hatamoto</title></head><body><a href="#hm-main">Skip</a><main id="hm-main"><h1>x</h1></main></body></html>`, "lang"},
		"title":    {`<html lang="en"><head><title>x</title></head><body><a href="#hm-main">Skip</a><main id="hm-main"><h1>x</h1></main></body></html>`, "title"},
		"headings": {`<html lang="en"><head><title>x · Hatamoto</title></head><body><a href="#hm-main">Skip</a><main id="hm-main"><h1>x</h1><h3>y</h3></main></body></html>`, "jumps"},
	}
	for name, c := range page {
		if got := Check(c.html, Options{Page: true}); !has(got, c.want) {
			t.Errorf("page %s: want %q, got %v", name, c.want, got)
		}
	}
	ok := `<html lang="en"><head><title>x · Hatamoto</title></head><body><a href="#hm-main">Skip</a><main id="hm-main"><h1>x</h1>
<form method="post"><input type="hidden" name="_csrf" value="t"><label for="a">A</label><input id="a" name="a"><button type="submit">Go</button></form></main></body></html>`
	if got := Check(ok, Options{Page: true}); len(got) != 0 {
		t.Errorf("clean page flagged: %v", got)
	}
}

func TestRatio(t *testing.T) {
	r, err := Ratio("#000000", "#ffffff")
	if err != nil || r < 20.9 || r > 21.1 {
		t.Fatalf("black on white = %v %v", r, err)
	}
	if r, _ := Ratio("#767676", "#ffffff"); r < 4.5 || r > 4.6 {
		t.Fatalf("#767676 on white = %v", r)
	}
}

func readTokens(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "admin", "css", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Light and dark token pairs the partials rely on meet WCAG 2.2 AA.
func TestTokenContrast(t *testing.T) {
	if issues := ContrastIssues(readTokens(t)); len(issues) > 0 {
		t.Fatalf("contrast:\n%s", strings.Join(issues, "\n"))
	}
}

// The plugin-facing contract: these names exist in tokens.css and never go away.
func TestTokensContract(t *testing.T) {
	light, dark := ParseTokens(readTokens(t))
	for _, name := range []string{
		"--color-shiro", "--color-paper", "--color-kasumi", "--color-kinu", "--color-nezumi", "--color-hai", "--color-sumi-soft", "--color-sumi",
		"--color-accent", "--color-accent-soft", "--color-success", "--color-success-soft", "--color-warning", "--color-warning-soft", "--color-danger", "--color-danger-soft",
		"--admin-accent", "--border-control", "--border-field", "--radius-sm", "--radius-md", "--radius-lg", "--radius-nav", "--control-h", "--sidebar-w", "--content-max",
		"--space-2", "--space-4", "--font-ui", "--font-mono", "--bg", "--surface", "--text", "--text-muted", "--border",
		"--color-brand", "--chart-1", "--chart-2", "--chart-3", "--chart-4", "--chart-5", "--chart-6",
		"--chart-stack-1", "--chart-stack-5", "--chart-stack-other", "--focus", "--tooltip-bg", "--tooltip-fg", "--control-accent",
		"--space-5", "--space-12", "--text-xs", "--text-2xl", "--shadow-1", "--shadow-2", "--shadow-3", "--ease", "--scrim",
		"--subnav-w", "--page-narrow", "--page-wide", "--page-pad", "--page-pad-x", "--page-pad-y", "--font-serif",
		"--radius-full", "--text-title", "--text-metric",
	} {
		if _, ok := light[name]; !ok {
			t.Errorf("light theme lacks %s", name)
		}
		if _, ok := dark[name]; !ok {
			t.Errorf("dark theme lacks %s", name)
		}
	}
}

var varUse = regexp.MustCompile(`var\((--[a-z0-9-]+)`)

// Every var(--x) the CSS uses is a token or defined locally in the same file.
func TestPartialCSSUsesDefinedTokens(t *testing.T) {
	light, _ := ParseTokens(readTokens(t))
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "web", "admin", "css", "*", "*.css"))
	files = append(files, filepath.Join("..", "..", "..", "web", "admin", "css", "base.css"))
	if len(files) < 5 {
		t.Fatalf("found only %d css files", len(files))
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		defined := map[string]bool{}
		for _, m := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:`).FindAllStringSubmatch(string(b), -1) {
			defined[m[1]] = true
		}
		for _, m := range varUse.FindAllStringSubmatch(string(b), -1) {
			if _, ok := light[m[1]]; !ok && !defined[m[1]] {
				t.Errorf("%s uses undefined %s", filepath.Base(f), m[1])
			}
		}
	}
}

// Interactive partials define a focus style, none removes outlines, base.css
// respects reduced motion, and controls are at least 24px (WCAG 2.5.8).
func TestCSSAccessibilityLint(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "web", "admin", "css")
	for _, n := range []string{"button", "field", "tabs", "pagination", "table", "pageheader", "sidebar", "mediatile"} {
		b, err := os.ReadFile(filepath.Join(dir, "partials", n+".css"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), ":focus-visible") {
			t.Errorf("%s.css has no :focus-visible rule", n)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "partials", "*.css"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if regexp.MustCompile(`outline:\s*(none|0)\b`).Match(b) {
			t.Errorf("%s removes the outline", filepath.Base(f))
		}
	}
	base, _ := os.ReadFile(filepath.Join(dir, "base.css"))
	if !strings.Contains(string(base), "prefers-reduced-motion") {
		t.Error("base.css has no prefers-reduced-motion block")
	}
	light, _ := ParseTokens(readTokens(t))
	for _, tok := range []string{"--control-h", "--control-h-sm"} {
		var px int
		if _, err := fmtSscan(light[tok], &px); err != nil || px < 24 {
			t.Errorf("%s = %q, want >= 24px", tok, light[tok])
		}
	}
}

// The "auto" theme (no choice made, follow the browser) must carry exactly the
// dark tokens: the CSS cannot share one block, so this keeps the copies equal.
func TestAutoThemeMatchesDark(t *testing.T) {
	css := readTokens(t)
	body := func(open string) string {
		i := strings.Index(css, open)
		if i < 0 {
			t.Fatalf("%q not found", open)
		}
		rest := css[i+len(open):]
		return strings.Join(strings.Fields(rest[:strings.Index(rest, "}")]), " ")
	}
	if body(`:root[data-admin-theme="dark"] {`) != body(`:root[data-admin-theme="auto"] {`) {
		t.Error("the auto (prefers-color-scheme) block differs from the dark block in tokens.css")
	}
}

// The soft dark palette (v1-23e): the dark block, and so the identical auto
// block, carries exactly these values.
func TestSoftDarkPalette(t *testing.T) {
	_, dark := ParseTokens(readTokens(t))
	want := map[string]string{
		"--color-shiro": "#141416", "--color-paper": "#1a1a1d", "--color-kasumi": "#222226", "--color-kinu": "#2c2c31",
		"--color-nezumi": "#3a3a40", "--color-hai": "#a3a3ab", "--color-sumi-soft": "#c2c2c8", "--color-sumi": "#d6d6da",
		"--border-control": "#6f6f78", "--color-accent": "#6cb4ff", "--color-accent-soft": "#132a44",
		"--color-danger": "#f0948a", "--color-success": "#86c9a6", "--color-warning": "#e3b872",
	}
	for name, v := range want {
		if dark[name] != v {
			t.Errorf("dark %s = %q, want %s", name, dark[name], v)
		}
	}
}
