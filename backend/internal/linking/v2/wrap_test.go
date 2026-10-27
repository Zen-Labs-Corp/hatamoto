package v2

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestWrapAll covers the pure-function wrap algorithm. The /linking/insert
// HTTP handler wires directly through WrapAll, so these scenarios lock the
// algorithmic contract for every caller. (Historically the v2 backfill
// script also routed through here; backfill ran once at the v1→v2 cutover
// and the tool has been retired.)
func TestWrapAll(t *testing.T) {
	t.Parallel()

	target1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	tests := []struct {
		name         string
		content      string
		specs        []LinkSpec
		wantInserted int
		wantSkips    map[string]int
		assertHTML   func(t *testing.T, got string)
	}{
		{
			name:         "empty content marks anchor as no_match",
			content:      "",
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "SEO", CanonicalURL: "/blog/seo"}},
			wantInserted: 0,
			wantSkips:    map[string]int{SkipNoMatch: 1},
		},
		{
			name:         "simple wrap inserts anchor with all attributes",
			content:      `<p>We offer SEO services to clients.</p>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "SEO services", CanonicalURL: "/blog/seo"}},
			wantInserted: 1,
			wantSkips:    map[string]int{},
			assertHTML: func(t *testing.T, got string) {
				if !strings.Contains(got, `href="/blog/seo"`) {
					t.Errorf("missing href: %q", got)
				}
				if !strings.Contains(got, `data-entity-id="11111111-1111-1111-1111-111111111111"`) {
					t.Errorf("missing data-entity-id: %q", got)
				}
				if !strings.Contains(got, `class="entity-link"`) {
					t.Errorf("missing entity-link class: %q", got)
				}
				if !strings.Contains(got, `>SEO services</a>`) {
					t.Errorf("anchor text not preserved verbatim: %q", got)
				}
			},
		},
		{
			name:         "multiple occurrences only wrap the first",
			content:      `<p>SEO services help. Our SEO services are top notch.</p>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "SEO services", CanonicalURL: "/blog/seo"}},
			wantInserted: 1,
			wantSkips:    map[string]int{},
			assertHTML: func(t *testing.T, got string) {
				if strings.Count(got, `data-entity-id=`) != 1 {
					t.Errorf("expected exactly 1 anchor, got %d: %q",
						strings.Count(got, `data-entity-id=`), got)
				}
			},
		},
		{
			name:         "heading match is skipped as in_heading",
			content:      `<h2>Our SEO Services</h2><p>contact us</p>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "SEO Services", CanonicalURL: "/blog/seo"}},
			wantInserted: 0,
			wantSkips:    map[string]int{SkipInHeading: 1},
		},
		{
			name:         "code block match is skipped as in_code",
			content:      `<pre><code>SELECT * FROM seo</code></pre>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "seo", CanonicalURL: "/blog/seo"}},
			wantInserted: 0,
			wantSkips:    map[string]int{SkipInCode: 1},
		},
		{
			name:         "existing anchor wrapping the same text is skipped",
			content:      `<p>See <a href="/foo">SEO</a> page.</p>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "SEO", CanonicalURL: "/blog/seo"}},
			wantInserted: 0,
			wantSkips:    map[string]int{SkipAlreadyLinked: 1},
		},
		{
			name: "existing entity link to same target anywhere skips even with available text",
			content: `<p>see <a data-entity-id="11111111-1111-1111-1111-111111111111" href="/foo">below</a>` +
				` for SEO services.</p>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "SEO services", CanonicalURL: "/blog/seo"}},
			wantInserted: 0,
			wantSkips:    map[string]int{SkipAlreadyLinked: 1},
		},
		{
			name:         "HTML entity in stored content matches plain anchor",
			content:      `<p>shop at M&amp;S for groceries.</p>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "M&S", CanonicalURL: "/blog/ms"}},
			wantInserted: 1,
			wantSkips:    map[string]int{},
			assertHTML: func(t *testing.T, got string) {
				if !strings.Contains(got, `href="/blog/ms"`) {
					t.Errorf("expected href in output: %q", got)
				}
				// x/net/html re-escapes & in text content on render. The
				// inserted anchor's TEXT child stores M&S literally; render
				// will encode it back to M&amp;S, which is what we want.
				if !strings.Contains(got, ">M&amp;S</a>") {
					t.Errorf("expected anchor text M&S (encoded) in output: %q", got)
				}
			},
		},
		{
			name:         "case-insensitive match preserves the original text casing",
			content:      `<p>Our seo services rock.</p>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "SEO services", CanonicalURL: "/blog/seo"}},
			wantInserted: 1,
			wantSkips:    map[string]int{},
			assertHTML: func(t *testing.T, got string) {
				if !strings.Contains(got, `>seo services</a>`) {
					t.Errorf("expected lowercase anchor text preserved, got %q", got)
				}
			},
		},
		{
			name:         "whole-word boundary rejects SEO inside SEOpedia",
			content:      `<p>visit SEOpedia for definitions.</p>`,
			specs:        []LinkSpec{{TargetEntityID: target1, AnchorText: "SEO", CanonicalURL: "/blog/seo"}},
			wantInserted: 0,
			wantSkips:    map[string]int{SkipNoMatch: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := WrapAll(tt.content, tt.specs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Inserted != tt.wantInserted {
				t.Errorf("Inserted = %d, want %d (skips=%+v, html=%q)",
					res.Inserted, tt.wantInserted, res.Skips, res.NewHTML)
			}
			if !equalSkipMaps(res.Skips, tt.wantSkips) {
				t.Errorf("Skips = %+v, want %+v", res.Skips, tt.wantSkips)
			}
			if tt.assertHTML != nil {
				tt.assertHTML(t, res.NewHTML)
			}
		})
	}
}

func equalSkipMaps(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
