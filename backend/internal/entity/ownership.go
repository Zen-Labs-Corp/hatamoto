package entity

import (
	"encoding/json/jsontext"
	"reflect"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
)

// structuralKeys are the attributes a plugin owns the shape of: the body and
// the SEO fields. Editing any other attribute (featured, excerpt, custom
// fields, the mirrored title) leaves the entity with its plugin. Keys with
// the "og_" prefix are SEO fields too.
var structuralKeys = []string{
	"content", "body", keyDoc, keySections,
	"meta_title", "meta_description", "canonical_url", "no_index",
	"schema_type", "custom_jsonld", attrJSONLDWebPage, attrJSONLDBreadcrumb, "hide_from_sitemap", "breadcrumb_title", "primary_term",
	"seo",
}

func isStructuralKey(k string) bool {
	return strings.HasPrefix(k, "og_") || containsString(structuralKeys, k)
}

// IsSEOAttr reports whether k is one of the SEO attributes: the structural
// keys other than the body text and the page doc.
func IsSEOAttr(k string) bool {
	return isStructuralKey(k) && k != "content" && k != "body" && k != keyDoc && k != keySections
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// structuralAttrsChanged reports whether the structural attributes differ
// between two blobs once both are put in the shape the admin forms write:
// absent, "", false and an empty object are the same, strings are trimmed with CRLF read as LF (browsers post textareas that way),
// and a schema's x-hm-alias keys stand in for an empty canonical key.
func structuralAttrsChanged(schema *EntitySchema, a, b jsontext.Value) bool {
	am, bm := structuralView(schema, a), structuralView(schema, b)
	return !reflect.DeepEqual(am, bm)
}

func structuralView(schema *EntitySchema, raw jsontext.Value) map[string]any {
	var attrs map[string]any
	if jsonx.Unmarshal(raw, &attrs, jsonx.ExactNumbers) != nil {
		return map[string]any{"_raw": string(raw)}
	}
	aliases := schemaAliases(schema)
	out := map[string]any{}
	for k := range attrs {
		if !isStructuralKey(k) {
			continue
		}
		if v := canonAttr(attrs[k]); v != nil {
			out[k] = v
		}
	}
	// An alias key reads as the canonical key when that one is empty.
	for canonical, list := range aliases {
		if !isStructuralKey(canonical) || out[canonical] != nil {
			continue
		}
		for _, alias := range list {
			if v := canonAttr(attrs[alias]); v != nil {
				out[canonical] = v
				break
			}
		}
	}
	return out
}

// schemaAliases reads the x-hm-alias lists of the schema's top-level properties.
func schemaAliases(schema *EntitySchema) map[string][]string {
	if schema == nil {
		return nil
	}
	var s struct {
		Properties map[string]struct {
			Alias []string `json:"x-hm-alias"`
		} `json:"properties"`
	}
	if jsonx.Unmarshal(schema.Schema, &s, jsonx.StoredLoose) != nil {
		return nil
	}
	out := map[string][]string{}
	for name, p := range s.Properties {
		if len(p.Alias) > 0 {
			out[name] = p.Alias
		}
	}
	return out
}

// canonAttr returns nil for an empty value ("", false, null, an empty or
// all-empty object) and the trimmed, recursively cleaned value otherwise.
func canonAttr(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t = strings.TrimSpace(strings.ReplaceAll(t, "\r\n", "\n")); t != "" {
			return t
		}
		return nil
	case bool:
		if t {
			return true
		}
		return nil
	case map[string]any:
		out := map[string]any{}
		for k, e := range t {
			// Listed in llms.txt unless switched off: here false is the value
			// that counts and true is the default.
			if b, ok := e.(bool); ok && k == "include_in_llms_txt" {
				if !b {
					out[k] = false
				}
				continue
			}
			if c := canonAttr(e); c != nil {
				out[k] = c
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return v
	}
}
