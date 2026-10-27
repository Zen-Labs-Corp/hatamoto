package main

import (
	"regexp"
	"strings"
	"testing"
)

func linkTo(href string) (string, error) {
	path, frag, _ := strings.Cut(href, "#")
	path = strings.TrimSuffix(strings.TrimPrefix(path, "../"), ".md")
	out := "/docs/" + path
	if frag != "" {
		out += "#" + frag
	}
	return out, nil
}

func conv(t *testing.T, md string) string {
	t.Helper()
	out, err := Convert(md, linkTo)
	if err != nil {
		t.Fatalf("Convert(%q): %v", md, err)
	}
	return out
}

func TestConvertBlocks(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ in, want string }{
		"heading and paragraph":  {"## Title\n\nsome *em* and **strong** text\nsecond line", "<h2>Title</h2>\n<p>some <em>em</em> and <strong>strong</strong> text\nsecond line</p>\n"},
		"h3 and h4":              {"### A\n#### B", "<h3>A</h3>\n<h4>B</h4>\n"},
		"rule":                   {"a\n\n---\n\nb", "<p>a</p>\n<hr>\n<p>b</p>\n"},
		"blockquote":             {"> quoted **x**\n> more", "<blockquote>\n<p>quoted <strong>x</strong>\nmore</p>\n</blockquote>\n"},
		"fenced code":            {"```go\nif a < b && c {\n}\n```", "<pre><code class=\"language-go\">if a &lt; b &amp;&amp; c {\n}</code></pre>\n"},
		"fence no language":      {"```\nx\n```", "<pre><code>x</code></pre>\n"},
		"inline code":            {"use `<div> & more` now", "<p>use <code>&lt;div&gt; &amp; more</code> now</p>\n"},
		"escapes":                {"a < b & c > d", "<p>a &lt; b &amp; c &gt; d</p>\n"},
		"snake case literal":     {"set MAX_UPLOAD_SIZE and _x_ here", "<p>set MAX_UPLOAD_SIZE and <em>x</em> here</p>\n"},
		"escaped star":           {`a \* b`, "<p>a * b</p>\n"},
		"table":                  {"| A | B |\n|---|:-:|\n| 1 | `x\\|y` |\n| 2 | **z** |", "<table>\n<thead>\n<tr><th>A</th><th>B</th></tr>\n</thead>\n<tbody>\n<tr><td>1</td><td><code>x|y</code></td></tr>\n<tr><td>2</td><td><strong>z</strong></td></tr>\n</tbody>\n</table>\n"},
		"bullets":                {"- a\n- b\n  continued", "<ul>\n<li>a</li>\n<li>b\ncontinued</li>\n</ul>\n"},
		"numbered":               {"1. one\n2. two", "<ol>\n<li>one</li>\n<li>two</li>\n</ol>\n"},
		"nested list":            {"- a\n  - b\n    1. c\n- d", "<ul>\n<li>a\n<ul>\n<li>b\n<ol>\n<li>c</li>\n</ol>\n</li>\n</ul>\n</li>\n<li>d</li>\n</ul>\n"},
		"list ends at paragraph": {"- a\n\ntext", "<ul>\n<li>a</li>\n</ul>\n<p>text</p>\n"},
	}
	for name, tc := range cases {
		if got := conv(t, tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}

func TestConvertLinks(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"[x](../themes/layout.md#toc)":          `<p><a href="/docs/themes/layout#toc">x</a></p>`,
		"[x](page.md)":                          `<p><a href="/docs/page">x</a></p>`,
		"[x](https://example.com/a?b=1&c)":      `<p><a href="https://example.com/a?b=1&amp;c">x</a></p>`,
		"[x](#frag)":                            `<p><a href="#frag">x</a></p>`,
		`[x](/blog/hello "T")`:                  `<p><a href="/blog/hello" title="T">x</a></p>`,
		"[`c` **b**](mailto:a@example.com)":     `<p><a href="mailto:a@example.com"><code>c</code> <strong>b</strong></a></p>`,
		"![alt *x*](https://example.com/i.png)": `<p><img src="https://example.com/i.png" alt="alt x"></p>`,
		"![a](img/local.png)":                   `<p><img src="img/local.png" alt="a"></p>`,
		"plain [not a link] text":               `<p>plain [not a link] text</p>`,
	}
	for in, want := range cases {
		if got := strings.TrimSpace(conv(t, in)); got != want {
			t.Errorf("%q:\n got %s\nwant %s", in, got, want)
		}
	}
}

func TestConvertLeavesMdLinksWithoutResolver(t *testing.T) {
	t.Parallel()
	out, err := Convert("[x](a.md)", nil)
	if err != nil || !strings.Contains(out, `href="a.md"`) {
		t.Fatalf("%q %v", out, err)
	}
}

func TestConvertErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ in, sub string }{
		"h1":               {"para\n\n# Title", "line 3: h1"},
		"h5":               {"##### x", "line 1: headings deeper"},
		"raw block html":   {"ok\n\n<div>x</div>", "line 3: raw HTML"},
		"raw inline html":  {"a <b>bold</b>", "line 1: raw HTML"},
		"html comment":     {"a <!-- c -->", "raw HTML"},
		"indented code":    {"para\n\n    code", "line 3: indented code"},
		"unclosed fence":   {"x\n```go\ncode", "line 2: code fence is never closed"},
		"odd fence":        {"```go title=x\ncode\n```", "unsupported code fence"},
		"unclosed code":    {"a `b", "unclosed inline code"},
		"js link":          {"[x](javascript:alert(1))", "scheme"},
		"data image":       {"![x](data:image/png;base64,AAAA)", "image scheme"},
		"http image":       {"![x](http://example.com/a.png)", "image scheme"},
		"ragged table row": {"| A | B |\n|---|---|\n| 1 |", "line 3: table row"},
		"error in quote":   {"x\n\n> a\n> <i>b</i>", "line 3"},
		"error in list":    {"- a\n- <i>b</i>", "line 2"},
	}
	for name, tc := range cases {
		_, err := Convert(tc.in, linkTo)
		if err == nil || !strings.Contains(err.Error(), tc.sub) {
			t.Errorf("%s: err = %v, want containing %q", name, err, tc.sub)
		}
	}
}

func TestConvertLinkResolverError(t *testing.T) {
	t.Parallel()
	_, err := Convert("[x](gone.md)", func(string) (string, error) { return "", errString("no such page") })
	if err == nil || !strings.Contains(err.Error(), "no such page") || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("err = %v", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// Copy of the server rich-text policy (backend/internal/entity/sanitize.go).
var (
	allowedTags = setOf("h1 h2 h3 h4 h5 h6 p br hr div span ul ol li strong em b i u s blockquote code pre figure figcaption table thead tbody tr th td a img")
	allowedAttr = map[string]map[string]bool{
		"*":   setOf("class"),
		"a":   setOf("href title rel target"),
		"img": setOf("src alt title width height loading"),
	}
)

func setOf(s string) map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(s) {
		m[f] = true
	}
	return m
}

var (
	tagScanRe  = regexp.MustCompile(`<(/?)([A-Za-z][A-Za-z0-9]*)((?:\s+[a-z-]+="[^"]*")*)\s*/?>`)
	anyAngleRe = regexp.MustCompile(`<[^>]*>?`)
	attrScanRe = regexp.MustCompile(`([a-z-]+)="`)
)

// TestConvertAllowlist checks every tag and attribute of a rich sample against
// the policy copy, using only a regex scan (no HTML parser).
func TestConvertAllowlist(t *testing.T) {
	t.Parallel()
	sample := "## Heading\n\nText with **b**, *i*, `c<x>` and [link](../a/b.md#c) and [ext](https://example.com).\n\n" +
		"![pic](https://example.com/p.png)\n\n> quote\n\n- a\n  - b\n1. n\n\n---\n\n" +
		"```sh\necho \"<hi>\" && ls\n```\n\n| H | I |\n|---|---|\n| `a` | [l](#x) |\n\n### Sub\n#### Deep\n"
	out := conv(t, sample)
	if got, want := len(anyAngleRe.FindAllString(out, -1)), len(tagScanRe.FindAllString(out, -1)); got != want {
		t.Fatalf("output has %d angle brackets groups but %d well formed tags:\n%s", got, want, out)
	}
	for _, m := range tagScanRe.FindAllStringSubmatch(out, -1) {
		tag := m[2]
		if !allowedTags[tag] {
			t.Errorf("tag <%s> is not allowlisted", tag)
		}
		for _, a := range attrScanRe.FindAllStringSubmatch(m[3], -1) {
			if !allowedAttr["*"][a[1]] && !allowedAttr[tag][a[1]] {
				t.Errorf("attribute %s on <%s> is not allowlisted", a[1], tag)
			}
		}
	}
	for _, banned := range []string{" id=", " style=", "<details", "<kbd", "<sup", "<dl"} {
		if strings.Contains(out, banned) {
			t.Errorf("output contains %q", banned)
		}
	}
}

func TestConvertListItemBlocks(t *testing.T) {
	t.Parallel()
	link := func(h string) (string, error) { return h, nil }
	cases := map[string]struct{ in, want string }{
		"fence under a step": {
			"1. Run:\n\n   ```bash\n   make <x>\n   ```\n2. Done.\n",
			"<ol>\n<li>Run:\n<pre><code class=\"language-bash\">make &lt;x&gt;</code></pre></li>\n<li>Done.</li>\n</ol>\n",
		},
		"fence without blank line": {
			"- a\n  ```\n  x\n  ```\n- b\n",
			"<ul>\n<li>a\n<pre><code>x</code></pre></li>\n<li>b</li>\n</ul>\n",
		},
		"table under a step": {
			"1. See:\n\n   | A | B |\n   |---|---|\n   | 1 | 2 |\n\n2. Next.\n",
			"<ol>\n<li>See:\n<table>\n<thead>\n<tr><th>A</th><th>B</th></tr>\n</thead>\n<tbody>\n<tr><td>1</td><td>2</td></tr>\n</tbody>\n</table></li>\n<li>Next.</li>\n</ol>\n",
		},
		"paragraph table and fence in one item": {
			"1. Intro\n\n   More text.\n\n   | A |\n   |---|\n   | 1 |\n\n   ```sh\n   ls\n   ```\n2. Done.\n",
			"<ol>\n<li>Intro\n<p>More text.</p>\n<table>\n<thead>\n<tr><th>A</th></tr>\n</thead>\n<tbody>\n<tr><td>1</td></tr>\n</tbody>\n</table>\n<pre><code class=\"language-sh\">ls</code></pre></li>\n<li>Done.</li>\n</ol>\n",
		},
		"two fences in one item": {
			"1. Run:\n\n   ```bash\n   a\n   ```\n\n   ```bash\n   b\n   ```\n\n2. Next.\n",
			"<ol>\n<li>Run:\n<pre><code class=\"language-bash\">a</code></pre>\n<pre><code class=\"language-bash\">b</code></pre></li>\n<li>Next.</li>\n</ol>\n",
		},
		"fence then continuation paragraph": {
			"1. Run:\n\n   ```bash\n   a\n   ```\n\n   Then wait.\n\n2. Next.\n",
			"<ol>\n<li>Run:\n<pre><code class=\"language-bash\">a</code></pre>\n<p>Then wait.</p></li>\n<li>Next.</li>\n</ol>\n",
		},
		"nested list then fence": {
			"- a\n  - b\n\n  ```\n  x\n  ```\n- c\n",
			"<ul>\n<li>a\n<ul>\n<li>b</li>\n</ul>\n<pre><code>x</code></pre></li>\n<li>c</li>\n</ul>\n",
		},
		"one list across blocks keeps numbering": {
			"3. Three\n\n   ```\n   x\n   ```\n\n4. Four\n",
			"<ol>\n<li>Three\n<pre><code>x</code></pre></li>\n<li>Four</li>\n</ol>\n",
		},
	}
	for name, c := range cases {
		got, err := Convert(c.in, link)
		if err != nil || got != c.want {
			t.Errorf("%s:\n got %q (%v)\nwant %q", name, got, err, c.want)
		}
	}
	for name, in := range map[string]string{
		"unclosed fence": "- a\n  ```\n  x\n",
		"shallow fence":  "1. a\n ```\n x\n ```\n",
	} {
		if _, err := Convert(in, link); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
