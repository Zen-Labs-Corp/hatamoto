package v2

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseLinks(t *testing.T) {
	t.Parallel()

	target1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	target2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	tests := []struct {
		name     string
		input    string
		expected []ParsedLink
	}{
		{
			name:     "empty content yields no links",
			input:    "",
			expected: nil,
		},
		{
			name:     "whitespace-only content yields no links",
			input:    "   \n\t",
			expected: nil,
		},
		{
			name:  "single link parses",
			input: `<p>Our <a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/seo">SEO services</a> help.</p>`,
			expected: []ParsedLink{
				{
					TargetEntityID:  target1,
					AnchorText:      "SEO services",
					Href:            "/blog/seo",
					OccurrenceIndex: 0,
				},
			},
		},
		{
			name: "two links to same target increment occurrence",
			input: `<p>First <a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/seo">link</a>.
			Second <a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/seo">link</a>.</p>`,
			expected: []ParsedLink{
				{TargetEntityID: target1, AnchorText: "link", Href: "/blog/seo", OccurrenceIndex: 0},
				{TargetEntityID: target1, AnchorText: "link", Href: "/blog/seo", OccurrenceIndex: 1},
			},
		},
		{
			name:  "section href parses",
			input: `<a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/foo">post</a>`,
			expected: []ParsedLink{
				{TargetEntityID: target1, AnchorText: "post", Href: "/blog/foo", OccurrenceIndex: 0},
			},
		},
		{
			name:  "uppercase data-entity-id attribute is normalised by x/net/html",
			input: `<a DATA-ENTITY-ID="11111111-1111-1111-1111-111111111111" href="/blog/seo">SEO</a>`,
			expected: []ParsedLink{
				{TargetEntityID: target1, AnchorText: "SEO", Href: "/blog/seo", OccurrenceIndex: 0},
			},
		},
		{
			name:  "deeply nested anchor still parses",
			input: `<div><section><p>Outer <span><a data-entity-id="22222222-2222-2222-2222-222222222222" href="/blog/ppc">PPC</a></span> wins.</p></section></div>`,
			expected: []ParsedLink{
				{TargetEntityID: target2, AnchorText: "PPC", Href: "/blog/ppc", OccurrenceIndex: 0},
			},
		},
		{
			name:  "anchor without data-entity-id is skipped",
			input: `<a href="/blog/seo">plain link</a> and <a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/ppc">tagged</a>`,
			expected: []ParsedLink{
				{TargetEntityID: target1, AnchorText: "tagged", Href: "/blog/ppc", OccurrenceIndex: 0},
			},
		},
		{
			name:     "anchor with non-uuid data-entity-id is skipped",
			input:    `<a data-entity-id="not-a-uuid" href="/blog/seo">link</a>`,
			expected: nil,
		},
		{
			name:  "anchor inside <code> is still parsed (skip-list is for backfill, not indexer)",
			input: `<pre><code>Example: <a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/seo">SEO</a></code></pre>`,
			expected: []ParsedLink{
				{TargetEntityID: target1, AnchorText: "SEO", Href: "/blog/seo", OccurrenceIndex: 0},
			},
		},
		{
			name:  "anchor inside heading is still parsed",
			input: `<h1>Why <a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/seo">SEO</a> wins</h1>`,
			expected: []ParsedLink{
				{TargetEntityID: target1, AnchorText: "SEO", Href: "/blog/seo", OccurrenceIndex: 0},
			},
		},
		{
			name:  "anchor with formatted inner text flattens",
			input: `<a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/seo"><em>SEO</em> <strong>services</strong></a>`,
			expected: []ParsedLink{
				{TargetEntityID: target1, AnchorText: "SEO services", Href: "/blog/seo", OccurrenceIndex: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLinks(tt.input)
			if err != nil {
				t.Fatalf("ParseLinks() unexpected error: %v", err)
			}
			if len(got) != len(tt.expected) {
				t.Fatalf("ParseLinks() returned %d, want %d: got %+v", len(got), len(tt.expected), got)
			}
			for i, want := range tt.expected {
				if got[i].TargetEntityID != want.TargetEntityID {
					t.Errorf("[%d] TargetEntityID = %s, want %s", i, got[i].TargetEntityID, want.TargetEntityID)
				}
				if got[i].AnchorText != want.AnchorText {
					t.Errorf("[%d] AnchorText = %q, want %q", i, got[i].AnchorText, want.AnchorText)
				}
				if got[i].Href != want.Href {
					t.Errorf("[%d] Href = %q, want %q", i, got[i].Href, want.Href)
				}
				if got[i].OccurrenceIndex != want.OccurrenceIndex {
					t.Errorf("[%d] OccurrenceIndex = %d, want %d", i, got[i].OccurrenceIndex, want.OccurrenceIndex)
				}
			}
		})
	}
}

// Sanity check on the unexported rewriteHrefsForEntity DOM walker. The
// rewriter integration test (rewriter_test.go) exercises it end-to-end
// through a real DB transaction; this lightweight test catches DOM-walk
// regressions without DB setup.
func TestRewriteHrefsForEntity_PreservesUntaggedAnchors(t *testing.T) {
	t.Parallel()

	target := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	in := `<p>Tagged <a data-entity-id="11111111-1111-1111-1111-111111111111" href="/blog/old-seo">SEO</a> ` +
		`and plain <a href="/blog/foo">post</a> together.</p>`

	out, rewrites, err := rewriteHrefsForEntity(in, target, "/blog/seo")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(rewrites) != 1 {
		t.Fatalf("expected 1 rewrite, got %d", len(rewrites))
	}
	if rewrites[0].oldHref != "/blog/old-seo" || rewrites[0].newHref != "/blog/seo" {
		t.Errorf("rewrite record wrong: %+v", rewrites[0])
	}
	if rewrites[0].anchor != "SEO" {
		t.Errorf("anchor = %q, want %q", rewrites[0].anchor, "SEO")
	}
	// The plain /blog/foo anchor must survive untouched.
	if !contains(out, `href="/blog/foo"`) {
		t.Errorf("plain href was clobbered; got %q", out)
	}
	// The tagged anchor must have the new href and keep its data-entity-id.
	if !contains(out, `href="/blog/seo"`) {
		t.Errorf("tagged href not rewritten; got %q", out)
	}
	if !contains(out, `data-entity-id="11111111-1111-1111-1111-111111111111"`) {
		t.Errorf("data-entity-id lost; got %q", out)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
