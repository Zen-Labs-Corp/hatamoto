package entity

import (
	"testing"
)

func TestEnrichJSONLDWithEvidence_EmptyLinks(t *testing.T) {
	jsonLD := map[string]any{"@type": "Article"}
	EnrichJSONLDWithEvidence(jsonLD, nil)

	if _, ok := jsonLD["citation"]; ok {
		t.Error("citation should not be set when no links provided")
	}
	if _, ok := jsonLD["evidenceCount"]; ok {
		t.Error("evidenceCount should not be set when no links provided")
	}
	if _, ok := jsonLD["mentions"]; ok {
		t.Error("mentions should not be set when no links provided")
	}
}

func TestEnrichJSONLDWithEvidence_AggregateFields(t *testing.T) {
	jsonLD := map[string]any{
		"@type": "Article",
		"@id":   "https://example.com/article#article",
	}
	verifiedEarly := "2025-01-01T00:00:00Z"
	verifiedLate := "2026-03-15T00:00:00Z"
	links := []EvidenceLinkForJSONLD{
		{
			FieldPath:       "headline",
			LinkType:        "supports",
			ConfidenceScore: 90,
			VerifiedAt:      &verifiedEarly,
			SourceTitle:     "External Report",
			SourceType:      "external_url",
			SourceURL:       "https://example.com/report",
		},
		{
			FieldPath:       "headline",
			LinkType:        "supports",
			ConfidenceScore: 70,
			VerifiedAt:      &verifiedLate,
			SourceTitle:     "Internal Case Study",
			SourceType:      "case_study",
			// No URL — counted but not cited.
		},
	}

	EnrichJSONLDWithEvidence(jsonLD, links)

	if jsonLD["evidenceCount"] != 2 {
		t.Errorf("evidenceCount: got %v, want 2", jsonLD["evidenceCount"])
	}
	if jsonLD["evidenceLevel"] != "strong" {
		t.Errorf("evidenceLevel: got %v, want strong (avg 80)", jsonLD["evidenceLevel"])
	}
	if jsonLD["lastReviewed"] != verifiedLate {
		t.Errorf("lastReviewed: got %v, want %s", jsonLD["lastReviewed"], verifiedLate)
	}

	citations, ok := jsonLD["citation"].([]map[string]any)
	if !ok {
		t.Fatalf("citation should be []map[string]interface{}, got %T", jsonLD["citation"])
	}
	if len(citations) != 1 {
		t.Errorf("citation count: got %d, want 1 (internal source should not appear)", len(citations))
	}
	if citations[0]["url"] != "https://example.com/report" {
		t.Errorf("citation url: got %v", citations[0]["url"])
	}
}

func TestEnrichJSONLDWithEvidence_PerFieldClaims(t *testing.T) {
	jsonLD := map[string]any{
		"@type": "Article",
		"@id":   "https://example.com/article#article",
	}
	links := []EvidenceLinkForJSONLD{
		{
			FieldPath:       "years_experience",
			ConfidenceScore: 80,
			SourceTitle:     "About Page",
			SourceType:      "external_url",
			SourceURL:       "https://example.com/about",
		},
		{
			FieldPath:       "client_satisfaction",
			ConfidenceScore: 60,
			SourceTitle:     "Survey Results",
			SourceType:      "external_url",
			SourceURL:       "https://example.com/survey",
		},
		{
			FieldPath:       "client_satisfaction",
			ConfidenceScore: 60,
			SourceTitle:     "Internal Doc",
			SourceType:      "case_study",
			// No URL — claim still emitted, but no citation for this link.
		},
	}

	EnrichJSONLDWithEvidence(jsonLD, links)

	mentions, ok := jsonLD["mentions"].([]map[string]any)
	if !ok {
		t.Fatalf("mentions should be []map[string]interface{}, got %T", jsonLD["mentions"])
	}
	if len(mentions) != 2 {
		t.Fatalf("mentions count: got %d, want 2 (one per distinct field)", len(mentions))
	}

	// Sorted by field path alphabetically: client_satisfaction, years_experience.
	if mentions[0]["@type"] != "Claim" {
		t.Errorf("mention[0] @type: got %v, want Claim", mentions[0]["@type"])
	}
	if mentions[0]["name"] != "Client Satisfaction" {
		t.Errorf("mention[0] name: got %v, want Client Satisfaction", mentions[0]["name"])
	}
	// Entity IRI already has a fragment, so claim @id uses a hyphen separator
	// to stay a valid single-fragment IRI.
	wantID := "https://example.com/article#article-claim-client_satisfaction"
	if mentions[0]["@id"] != wantID {
		t.Errorf("mention[0] @id: got %v, want %s", mentions[0]["@id"], wantID)
	}

	clientSatCitations, ok := mentions[0]["citation"].([]map[string]any)
	if !ok {
		t.Fatalf("mention[0] citation should be []map[string]interface{}, got %T", mentions[0]["citation"])
	}
	if len(clientSatCitations) != 1 {
		t.Errorf("client_satisfaction citation count: got %d, want 1 (internal doc skipped)", len(clientSatCitations))
	}

	if mentions[1]["name"] != "Years Experience" {
		t.Errorf("mention[1] name: got %v, want Years Experience", mentions[1]["name"])
	}

	// Appearance back-reference points at the entity IRI.
	app, ok := mentions[0]["appearance"].(map[string]any)
	if !ok {
		t.Fatal("mention[0] appearance missing")
	}
	if app["@id"] != "https://example.com/article#article" {
		t.Errorf("appearance @id: got %v", app["@id"])
	}
}

func TestEnrichJSONLDWithEvidence_NoEntityIRI(t *testing.T) {
	// Without @id/url we still emit the claim, just without identifiers.
	jsonLD := map[string]any{"@type": "Article"}
	links := []EvidenceLinkForJSONLD{{
		FieldPath:       "headline",
		ConfidenceScore: 80,
		SourceTitle:     "Source",
		SourceType:      "external_url",
		SourceURL:       "https://example.com/source",
	}}

	EnrichJSONLDWithEvidence(jsonLD, links)

	mentions, ok := jsonLD["mentions"].([]map[string]any)
	if !ok || len(mentions) != 1 {
		t.Fatalf("mentions: got %v", jsonLD["mentions"])
	}
	if _, hasID := mentions[0]["@id"]; hasID {
		t.Error("claim @id should be omitted when entity has no IRI")
	}
	if _, hasAppearance := mentions[0]["appearance"]; hasAppearance {
		t.Error("claim appearance should be omitted when entity has no IRI")
	}
}

func TestEnrichJSONLDWithEvidence_InternalSourceCitation(t *testing.T) {
	// Internal entity-backed source — the server layer has already resolved
	// the canonical URL and set a more specific Schema.org @type.
	jsonLD := map[string]any{
		"@type": "Article",
		"@id":   "https://example.com/blog/foo#article",
	}
	links := []EvidenceLinkForJSONLD{
		{
			FieldPath:       "client_satisfaction",
			ConfidenceScore: 80,
			SourceTitle:     "Acme Growth Case Study",
			SourceType:      "case_study",
			SourceURL:       "https://example.com/case-studies/acme-growth",
			SourceURLType:   "Article",
		},
		{
			FieldPath:       "client_satisfaction",
			ConfidenceScore: 70,
			SourceTitle:     "John Rambrandt",
			SourceType:      "internal_entity",
			SourceURL:       "https://example.com/team/john-rambrandt",
			SourceURLType:   "Person",
		},
	}

	EnrichJSONLDWithEvidence(jsonLD, links)

	citations, ok := jsonLD["citation"].([]map[string]any)
	if !ok || len(citations) != 2 {
		t.Fatalf("citation: got %v", jsonLD["citation"])
	}

	// Global citation block carries the specific @type, not the default.
	types := map[string]bool{}
	for _, c := range citations {
		types[c["@type"].(string)] = true
	}
	if !types["Article"] || !types["Person"] {
		t.Errorf("citation @types: got %v, want both Article and Person", types)
	}

	// Per-field claim also propagates the type.
	mentions, ok := jsonLD["mentions"].([]map[string]any)
	if !ok || len(mentions) != 1 {
		t.Fatalf("mentions: got %v", jsonLD["mentions"])
	}
	claimCitations := mentions[0]["citation"].([]map[string]any)
	if len(claimCitations) != 2 {
		t.Fatalf("claim citation count: got %d, want 2", len(claimCitations))
	}
}

func TestEnrichJSONLDWithEvidence_DefaultsToCreativeWork(t *testing.T) {
	jsonLD := map[string]any{"@type": "Article"}
	links := []EvidenceLinkForJSONLD{{
		FieldPath:       "headline",
		ConfidenceScore: 80,
		SourceTitle:     "Source",
		SourceType:      "external_url",
		SourceURL:       "https://example.com/source",
		// SourceURLType left empty
	}}

	EnrichJSONLDWithEvidence(jsonLD, links)

	citations := jsonLD["citation"].([]map[string]any)
	if citations[0]["@type"] != "CreativeWork" {
		t.Errorf("expected CreativeWork fallback, got %v", citations[0]["@type"])
	}
}

func TestSchemaOrgTypeForEntity(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"blog", "Article"},
		{"page", "WebPage"},
		{"unknown-thing", "CreativeWork"},
	}
	for _, c := range cases {
		ent := &Entity{Schema: &EntitySchema{Slug: c.slug}}
		if got := SchemaOrgTypeForEntity(ent); got != c.want {
			t.Errorf("SchemaOrgTypeForEntity(%q) = %q, want %q", c.slug, got, c.want)
		}
	}
	// Nil-safety
	if got := SchemaOrgTypeForEntity(nil); got != "CreativeWork" {
		t.Errorf("nil entity should default to CreativeWork, got %q", got)
	}
}

func TestSanitiseFragment(t *testing.T) {
	cases := map[string]string{
		"simple":          "simple",
		"with_underscore": "with_underscore",
		"dotted.path":     "dotted.path",
		"array[0]":        "array-0-",
		"unicode-é":       "unicode--",
		"trailing-dash-":  "trailing-dash-",
	}
	for input, want := range cases {
		if got := sanitiseFragment(input); got != want {
			t.Errorf("sanitiseFragment(%q) = %q, want %q", input, got, want)
		}
	}
}
