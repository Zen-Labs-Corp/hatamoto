package entity

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type richSample struct {
	Name string `json:"name"`
	HTML string `json:"html"`
}

func loadRichSamples(t *testing.T) []richSample {
	t.Helper()
	raw, err := os.ReadFile("testdata/rich-html-samples.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []richSample
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	return samples
}

// normalise parses a fragment and re-renders it, optionally removing what the
// policy must remove (script/style elements and on* handlers) first, so two
// markups compare structurally rather than byte-for-byte.
func normalise(t *testing.T, src string, removeUnsafe bool) string {
	t.Helper()
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(src), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var strip func(n *html.Node)
	strip = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode && (c.Data == "script" || c.Data == "style") {
				n.RemoveChild(c)
			} else {
				kept := c.Attr[:0]
				for _, a := range c.Attr {
					if !strings.HasPrefix(strings.ToLower(a.Key), "on") {
						kept = append(kept, a)
					}
				}
				c.Attr = kept
				strip(c)
			}
			c = next
		}
	}
	var sb strings.Builder
	for _, n := range nodes {
		if removeUnsafe {
			wrap := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
			wrap.AppendChild(n)
			strip(wrap)
			for c := wrap.FirstChild; c != nil; c = c.NextSibling {
				_ = html.Render(&sb, c)
			}
			continue
		}
		_ = html.Render(&sb, n)
	}
	return sb.String()
}

func TestHTMLSectionPolicy_KeepsAllowlistedSamples(t *testing.T) {
	samples := loadRichSamples(t)
	if len(samples) != 12 {
		t.Fatalf("want 12 samples, got %d", len(samples))
	}
	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			got := normalise(t, htmlSection().Sanitize(s.HTML), false)
			want := normalise(t, s.HTML, true)
			if got != want {
				t.Errorf("policy altered allowlisted markup\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestHTMLSectionPolicy_StripsUnsafe(t *testing.T) {
	in := `<div><script>alert(1)</script><style>p{}</style>` +
		`<a href="javascript:alert(1)" onclick="x()">a</a><img src=x onerror=y()>` +
		`<form action="/x"><input name="a"></form><svg><foreignObject><p>x</p></foreignObject></svg>` +
		`<p style="background:expression(alert(1))">e</p>` +
		`<p style="background:url(javascript:alert(1))">j</p>` +
		`<p style="background:url(data:text/html,x)">d</p>` +
		`<p style="color:red;--gap:4px;background:url(/img/a.png)">ok</p></div>`
	out := sanitizeHTMLBody(in)
	for _, bad := range []string{"<script", "<style", "javascript:", "onclick", "onerror", "<form", "<input", "foreignObject", "expression", "data:text"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(bad)) {
			t.Errorf("%q survived: %s", bad, out)
		}
	}
	if !strings.Contains(out, `style="color:red;--gap:4px;background:url(/img/a.png)"`) {
		t.Errorf("ordinary style dropped: %s", out)
	}
}

func TestSanitizeAttributes_HTMLSectionBodyIsTypeAware(t *testing.T) {
	in := json.RawMessage(`{"page_sections":[` +
		`{"id":"a","type":"html","content":{"body":"<section id=\"x\"><script>alert(1)</script><p onclick=\"y()\">hi</p></section>","note":"<b onclick=1>keep</b>"}},` +
		`{"id":"b","type":"content","content":{"body":"<section>stays</section>"}}]}`)
	out, err := sanitizeAttributes(in)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Sections []struct {
			Content map[string]string `json:"content"`
		} `json:"page_sections"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if got := v.Sections[0].Content["body"]; got != `<section id="x"><p>hi</p></section>` {
		t.Errorf("html body: %s", got)
	}
	if got := v.Sections[0].Content["note"]; got != `<b onclick=1>keep</b>` {
		t.Errorf("non-body field must be untouched: %s", got)
	}
	if got := v.Sections[1].Content["body"]; got != `<section>stays</section>` {
		t.Errorf("non-html section must be untouched: %s", got)
	}
}

func TestSanitizeAttributes_HTMLSectionCleanBodyIsByteIdentical(t *testing.T) {
	in := json.RawMessage(`{"page_sections":[{"type":"html","content":{"body":"<section class=\"a\"><p>hi</p></section>"}}]}`)
	out, _ := sanitizeAttributes(in)
	if string(out) != string(in) {
		t.Errorf("clean body re-marshalled: %s", out)
	}
}

func TestStyleAllowed_SharedCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/style-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Style string `json:"style"`
		OK    bool   `json:"ok"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := styleAllowed(c.Style); got != c.OK {
			t.Errorf("styleAllowed(%q) = %v, want %v", c.Style, got, c.OK)
		}
		// End to end through the policy: attribute survives iff allowed.
		out := sanitizeHTMLBody(`<div style="` + html.EscapeString(c.Style) + `">x</div>`)
		if has := strings.Contains(out, "style="); has != c.OK {
			t.Errorf("sanitizeHTMLBody style %q: attribute present=%v, want %v (%s)", c.Style, has, c.OK, out)
		}
	}
}

// Every style attribute in the real-world corpus must survive unchanged.
func TestSanitizeHTMLBody_CorpusStylesSurvive(t *testing.T) {
	raw, err := os.ReadFile("testdata/html-style-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []string
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 60 {
		t.Fatalf("want 60 samples, got %d", len(samples))
	}
	for i, in := range samples {
		want := styleAttrs(t, in)
		got := styleAttrs(t, sanitizeHTMLBody(in))
		if len(want) == 0 {
			continue
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("sample %d: style attributes changed\n in: %q\nout: %q", i, want, got)
		}
	}
}

func styleAttrs(t *testing.T, src string) []string {
	t.Helper()
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(src), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for _, a := range n.Attr {
			if a.Key == "style" {
				out = append(out, a.Val)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

func TestHTMLSectionTypeAliases(t *testing.T) {
	t.Setenv("HTML_SECTION_TYPE_ALIASES", " legacy_a , legacy_b")
	in := json.RawMessage(`{"page_sections":[` +
		`{"type":"legacy_a","content":{"body":"<p onclick=\"x()\">a</p>"}},` +
		`{"type":"legacy_b","content":{"body":"<script>1</script><p>b</p>"}},` +
		`{"type":"other","content":{"body":"<p onclick=\"x()\">c</p>"}}]}`)

	out, err := sanitizeAttributes(in)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); strings.Contains(s, "script") || strings.Count(s, "onclick") != 1 {
		t.Errorf("save path: aliased types must be sanitised, others untouched: %s", s)
	}

	out, n, err := SanitizeHTMLSections(in)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); n != 2 || strings.Contains(s, "script") || strings.Count(s, "onclick") != 1 {
		t.Errorf("sanitize-html-sections: n=%d out=%s", n, s)
	}
}

func TestHTMLSectionTypeAliases_DefaultEmpty(t *testing.T) {
	t.Setenv("HTML_SECTION_TYPE_ALIASES", "")
	in := json.RawMessage(`{"page_sections":[{"type":"legacy_a","content":{"body":"<p onclick=\"x()\">a</p>"}}]}`)
	if _, n, _ := SanitizeHTMLSections(in); n != 0 {
		t.Errorf("no aliases configured, nothing should change; n=%d", n)
	}
}
