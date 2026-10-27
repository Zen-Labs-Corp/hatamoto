package v2

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Section-shaped fixture mirroring real production page_sections rows
// (verified 2026-06-02 against id=01423072-de5e-4faa-b9ce-fa0ea460200b).
// Headlines / eyebrows / CTAs are present but must NOT be wrapped — the
// allowlist excludes them. Wrap targets in this fixture: full_description,
// page_sections[2].content.body, page_sections[3].content.items[*].answer.
const sectionFixture = `{
	"name": "BigCommerce Development",
	"meta_title": "BigCommerce Development | Example Co",
	"meta_description": "Build a fast, scalable BigCommerce store.",
	"full_description": "We help retailers migrate to BigCommerce while protecting their SEO rankings.",
	"page_sections": [
		{
			"id": "h1",
			"type": "hero",
			"sort_order": 0,
			"title": "Hero",
			"content": {
				"eyebrow": "BigCommerce experts",
				"headline": "Launch your BigCommerce store",
				"subheadline": "From migration to SEO, end-to-end."
			}
		},
		{
			"id": "p1",
			"type": "process_steps",
			"sort_order": 1,
			"title": "How we work",
			"content": {
				"heading": "Our process",
				"steps": [
					{"title": "Discovery", "description": "We audit your current store for SEO gaps."},
					{"title": "Build",     "description": "Custom themes and integrations."}
				]
			}
		},
		{
			"id": "c1",
			"type": "content",
			"sort_order": 2,
			"title": "Why BigCommerce",
			"content": {
				"headline": "Why BigCommerce",
				"body": "We pair BigCommerce with proven SEO services so launches don't lose traffic."
			}
		},
		{
			"id": "f1",
			"type": "faq",
			"sort_order": 3,
			"title": "FAQ",
			"content": {
				"heading": "Common questions",
				"items": [
					{"question": "How long does migration take?", "answer": "Most BigCommerce migrations finish inside 8 weeks."},
					{"question": "Will I lose SEO?", "answer": "No — our SEO services preserve rankings via 301 maps."}
				]
			}
		}
	]
}`

func TestWrapAllJSONB_WrapsInsideSectionBodyAndFAQ(t *testing.T) {
	t.Parallel()
	target := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	specs := []LinkSpec{{
		TargetEntityID: target,
		AnchorText:     "SEO services",
		CanonicalURL:   "/blog/old-seo",
	}}

	res, out, err := WrapAllJSONB([]byte(sectionFixture), specs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Inserted != 1 {
		t.Fatalf("Inserted=%d want 1; skips=%+v", res.Inserted, res.Skips)
	}

	// The wrap should land in the FIRST matching field in document order.
	// page_sections[2].content.body contains "SEO services" before
	// page_sections[3] items, so it wins.
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	body := drill(t, got, "page_sections", 2, "content", "body").(string)
	if !strings.Contains(body, `data-entity-id="11111111-1111-1111-1111-111111111111"`) {
		t.Errorf("expected wrap in page_sections[2].content.body, got %q", body)
	}
	if !strings.Contains(body, `href="/blog/old-seo"`) {
		t.Errorf("expected href=/blog/old-seo in body, got %q", body)
	}

	// The later FAQ answer that ALSO contains "SEO services" must NOT be
	// wrapped — first-occurrence-per-target semantics carry across fields.
	answer := drill(t, got, "page_sections", 3, "content", "items", 1, "answer").(string)
	if strings.Contains(answer, "data-entity-id") {
		t.Errorf("FAQ answer should not be wrapped (already-linked guard): %q", answer)
	}
}

func TestWrapAllJSONB_SkipsHeadlinesAndCTAs(t *testing.T) {
	t.Parallel()
	target := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	// "BigCommerce" appears only in the hero headline + page_sections titles
	// + section.title (sort_order:0 etc). None of those are wrap targets.
	specs := []LinkSpec{{
		TargetEntityID: target,
		AnchorText:     "Launch your BigCommerce store",
		CanonicalURL:   "/blog/old-bigcommerce",
	}}

	res, _, err := WrapAllJSONB([]byte(sectionFixture), specs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Inserted != 0 {
		t.Fatalf("expected skip, got Inserted=%d", res.Inserted)
	}
	if res.Skips[SkipNoMatch] != 1 {
		t.Errorf("expected no_match skip, got %+v", res.Skips)
	}
}

func TestWrapAllJSONB_WrapsTopLevelFullDescription(t *testing.T) {
	t.Parallel()
	target := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	specs := []LinkSpec{{
		TargetEntityID: target,
		AnchorText:     "SEO rankings",
		CanonicalURL:   "/blog/old-seo",
	}}

	res, out, err := WrapAllJSONB([]byte(sectionFixture), specs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Inserted != 1 {
		t.Fatalf("Inserted=%d want 1; skips=%+v", res.Inserted, res.Skips)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fd := got["full_description"].(string)
	if !strings.Contains(fd, `data-entity-id="33333333-3333-3333-3333-333333333333"`) {
		t.Errorf("expected wrap in full_description, got %q", fd)
	}
}

func TestWrapAllJSONB_AlreadyLinkedAcrossLeaves(t *testing.T) {
	t.Parallel()
	target := uuid.MustParse("44444444-4444-4444-4444-444444444444")

	// Fixture where the target is already linked in a heading-adjacent
	// content body. A new spec for the same target must skip with
	// already_linked, regardless of which leaf the prior link sits in.
	const fixture = `{
		"page_sections": [
			{"type": "content", "content": {"body": "We pair <a data-entity-id=\"44444444-4444-4444-4444-444444444444\" href=\"/blog/old-seo\">SEO services</a> with code."}},
			{"type": "faq", "content": {"items": [{"answer": "Our SEO services preserve rankings."}]}}
		]
	}`

	specs := []LinkSpec{{
		TargetEntityID: target,
		AnchorText:     "SEO services",
		CanonicalURL:   "/blog/old-seo",
	}}

	res, out, err := WrapAllJSONB([]byte(fixture), specs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Inserted != 0 {
		t.Fatalf("expected no insert (already linked elsewhere), got Inserted=%d", res.Inserted)
	}
	if res.Skips[SkipAlreadyLinked] != 1 {
		t.Errorf("expected already_linked skip, got %+v", res.Skips)
	}
	// Output should be the original bytes (no change marker).
	if !strings.Contains(string(out), `"items"`) {
		t.Errorf("output should preserve original JSON shape")
	}
}

func TestWrapAllJSONB_EmptyAnchor(t *testing.T) {
	t.Parallel()
	specs := []LinkSpec{{
		TargetEntityID: uuid.New(),
		AnchorText:     "   ",
		CanonicalURL:   "/blog/old-seo",
	}}
	res, _, err := WrapAllJSONB([]byte(sectionFixture), specs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Skips[SkipEmptyAnchor] != 1 {
		t.Errorf("expected empty_anchor skip, got %+v", res.Skips)
	}
}

func TestWrapAllJSONB_MultipleSpecsDistributeAcrossLeaves(t *testing.T) {
	t.Parallel()
	// Two distinct targets, two distinct anchors — both should land,
	// in different leaves.
	target1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	target2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	specs := []LinkSpec{
		{TargetEntityID: target1, AnchorText: "SEO services", CanonicalURL: "/blog/old-seo"},
		{TargetEntityID: target2, AnchorText: "BigCommerce migrations", CanonicalURL: "/blog/old-bigcommerce"},
	}

	res, out, err := WrapAllJSONB([]byte(sectionFixture), specs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Inserted != 2 {
		t.Fatalf("Inserted=%d want 2; skips=%+v", res.Inserted, res.Skips)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	body := drill(t, got, "page_sections", 2, "content", "body").(string)
	if !strings.Contains(body, `data-entity-id="11111111-1111-1111-1111-111111111111"`) {
		t.Errorf("target1 should be in body: %q", body)
	}
	answer := drill(t, got, "page_sections", 3, "content", "items", 0, "answer").(string)
	if !strings.Contains(answer, `data-entity-id="22222222-2222-2222-2222-222222222222"`) {
		t.Errorf("target2 should be in FAQ answer[0]: %q", answer)
	}
}

// drill follows a path of map keys (string) and slice indices (int) into a
// decoded JSON tree. Used by tests to assert mutations land in the right leaf.
func drill(t *testing.T, node any, path ...any) any {
	t.Helper()
	for i, step := range path {
		switch s := step.(type) {
		case string:
			m, ok := node.(map[string]any)
			if !ok {
				t.Fatalf("drill step %d (%q): expected map, got %T", i, s, node)
			}
			node = m[s]
		case int:
			a, ok := node.([]any)
			if !ok {
				t.Fatalf("drill step %d (%d): expected slice, got %T", i, s, node)
			}
			if s < 0 || s >= len(a) {
				t.Fatalf("drill step %d (%d): index out of range (len=%d)", i, s, len(a))
			}
			node = a[s]
		default:
			t.Fatalf("drill step %d: unsupported type %T", i, step)
		}
	}
	return node
}
