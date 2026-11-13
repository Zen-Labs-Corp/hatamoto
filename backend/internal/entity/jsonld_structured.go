package entity

import (
	"strings"
	"time"
)

// schemaSettingsOf is the entity's decoded schema settings; unreadable
// settings read as zero (JSON-LD must never fail on them).
func schemaSettingsOf(ent *Entity) SchemaURLSettings {
	if ent == nil || ent.Schema == nil {
		return SchemaURLSettings{}
	}
	st, _ := ent.Schema.URLSettings()
	return st
}

// modifiedTime is the dateModified of an entity: updated_at, unless the
// schema's modified_from names an attribute holding a date (RFC 3339 or
// YYYY-MM-DD). An absent or unparseable attribute falls back to updated_at,
// and the result is never earlier than published. Without modified_from the
// result is updated_at untouched, so default output does not change.
func modifiedTime(ent *Entity, attrs map[string]any, st SchemaURLSettings, published *time.Time) time.Time {
	if st.ModifiedFrom == "" {
		return ent.UpdatedAt
	}
	t := ent.UpdatedAt
	if s, ok := attrs[string(st.ModifiedFrom)].(string); ok {
		if p, ok := parseModified(strings.TrimSpace(s)); ok {
			t = p
		}
	}
	if published != nil && !published.IsZero() && t.Before(*published) {
		t = *published
	}
	return t
}

func parseModified(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// absoluteURL resolves a root-relative path against baseURL; absolute URLs
// pass through.
func absoluteURL(baseURL, u string) string {
	if strings.HasPrefix(u, "/") {
		return strings.TrimRight(baseURL, "/") + u
	}
	return u
}

// creativeWorkTypes are the allowed schema_org_type values that are a
// schema.org CreativeWork, the types that take publisher and isPartOf.
var creativeWorkTypes = map[string]bool{
	"Article": true, "BlogPosting": true, "NewsArticle": true,
	"Course": true, "HowTo": true, "WebPage": true, "CreativeWork": true,
}

// applyStructuredLinks adds the site-graph references and the term set to a
// generic document. It does nothing unless a structured-data key is set, so
// unconfigured schemas keep today's output. Existing keys are never replaced.
// publisher and isPartOf are CreativeWork properties and are added to those
// types only.
func applyStructuredLinks(doc map[string]any, st SchemaURLSettings, baseURL string) {
	if !st.StructuredDataConfigured() {
		return
	}
	if doc["@type"] == "DefinedTerm" && st.TermSetURL != "" {
		doc["inDefinedTermSet"] = map[string]any{
			"@type": "DefinedTermSet",
			"@id":   absoluteURL(baseURL, string(st.TermSetURL)),
		}
	}
	if typ, _ := doc["@type"].(string); !creativeWorkTypes[typ] {
		return
	}
	if _, ok := doc["publisher"]; !ok {
		doc["publisher"] = map[string]any{"@id": baseURL + "#organization"}
	}
	if _, ok := doc["isPartOf"]; !ok {
		doc["isPartOf"] = map[string]any{"@id": baseURL + "#website"}
	}
}
