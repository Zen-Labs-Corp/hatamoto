package entity

import (
	"encoding/json"
	"errors"
	"hatamoto.dev/core/internal/builder"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestAllowedInlineHrefFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/inline_href_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Href string `json:"href"`
		OK   bool   `json:"ok"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := AllowedInlineHref(c.Href); got != c.OK {
			t.Errorf("AllowedInlineHref(%q)=%v want %v", c.Href, got, c.OK)
		}
	}
}

func TestSanitizeInlineHTML(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "hello", "hello"},
		{"escapes", "a &amp; b &lt;i&gt;", "a &amp; b &lt;i&gt;"},
		{"marks", "<strong>a</strong><em>b</em><b>c</b><i>d</i><u>e</u><s>f</s><code>g</code>a<br>b",
			"<strong>a</strong><em>b</em><b>c</b><i>d</i><u>e</u><s>f</s><code>g</code>a<br>b"},
		{"drops elements", `<p>x</p><span class="c" style="color:red">y</span><h1 id=a>z</h1><img src=x onerror=alert(1)>`, "xyz"},
		{"script", `a<script>alert(1)</script>b<style>p{}</style>`, "ab"},
		{"link", `<a href="https://a.com/x">go</a>`, `<a href="https://a.com/x">go</a>`},
		{"link relative blank", `<a href="/x" target="_blank">go</a>`, `<a href="/x" target="_blank" rel="noopener noreferrer">go</a>`},
		{"input rel dropped", `<a href="/x" rel="opener">go</a>`, `<a href="/x">go</a>`},
		{"target other", `<a href="/x" target="_top">go</a>`, `<a href="/x">go</a>`},
		{"js href", `<a href="javascript:alert(1)">go</a>`, "go"},
		{"entity js href", `<a href="java&#9;script:alert(1)">go</a>`, "go"},
		{"data href", `<a href="data:text/html,x"><b>go</b></a>`, "<b>go</b>"},
		{"vbscript", `<a href="vbscript:x">go</a>`, "go"},
		{"protocol relative", `<a href="//evil.test">go</a>`, "go"},
		{"no href", `<a target="_blank">go</a>`, "go"},
		{"event handler", `<a href="/x" onclick="x()" class="k">go</a>`, `<a href="/x">go</a>`},
		{"hm attr", `<strong data-hm-x="1">a</strong>`, "<strong>a</strong>"},
		{"unclosed", `<a href="/x"><strong>go`, `<a href="/x"><strong>go</strong></a>`},
		{"stray end", `a</strong></a>b`, "ab"},
		{"only unsafe", `<script>x</script>`, ""},
	}
	for _, tt := range tests {
		got := SanitizeInlineHTML(tt.in)
		if got != tt.want {
			t.Errorf("%s: SanitizeInlineHTML(%q)=%q want %q", tt.name, tt.in, got, tt.want)
		}
		if again := SanitizeInlineHTML(got); again != got {
			t.Errorf("%s: not idempotent: %q -> %q", tt.name, got, again)
		}
	}
}

func TestSanitizeInlineHTMLBalanced(t *testing.T) {
	got := SanitizeInlineHTML(`<em><a href="/x">a</em>b`)
	if strings.Count(got, "<a") != strings.Count(got, "</a>") || strings.Count(got, "<em>") != strings.Count(got, "</em>") {
		t.Errorf("unbalanced: %q", got)
	}
}

type inlineCase struct {
	Name, In, Canonical string
}

func readInlineCases(t *testing.T) []inlineCase {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/inline/canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []inlineCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// The canonical inline form is the shared fixture of Go, the bridge and the inspector editor: sanitising
// the canonical form changes nothing, so a save never differs from what the editors sent (no adopt).
func TestSanitizeInlineCanonicalFixture(t *testing.T) {
	for _, c := range readInlineCases(t) {
		if got := SanitizeInlineHTML(c.In); got != c.Canonical {
			t.Errorf("%s: SanitizeInlineHTML(%q)=%q want %q", c.Name, c.In, got, c.Canonical)
		}
		if got := SanitizeInlineHTML(c.Canonical); got != c.Canonical {
			t.Errorf("%s: canonical is not a fixed point: %q -> %q", c.Name, c.Canonical, got)
		}
	}
}

// A sanitised html over the schema limit is refused, not stored (the doc would stop parsing).
func TestCheckSanitisedLength(t *testing.T) {
	long := "<strong>q</strong>" + strings.Repeat(`say "hi" `, 2000) // 18,018 canonical characters
	if got := SanitizeInlineHTML(long); len(got) != len(long) {
		t.Fatalf("canonical input changed length: %d -> %d", len(long), len(got))
	}
	over := strings.Repeat("<a href=/x>", 1818) // grows once the links are closed
	for _, tc := range []struct {
		html string
		bad  bool
	}{{long, false}, {over, true}} {
		doc := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "h", T: "heading", P: map[string]any{"html": tc.html}}}}
		SanitizeDocComponents(doc)
		err := CheckSanitisedLength(doc)
		if (err != nil) != tc.bad || (err != nil && !errors.Is(err, ErrInvalidDoc)) {
			t.Errorf("len %d: err=%v want bad=%v", len(tc.html), err, tc.bad)
		}
	}
}

// richtext and html carry their own (100,000) schema limit: a long article saves.
func TestLongRichTextSavesAndOverLimitFails(t *testing.T) {
	long := "<p>" + strings.Repeat("word ", 5000) + "</p>" // 25,007 characters
	in := `{"v":2,"nodes":[{"id":"r","t":"richtext","p":{"html":` + strconv.Quote(long) + `}}]}`
	out, err := NormalizeAttributes(nil, json.RawMessage(`{"page_doc":`+in+`}`))
	if err != nil || !strings.Contains(string(out), "word word") {
		t.Fatalf("25k richtext: %v", err)
	}
	doc := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "r", T: "richtext", P: map[string]any{"html": strings.Repeat("a", 100001)}}}}
	if err := CheckSanitisedLength(doc); !errors.Is(err, ErrInvalidDoc) || !strings.Contains(err.Error(), "limit is 100000") {
		t.Errorf("over 100k richtext: %v", err)
	}
}
