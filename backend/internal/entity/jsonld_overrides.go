package entity

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
)

// MaxCustomJSONLDBytes caps the custom JSON-LD block of one entry.
const MaxCustomJSONLDBytes = 16 << 10

// Entry attribute keys of the per-entry schema controls.
const (
	attrSchemaType   = "schema_type"
	attrCustomJSONLD = "custom_jsonld"
)

// ValidateCustomJSONLD checks an entry's custom JSON-LD text: empty is fine;
// otherwise it must be at most MaxCustomJSONLDBytes of JSON holding one object
// or an array of objects, each with a non-empty @context and @type.
func ValidateCustomJSONLD(s string) error {
	_, err := parseCustomJSONLD(s)
	return err
}

func parseCustomJSONLD(s string) ([]map[string]jsontext.Value, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) > MaxCustomJSONLDBytes {
		return nil, fmt.Errorf("keep it to %d KB or less", MaxCustomJSONLDBytes>>10)
	}
	var raw jsontext.Value
	if err := jsonx.Unmarshal([]byte(s), &raw, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("enter valid JSON")
	}
	var docs []map[string]jsontext.Value
	switch raw[0] {
	case '{':
		var one map[string]jsontext.Value
		if err := jsonx.Unmarshal(raw, &one, jsonx.StoredLoose); err != nil {
			return nil, fmt.Errorf("enter valid JSON")
		}
		docs = []map[string]jsontext.Value{one}
	case '[':
		if err := jsonx.Unmarshal(raw, &docs, jsonx.StoredLoose); err != nil {
			return nil, fmt.Errorf("use one object or an array of objects")
		}
	default:
		return nil, fmt.Errorf("use one object or an array of objects")
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("add at least one object")
	}
	for _, d := range docs {
		for _, k := range []string{"@context", "@type"} {
			if !nonEmptyJSON(d[k]) {
				return nil, fmt.Errorf("give every object a non-empty %s", k)
			}
		}
	}
	return docs, nil
}

func nonEmptyJSON(v jsontext.Value) bool {
	switch string(bytes.TrimSpace(v)) {
	case "", "null", `""`, "[]", "{}":
		return false
	}
	return true
}

// CustomJSONLDDocs returns the entry's valid custom JSON-LD objects, each
// re-marshalled compactly. A missing or invalid block yields nil. The result
// is meant for extra ld+json scripts next to the generated document and is
// never merged into it.
func CustomJSONLDDocs(attrs jsontext.Value) []jsontext.Value {
	var m map[string]jsontext.Value
	if jsonx.Unmarshal(attrs, &m, jsonx.StoredLoose) != nil {
		return nil
	}
	var text string
	if jsonx.Unmarshal(m[attrCustomJSONLD], &text, jsonx.StoredLoose) != nil {
		return nil
	}
	docs, err := parseCustomJSONLD(text)
	if err != nil {
		return nil
	}
	out := make([]jsontext.Value, 0, len(docs))
	for _, d := range docs {
		b, err := jsonx.Marshal(d, jsonx.HTMLSafe)
		if err != nil {
			return nil
		}
		out = append(out, b)
	}
	return out
}

// applyTypeOverrides changes only doc["@type"]: first the schema's article
// subtype for Article-family documents, then the entry's own schema_type,
// which wins. Documents of plugin-built schemas are left alone.
func applyTypeOverrides(doc map[string]any, e *Entity) {
	if doc == nil || e == nil || e.Schema == nil {
		return
	}
	if _, ok := LookupJSONLDBuilder(e.Schema.Slug); ok {
		return
	}
	if cur, _ := doc["@type"].(string); slices.Contains(AllowedArticleTypes, cur) {
		if st, err := e.Schema.URLSettings(); err == nil && slices.Contains(AllowedArticleTypes, st.ArticleType) {
			doc["@type"] = st.ArticleType
		}
	}
	var attrs map[string]any
	if jsonx.Unmarshal(e.Attributes, &attrs, jsonx.StoredLoose) != nil {
		return
	}
	if t, _ := attrs[attrSchemaType].(string); IsAllowedSchemaOrgType(t) {
		doc["@type"] = t
	}
}

// SchemaFixesJSONLDType reports whether the JSON-LD @type of a schema's entries
// is decided by the schema itself (an x-schema-org-type annotation) or by a
// plugin builder, so settings.schema_org_type has no effect on it.
func SchemaFixesJSONLDType(sc *EntitySchema) bool {
	if sc == nil {
		return false
	}
	if _, ok := LookupJSONLDBuilder(sc.Slug); ok {
		return true
	}
	return sc.Slug != personSchemaSlug && hasSchemaOrgAnnotations(sc.Schema)
}
