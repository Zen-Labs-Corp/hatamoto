package searchreplace

import (
	"encoding/json"
	"slices"
)

// byAlias is the property that lists k in its x-hm-alias.
func byAlias(props map[string]any, k string) map[string]any {
	for _, p := range props {
		pm, _ := p.(map[string]any)
		al, _ := pm["x-hm-alias"].([]any)
		if slices.Contains(al, any(k)) {
			return pm
		}
	}
	return nil
}

// schemaKinds returns the field-kind lookup of an entity schema: "richtext"
// for a property with format richtext or x-hm-field richtext, "html" for
// x-hm-field html, "" otherwise. Repeater items follow their item schema, so
// array indices are not part of the path.
func schemaKinds(raw json.RawMessage) func(path []string) string {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	return func(path []string) string {
		n := root
		for _, k := range path {
			for n != nil && n["properties"] == nil && n["items"] != nil {
				n, _ = n["items"].(map[string]any)
			}
			props, _ := n["properties"].(map[string]any)
			next, _ := props[k].(map[string]any)
			if next == nil {
				next = byAlias(props, k)
			}
			n = next
			if n == nil {
				return ""
			}
		}
		if f, _ := n["x-hm-field"].(string); f == "richtext" || f == "html" {
			return f
		}
		if f, _ := n["format"].(string); f == "richtext" {
			return "richtext"
		}
		return ""
	}
}
