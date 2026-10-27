package v2

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// rewriteHrefsInAttributes / rewriteSingleHrefInAttributes are the leaf-aware
// rewriters that fixed the bug where Industry-vertical entities (and any
// other entity whose links live inside page_sections rather than top-level
// `content`) silently no-op'd. These tests pin the fix.

func TestRewriteHrefsInAttributes_TopLevelContent(t *testing.T) {
	target := uuid.New()
	attrs := []byte(`{"content":"<p>Read our <a data-entity-id=\"` + target.String() + `\" href=\"/blog/old-seo\">SEO guide</a>.</p>"}`)

	newAttrs, recs, err := rewriteHrefsInAttributes(attrs, target, "/blog/seo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 rewrite, got %d", len(recs))
	}
	if !strings.Contains(string(newAttrs), `/blog/seo`) {
		t.Errorf("expected /blog/seo in output, got: %s", newAttrs)
	}
	if strings.Contains(string(newAttrs), `/blog/old-seo`) {
		t.Errorf("expected /blog/old-seo to be gone, got: %s", newAttrs)
	}
}

// The asbestos-removal-seo-services bug: empty top-level `content`,
// link lives inside a page_sections[*].content.body leaf.
func TestRewriteHrefsInAttributes_PageSectionBody(t *testing.T) {
	target := uuid.New()
	attrs := []byte(`{
		"content": "",
		"page_sections": [
			{"type": "content", "content": {"body": "<p>Need help? See <a data-entity-id=\"` + target.String() + `\" href=\"/blog/old-seo\">our SEO service</a>.</p>"}}
		]
	}`)

	newAttrs, recs, err := rewriteHrefsInAttributes(attrs, target, "/blog/seo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 rewrite, got %d (this is the asbestos bug if 0)", len(recs))
	}
	if !strings.Contains(string(newAttrs), `/blog/seo`) {
		t.Errorf("expected /blog/seo in output, got: %s", newAttrs)
	}
}

// FAQ answers, problem/solution descriptions, methodology body — every
// wrap-target leaf the indexer walks must also be rewritable.
func TestRewriteHrefsInAttributes_MultipleLeafKinds(t *testing.T) {
	target := uuid.New()
	// The HTML's inner quotes need backslash-escaping when interpolated into a
	// JSON string literal; build the tag with \" sequences.
	tag := func(href string) string {
		return `<a data-entity-id=\"` + target.String() + `\" href=\"` + href + `\">x</a>`
	}
	attrs := []byte(`{
		"page_sections": [
			{"type": "faq", "content": {"items": [{"question": "q", "answer": "` + tag("/blog/old-seo") + `"}]}},
			{"type": "problem_solution", "content": {"problemDescription": "` + tag("/blog/old-seo") + `", "solutionDescription": "` + tag("/blog/old-seo") + `"}},
			{"type": "content", "content": {"body": "` + tag("/blog/old-seo") + `"}}
		]
	}`)

	newAttrs, recs, err := rewriteHrefsInAttributes(attrs, target, "/blog/seo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 4 {
		t.Fatalf("expected 4 rewrites (4 wrap-target leaves), got %d", len(recs))
	}
	if strings.Contains(string(newAttrs), `/blog/old-seo`) {
		t.Errorf("expected /blog/old-seo to be gone everywhere, got: %s", newAttrs)
	}
}

func TestRewriteSingleHrefInAttributes_NthAcrossLeaves(t *testing.T) {
	target := uuid.New()
	tag := func(href string) string {
		return `<a data-entity-id=\"` + target.String() + `\" href=\"` + href + `\">x</a>`
	}
	// Three occurrences: occurrence 0 in body, occurrence 1 in body, occurrence 2 in answer.
	attrs := []byte(`{
		"page_sections": [
			{"type": "content", "content": {"body": "` + tag("/blog/old-seo") + ` and again ` + tag("/blog/old-seo") + `"}},
			{"type": "faq", "content": {"items": [{"question": "q", "answer": "` + tag("/blog/old-seo") + `"}]}}
		]
	}`)

	// Retarget occurrence 2 (the FAQ one) — first two should stay /blog/old-.
	newAttrs, rec, err := rewriteSingleHrefInAttributes(attrs, target, 2, "/blog/seo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec == nil {
		t.Fatalf("expected one record, got nil")
	}
	if rec.oldHref != "/blog/old-seo" || rec.newHref != "/blog/seo" {
		t.Errorf("unexpected record: %+v", rec)
	}
	out := string(newAttrs)
	// First two old hrefs survive (in the body section); the FAQ one was retargeted.
	if strings.Count(out, "/blog/old-seo") != 2 {
		t.Errorf("expected 2 surviving /blog/old-seo refs (body occurrences 0 and 1), got: %s", out)
	}
	if strings.Count(out, "/blog/seo") != 1 {
		t.Errorf("expected exactly 1 /blog/seo (FAQ occurrence 2), got: %s", out)
	}
}

func TestRewriteSingleHrefInAttributes_OutOfRange(t *testing.T) {
	target := uuid.New()
	attrs := []byte(`{"content":"<a data-entity-id=\"` + target.String() + `\" href=\"/blog/old-seo\">x</a>"}`)

	// Only one occurrence (index 0); ask for occurrence 5.
	out, rec, err := rewriteSingleHrefInAttributes(attrs, target, 5, "/blog/seo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected nil record for out-of-range index, got: %+v", rec)
	}
	if string(out) != string(attrs) {
		t.Errorf("expected unchanged attrs, got: %s", out)
	}
}

func TestRewriteSingleHrefInAttributes_OtherTargetsIgnored(t *testing.T) {
	target := uuid.New()
	other := uuid.New()
	attrs := []byte(`{"content":"` +
		`<a data-entity-id=\"` + other.String() + `\" href=\"/blog/old-x\">other</a> ` +
		`<a data-entity-id=\"` + target.String() + `\" href=\"/blog/old-seo\">target1</a>` +
		`"}`)

	// Occurrence 0 of target should rewrite target's link, NOT the other-target's.
	newAttrs, rec, err := rewriteSingleHrefInAttributes(attrs, target, 0, "/blog/seo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec == nil {
		t.Fatalf("expected rewrite, got nil")
	}
	out := string(newAttrs)
	if !strings.Contains(out, `/blog/seo`) {
		t.Errorf("expected /blog/seo to be present, got: %s", out)
	}
	if !strings.Contains(out, `/blog/old-x`) {
		t.Errorf("other-target href should be untouched, got: %s", out)
	}
}
