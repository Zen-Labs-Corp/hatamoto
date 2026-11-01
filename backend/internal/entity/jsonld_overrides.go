package entity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

func parseCustomJSONLD(s string) ([]map[string]json.RawMessage, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) > MaxCustomJSONLDBytes {
		return nil, fmt.Errorf("must be at most %d KB", MaxCustomJSONLDBytes>>10)
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("is not valid JSON")
	}
	var docs []map[string]json.RawMessage
	switch raw[0] {
	case '{':
		var one map[string]json.RawMessage
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, fmt.Errorf("is not valid JSON")
		}
		docs = []map[string]json.RawMessage{one}
	case '[':
		if err := json.Unmarshal(raw, &docs); err != nil {
			return nil, fmt.Errorf("must be an object or an array of objects")
		}
	default:
		return nil, fmt.Errorf("must be an object or an array of objects")
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("must hold at least one object")
	}
	for _, d := range docs {
		for _, k := range []string{"@context", "@type"} {
			if !nonEmptyJSON(d[k]) {
				return nil, fmt.Errorf("every object needs a non-empty %s", k)
			}
		}
	}
	return docs, nil
}

func nonEmptyJSON(v json.RawMessage) bool {
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
func CustomJSONLDDocs(attrs json.RawMessage) []json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(attrs, &m) != nil {
		return nil
	}
	var text string
	if json.Unmarshal(m[attrCustomJSONLD], &text) != nil {
		return nil
	}
	docs, err := parseCustomJSONLD(text)
	if err != nil {
		return nil
	}
	out := make([]json.RawMessage, 0, len(docs))
	for _, d := range docs {
		b, err := json.Marshal(d)
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
	if json.Unmarshal(e.Attributes, &attrs) != nil {
		return
	}
	if t, _ := attrs[attrSchemaType].(string); IsAllowedSchemaOrgType(t) {
		doc["@type"] = t
	}
}
