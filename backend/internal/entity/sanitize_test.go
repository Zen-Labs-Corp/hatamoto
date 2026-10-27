package entity

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/microcosm-cc/bluemonday"
)

func TestSanitizeAttributes_StripsXSS(t *testing.T) {
	in := json.RawMessage(`{"content":"<p>ok</p><script>alert(1)</script><img src=x onerror=alert(1)>","title":"keep"}`)
	out, err := sanitizeAttributes(in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "<script") {
		t.Errorf("script survived: %s", s)
	}
	if strings.Contains(s, "onerror") {
		t.Errorf("event handler survived: %s", s)
	}
	// Non-content keys must be preserved untouched.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if string(obj["title"]) != `"keep"` {
		t.Errorf("title mutated: %s", obj["title"])
	}
}

func TestSanitizeAttributes_DropsStyleAndJSHref(t *testing.T) {
	in := json.RawMessage(`{"content":"<p style=\"color:red\">x</p><a href=\"javascript:alert(1)\">y</a>"}`)
	out, _ := sanitizeAttributes(in)
	s := string(out)
	if strings.Contains(s, "style=") {
		t.Errorf("style survived: %s", s)
	}
	if strings.Contains(strings.ToLower(s), "javascript:") {
		t.Errorf("javascript: href survived: %s", s)
	}
}

func TestSanitizeAttributes_IframeAllowlist(t *testing.T) {
	in := json.RawMessage(`{"content":"<iframe src=\"https://www.youtube.com/embed/abc\"></iframe><iframe src=\"https://evil.com/x\"></iframe>"}`)
	out, _ := sanitizeAttributes(in)
	s := string(out)
	if !strings.Contains(s, "youtube.com/embed/abc") {
		t.Errorf("youtube iframe should survive: %s", s)
	}
	if strings.Contains(s, "evil.com") {
		t.Errorf("evil iframe should be stripped: %s", s)
	}
}

func TestSanitizeAttributes_PreservesLinkerBridgeAndRoundTripsCleanContent(t *testing.T) {
	// Clean editor content, incl. the v2 linker bridge attributes, must survive.
	content := `<h2>Title</h2><p>Some <strong>bold</strong> and <a href="/blog/seo" data-entity-id="abc-123" class="entity-link">a link</a>.</p><ul><li>one</li></ul>`
	raw, _ := json.Marshal(map[string]string{"content": content})
	out, err := sanitizeAttributes(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Decode the content field back to HTML (raw JSON escapes '<' to \u003c).
	var obj map[string]string
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	s := obj["content"]
	for _, want := range []string{"<h2>", "<strong>", `data-entity-id="abc-123"`, `class="entity-link"`, `href="/blog/seo"`, "<li>"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected clean content to retain %q; got %s", want, s)
		}
	}
}

func TestSanitizeAttributes_NestedSectionContentSanitized(t *testing.T) {
	// Section HTML nested inside arrays/objects must also be sanitized.
	in := json.RawMessage(`{"sections":[{"type":"text","content":"<p>ok</p><script>alert(1)</script>"}],"count":3}`)
	out, err := sanitizeAttributes(in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "script") {
		t.Errorf("nested script survived: %s", s)
	}
	if !strings.Contains(s, `"count":3`) {
		t.Errorf("sibling numeric key must be preserved exactly: %s", s)
	}
}

func TestSanitizeAttributes_KeepsTelLinks(t *testing.T) {
	in := json.RawMessage(`{"content":"<a href=\"tel:+15551234\">call</a>"}`)
	out, _ := sanitizeAttributes(in)
	if !strings.Contains(string(out), "tel:+15551234") {
		t.Errorf("tel link should survive: %s", out)
	}
}

func TestSanitizeAttributes_NoContentKeyUnchanged(t *testing.T) {
	in := json.RawMessage(`{"title":"hi","count":3}`)
	out, err := sanitizeAttributes(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("blob without content should be unchanged: %s", out)
	}
}

func TestIframeSrcAllowlistFromEnv(t *testing.T) {
	ok := func(re *regexp.Regexp, src string) bool { return re.MatchString(src) }
	def := newIframeSrcRE("")
	for src, want := range map[string]bool{
		"https://www.youtube.com/embed/abc":                   true,
		"https://youtube-nocookie.com/embed/abc":              true,
		"https://player.vimeo.com/video/1":                    true,
		"https://www.youtube.com/watch?v=1":                   false, // not the embed path
		"http://www.youtube.com/embed/abc":                    false,
		"https://www.youtube.com.evil.test/embed/abc":         false,
		"https://www.youtube.com@evil.test/embed/abc":         false,
		"https://www.youtube.com:8443/embed/abc":              false,
		"https://evil.test/?u=https://www.youtube.com/embed/": false,
		"https://maps.example.test/x":                         false,
	} {
		if got := ok(def, src); got != want {
			t.Errorf("default %q = %v, want %v", src, got, want)
		}
	}
	// A configured list replaces the defaults; unknown hosts embed at any path.
	custom := newIframeSrcRE(" Maps.Example.test , player.vimeo.com,bad host,https://x.test,,*.evil.test")
	for src, want := range map[string]bool{
		"https://maps.example.test/anything?x=1": true,
		"https://maps.example.test.evil.test/x":  false,
		"https://player.vimeo.com/video/1":       true,
		"https://player.vimeo.com/other":         false, // stock host keeps its path
		"https://www.youtube.com/embed/abc":      false, // replaced
		"https://x.test/a":                       false, // not a host: ignored
		"https://a.evil.test/x":                  false,
	} {
		if got := ok(custom, src); got != want {
			t.Errorf("custom %q = %v, want %v", src, got, want)
		}
	}
	// Nothing usable in the variable: defaults, never an open policy.
	if got := newIframeSrcRE("nonsense, ,*"); got.String() != def.String() {
		t.Errorf("invalid list must fall back to the defaults: %s", got)
	}
}

func TestPoliciesHonourEmbedHosts(t *testing.T) {
	// The process-wide allowlist is built once from the environment; build the
	// policies against this test's list directly.
	re := newIframeSrcRE("maps.example.test")
	for name, p := range map[string]*bluemonday.Policy{"rich": newRichTextPolicy(re), "html": newHTMLSectionPolicy(re)} {
		in := `<iframe src="https://maps.example.test/e/1"></iframe><iframe src="https://www.youtube.com/embed/a"></iframe>`
		out := p.Sanitize(in)
		if !strings.Contains(out, "maps.example.test/e/1") || strings.Contains(out, "youtube") {
			t.Errorf("%s policy: %s", name, out)
		}
	}
}

func TestSanitizeHTMLSectionBodyKeepsSectionAttributes(t *testing.T) {
	in := `<div class="c" id="x" role="note" style="color:red" aria-label="l" data-k="v" data-hm-section="s" onclick="a()"><script>x()</script><details open><summary>s</summary>t</details></div>`
	out := SanitizeHTMLSectionBody(in)
	for _, want := range []string{`class="c"`, `id="x"`, `role="note"`, `style="color:red"`, `aria-label="l"`, `data-k="v"`, `<details open="">`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	for _, bad := range []string{"onclick", "script", "data-hm-section"} {
		if strings.Contains(out, bad) {
			t.Errorf("kept %s in %s", bad, out)
		}
	}
}
