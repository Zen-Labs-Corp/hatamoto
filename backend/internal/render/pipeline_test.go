package render

import (
	"html/template"
	"strings"
	"testing"
	"testing/fstest"
)

func pipelineTheme(t *testing.T) *Theme {
	t.Helper()
	fsys := fstest.MapFS{
		"sections/faq.html": mapFile(`{{define "section-faq"}}{{end}}`),
		"schemas/faq.json": mapFile(`{"type":"object","properties":{
			"heading":{"type":"string","x-hm-alias":["headline"],"x-hm-field":"plaintext"},
			"faqs":{"type":"array","x-hm-alias":["items"],"items":{"type":"object","properties":{
				"question":{"type":"string","x-hm-field":"plaintext"},
				"answer":{"type":"string","x-hm-field":"richtext"}}}}}}`),
		"sections/html.html":    mapFile(`{{define "section-html"}}{{end}}`),
		"schemas/html.json":     mapFile(`{"type":"object","properties":{"body":{"type":"string","x-hm-field":"html"}}}`),
		"sections/content.html": mapFile(`{{define "section-content"}}{{end}}`),
		"schemas/content.json":  mapFile(`{"type":"object","properties":{"body":{"type":"string","x-hm-field":"richtext"}}}`),
	}
	th, err := LoadTheme(Chain{fsys})
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestPipelineVisibilityAndOrder(t *testing.T) {
	t.Parallel()
	th := pipelineTheme(t)
	got := prepareSections(th, []Section{
		{ID: "c", Type: "faq", SortOrder: 3},
		{ID: "hidden", Type: "faq", SortOrder: 0, IsVisible: new(false)},
		{ID: "a", Type: "faq", SortOrder: 1},
		{ID: "b", Type: "faq", SortOrder: 1, IsVisible: new(true)},
	}, false)
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "a,b,c" {
		t.Fatalf("order: %v", ids)
	}
}

func TestPipelineUnknownType(t *testing.T) {
	t.Parallel()
	th := pipelineTheme(t)
	got := prepareSections(th, []Section{{ID: "x", Type: `evil--><script>`}}, false)
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	c := string(got[0].CommentHTML())
	if c != "<!-- unknown section type: evilscript -->" {
		t.Fatalf("comment: %q", c)
	}
	if strings.Count(c, "--") != 2 || strings.ContainsAny(strings.TrimSuffix(strings.TrimPrefix(c, "<!--"), "-->"), "<>") {
		t.Fatalf("comment not safe: %q", c)
	}
	got = prepareSections(th, []Section{{Type: "nope"}}, false)
	if string(got[0].CommentHTML()) != "<!-- unknown section type: nope -->" {
		t.Fatalf("got %q", got[0].CommentHTML())
	}
}

func TestPipelineAliasNormalisation(t *testing.T) {
	t.Parallel()
	th := pipelineTheme(t)
	tests := []struct {
		name        string
		content     map[string]any
		wantHeading string
	}{
		{"alias copied when canonical missing", map[string]any{"headline": "Legacy"}, "Legacy"},
		{"alias copied when canonical blank", map[string]any{"heading": "", "headline": "Legacy"}, "Legacy"},
		{"canonical wins", map[string]any{"heading": "Canon", "headline": "Legacy"}, "Canon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := prepareSections(th, []Section{{Type: "faq", Content: tt.content}}, false)
			if got[0].Content["heading"] != tt.wantHeading {
				t.Fatalf("heading=%v", got[0].Content["heading"])
			}
		})
	}
	in := map[string]any{"items": []any{map[string]any{"question": "q", "answer": "<p>a</p>"}}}
	got := prepareSections(th, []Section{{Type: "faq", Content: in}}, false)
	faqs, ok := got[0].Content["faqs"].([]any)
	if !ok || len(faqs) != 1 {
		t.Fatalf("faqs alias not copied: %v", got[0].Content)
	}
	if _, ok := faqs[0].(map[string]any)["answer"].(template.HTML); !ok {
		t.Fatalf("nested richtext not wrapped: %T", faqs[0].(map[string]any)["answer"])
	}
	if _, mutated := in["faqs"]; mutated {
		t.Fatal("input content was mutated")
	}
}

func TestPipelineSanitisesAndStripsForgedMarkers(t *testing.T) {
	t.Parallel()
	th := pipelineTheme(t)
	got := prepareSections(th, []Section{
		{Type: "html", Content: map[string]any{"body": `<div data-hm-section="evil" data-hm-field="body" style="color:red"><script>alert(1)</script>ok</div>`}},
		{Type: "faq", Content: map[string]any{"faqs": []any{map[string]any{"question": `<b>plain</b>`, "answer": `<p data-hm-field="x" onclick="y()">a</p>`}}}},
	}, false)
	body, ok := got[0].Content["body"].(template.HTML)
	if !ok {
		t.Fatalf("html body type %T", got[0].Content["body"])
	}
	for _, bad := range []string{"data-hm-", "<script", "alert"} {
		if strings.Contains(string(body), bad) {
			t.Errorf("html body contains %q: %s", bad, body)
		}
	}
	if !strings.Contains(string(body), "style=") || !strings.Contains(string(body), "ok") {
		t.Errorf("html body lost legit content: %s", body)
	}
	item := got[1].Content["faqs"].([]any)[0].(map[string]any)
	if q, ok := item["question"].(string); !ok || q != `<b>plain</b>` {
		t.Errorf("plaintext must stay a plain string (auto-escaped later): %#v", item["question"])
	}
	if a := string(item["answer"].(template.HTML)); a != "<p>a</p>" {
		t.Errorf("answer: %q", a)
	}
}

func TestPipelineBlankContentBody(t *testing.T) {
	t.Parallel()
	th := pipelineTheme(t)
	tests := []struct {
		name string
		body any
		keep bool
	}{
		{"missing", nil, false},
		{"empty", "", false},
		{"tags only", "<p> &nbsp; </p>", false},
		{"text", "<p>hi</p>", true},
		{"image only", `<img src="/a.png" alt="">`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := map[string]any{"heading": "H"}
			if tt.body != nil {
				c["body"] = tt.body
			}
			got := prepareSections(th, []Section{{Type: "content", Content: c}}, false)
			if (len(got) == 1) != tt.keep {
				t.Fatalf("keep=%v got %d sections", tt.keep, len(got))
			}
		})
	}
	// Blank rule applies to "content" only.
	if got := prepareSections(th, []Section{{Type: "faq"}}, false); len(got) != 1 {
		t.Fatal("faq without body must be kept")
	}
}
