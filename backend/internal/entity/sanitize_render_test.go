package entity

import (
	"strings"
	"testing"
)

func TestStripHMAttrs(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"no markers", `<p class="a">x</p>`, `<p class="a">x</p>`},
		{"strips both", `<div data-hm-section="x" data-hm-field="y" id="k">t</div>`, `<div id="k">t</div>`},
		{"case insensitive", `<div DATA-HM-TYPE="x">t</div>`, `<div>t</div>`},
		{"keeps other data", `<div data-hm-a="1" data-foo="b">t</div>`, `<div data-foo="b">t</div>`},
		{"self closing", `<br data-hm-x="1"/>`, `<br/>`},
		{"text mentioning prefix", `<p>data-hm-section</p>`, `<p>data-hm-section</p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := stripHMAttrs(tt.in); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeHTMLSectionBody(t *testing.T) {
	t.Parallel()
	in := `<div data-hm-section="x" data-hm-field="y" data-keep="1" style="color:red"><script>alert(1)</script><details open><summary>s</summary>b</details></div>`
	got := SanitizeHTMLSectionBody(in)
	for _, bad := range []string{"data-hm-", "<script", "alert(1)"} {
		if strings.Contains(got, bad) {
			t.Errorf("output still contains %q: %s", bad, got)
		}
	}
	for _, keep := range []string{"data-keep", "style=", "<details", "<summary>"} {
		if !strings.Contains(got, keep) {
			t.Errorf("output lost %q: %s", keep, got)
		}
	}
}

func TestSanitizeRichText(t *testing.T) {
	t.Parallel()
	got := SanitizeRichText(`<p data-hm-field="x" onclick="a()">hi<script>x</script></p>`)
	if got != `<p>hi</p>` {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeSectionContent(t *testing.T) {
	t.Parallel()
	orig := map[string]any{
		"body":  `<p data-hm-x="1">b<script>x</script></p>`,
		"title": `<b>plain</b>`,
		"items": []any{map[string]any{"content": `<i data-hm-f="1">z</i><script>q</script>`}},
	}
	htmlOut := SanitizeSectionContent("html", orig)
	if got := htmlOut["body"].(string); got != `<p>b</p>` {
		t.Errorf("html body: %q", got)
	}
	if got := htmlOut["title"].(string); got != `<b>plain</b>` {
		t.Errorf("non-body string must be untouched: %q", got)
	}
	if got := htmlOut["items"].([]any)[0].(map[string]any)["content"].(string); strings.Contains(got, "script") || strings.Contains(got, "data-hm") {
		t.Errorf("content key not sanitized: %q", got)
	}
	// Non-html section: body is left alone, input is not mutated.
	faq := SanitizeSectionContent("faq", orig)
	if faq["body"].(string) != orig["body"].(string) {
		t.Errorf("non-html section body modified")
	}
	if !strings.Contains(orig["body"].(string), "script") {
		t.Errorf("input mutated")
	}
	if SanitizeSectionContent("html", nil) != nil {
		t.Errorf("nil content should stay nil")
	}
}
