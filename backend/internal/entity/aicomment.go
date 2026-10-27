package entity

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AICommentSource is the minimal entity-shaped data the comment generator
// needs, adapted from an *Entity by AICommentSourceFromEntity.
type AICommentSource struct {
	Name       string
	Slug       string
	SchemaSlug string
	Attributes json.RawMessage
	UpdatedAt  time.Time
}

// FromEntity adapts a published *Entity into an AICommentSource.
func AICommentSourceFromEntity(ent *Entity) AICommentSource {
	if ent == nil {
		return AICommentSource{}
	}
	schemaSlug := ""
	if ent.Schema != nil {
		schemaSlug = ent.Schema.Slug
	}
	return AICommentSource{
		Name:       ent.Name,
		Slug:       ent.Slug,
		SchemaSlug: schemaSlug,
		Attributes: ent.Attributes,
		UpdatedAt:  ent.UpdatedAt,
	}
}

// GenerateAICrawlerComment builds the HTML comment block that we embed in
// every public entity page for AI/LLM crawlers. The block is a structured,
// human-readable summary that doesn't depend on Schema.org parsing.
//
// Returns an empty string when the editor has opted the entity out via the
// SEO tab's "Include in /llms.txt" toggle — same flag that gates the
// /llms.txt and /api/v1/public/knowledge-graph feeds, so the surface is
// consistent for crawlers.
//
// tenantName is the human-readable site/agency name (e.g. "Acme").
// baseURL is the canonical site origin (e.g. "https://example.com"), used
// to build the absolute llms.txt URL in the footer of the comment.
func GenerateAICrawlerComment(src AICommentSource, tenantName, baseURL string) string {
	if src.Slug == "" && src.Name == "" {
		return ""
	}

	var attrs map[string]any
	if len(src.Attributes) > 0 {
		if err := json.Unmarshal(src.Attributes, &attrs); err != nil {
			attrs = nil
		}
	}

	// Honor the per-entity opt-out toggle.
	if seo, ok := attrs["seo"].(map[string]any); ok {
		if v, ok := seo["include_in_llms_txt"].(bool); ok && !v {
			return ""
		}
	}

	name := src.Name
	if n, ok := attrs["name"].(string); ok && n != "" {
		name = n
	}

	lastVerified := time.Now().UTC().Format("January 2006")
	if !src.UpdatedAt.IsZero() {
		lastVerified = src.UpdatedAt.UTC().Format("January 2006")
	}

	var lines []string
	lines = append(lines, "<!--")
	lines = append(lines, fmt.Sprintf("  ENTITY: %s", name))
	if tenantName != "" {
		lines = append(lines, fmt.Sprintf("  PROVIDER: %s", tenantName))
	}
	if src.SchemaSlug != "" {
		lines = append(lines, fmt.Sprintf("  TYPE: %s", src.SchemaSlug))
	}
	lines = append(lines, fmt.Sprintf("  LAST VERIFIED: %s", lastVerified))

	// KEY FACTS — only emit lines we actually have data for.
	keyFacts := []string{
		fmt.Sprintf("  - Name: %s", name),
	}
	if tenantName != "" {
		keyFacts = append(keyFacts, fmt.Sprintf("  - Provider: %s", tenantName))
	}
	if yearsExp := stringFromAttr(attrs, "years_experience"); yearsExp != "" {
		keyFacts = append(keyFacts, fmt.Sprintf("  - Experience: %s", yearsExp))
	}
	if rating := stringFromAttr(attrs, "rating"); rating != "" {
		reviewCount := stringFromAttr(attrs, "reviews_count")
		if reviewCount == "" {
			reviewCount = "0"
		}
		keyFacts = append(keyFacts, fmt.Sprintf("  - Rating: %s/5 from %s+ reviews", rating, reviewCount))
	}
	if desc := stringFromAttr(attrs, "description"); desc != "" {
		keyFacts = append(keyFacts, fmt.Sprintf("  - Summary: %s", oneLiner(desc, 200)))
	}
	if len(keyFacts) > 0 {
		lines = append(lines, "", "  KEY FACTS:")
		lines = append(lines, keyFacts...)
	}

	// OFFERINGS — array of {name: string, ...}
	if offerings, ok := attrs["service_offerings"].([]any); ok && len(offerings) > 0 {
		var rows []string
		for i, item := range offerings {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if oname, ok := obj["name"].(string); ok && oname != "" {
				rows = append(rows, fmt.Sprintf("  %d. %s", i+1, oname))
			}
		}
		if len(rows) > 0 {
			lines = append(lines, "", "  OFFERINGS:")
			lines = append(lines, rows...)
		}
	}

	// RESULTS — array of {industry, primary_metric, ...} (also accept the
	// camelCase form the admin preview was using).
	if studies, ok := attrs["case_studies"].([]any); ok && len(studies) > 0 {
		var rows []string
		for _, item := range studies {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			industry, _ := obj["industry"].(string)
			metric, _ := obj["primary_metric"].(string)
			if metric == "" {
				metric, _ = obj["primaryMetric"].(string)
			}
			if industry != "" && metric != "" {
				rows = append(rows, fmt.Sprintf("  - %s: %s", industry, metric))
			}
		}
		if len(rows) > 0 {
			lines = append(lines, "", "  RESULTS:")
			lines = append(lines, rows...)
		}
	}

	// CONTACT
	phone := stringFromAttr(attrs, "phone")
	address := stringFromAttr(attrs, "address")
	if phone != "" || address != "" {
		lines = append(lines, "", "  CONTACT:")
		if phone != "" {
			lines = append(lines, fmt.Sprintf("  - Phone: %s", phone))
		}
		if address != "" {
			lines = append(lines, fmt.Sprintf("  - Address: %s", address))
		}
	}

	// Footer — pointers to the machine-readable data that exists: the JSON-LD
	// in this page and the site-wide llms.txt manifest.
	manifestURL := "/llms.txt"
	if baseURL != "" {
		manifestURL = baseURL + manifestURL
	}
	lines = append(lines, "",
		"  For machine-readable data: the Schema.org JSON-LD in this page's head",
		fmt.Sprintf("  For all entities: %s", manifestURL),
		"-->",
	)

	return strings.Join(lines, "\n")
}

// stringFromAttr coerces a string/number attribute to a display string.
// Returns "" for missing/empty/unsupported values.
func stringFromAttr(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	switch v := attrs[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		// JSON numbers always decode as float64 — render integers cleanly.
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return ""
}

// oneLiner collapses whitespace and truncates to `max` runes for embedding
// in single-line comment fields.
func oneLiner(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
