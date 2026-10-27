package v2

import (
	"encoding/json"
	"testing"
)

func TestPrefilterSafe(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"Google Ads":        true,
		"e-commerce SEO":    true,
		"SEO 2025":          true,
		"M&S":               false, // stored as &amp;
		`"quoted"`:          false, // JSON-escaped
		"50% off":           false, // ILIKE wildcard
		"snake_case":        false, // ILIKE wildcard
		"what's new":        false, // may be &#39;
		"Café":              true,
		"<b>":               false,
		"search visibility": true,
	}
	for in, want := range cases {
		if got := prefilterSafe(in); got != want {
			t.Errorf("prefilterSafe(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWithContentReplacesOnlyContent(t *testing.T) {
	t.Parallel()
	in := []byte(`{"content":"<p>old</p>","author":"jo","page_sections":[{"type":"text"}]}`)
	out, err := withContent(in, "<p>new</p>")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["content"] != "<p>new</p>" {
		t.Errorf("content = %v, want <p>new</p>", m["content"])
	}
	if m["author"] != "jo" {
		t.Errorf("author lost: %v", m["author"])
	}
	if _, ok := m["page_sections"]; !ok {
		t.Error("page_sections lost")
	}
}

func TestWithContentEmptyAttrs(t *testing.T) {
	t.Parallel()
	out, err := withContent(nil, "<p>x</p>")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"content":"\u003cp\u003ex\u003c/p\u003e"}` {
		t.Errorf("unexpected: %s", out)
	}
}
