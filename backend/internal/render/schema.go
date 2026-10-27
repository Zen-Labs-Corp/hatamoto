package render

import (
	"encoding/json"
	"fmt"
)

// bannedSchemaKeywords are JSON Schema features outside the supported subset;
// the admin form renderer cannot render them.
var bannedSchemaKeywords = []string{"$ref", "oneOf", "anyOf", "allOf", "patternProperties"}

// allowedFieldKinds are the legal values of the x-hm-field extension.
var allowedFieldKinds = map[string]bool{
	"plaintext": true, "richtext": true, "html": true, "url": true, "image": true, "code": true, "css": true, "lines": true,
}

// SectionMeta is the top-level x-hm-section extension of a section schema.
type SectionMeta struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// parsedSchema is the subset of a section schema the renderer reads itself.
type parsedSchema struct {
	Section    SectionMeta                `json:"x-hm-section"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// FieldKind returns the x-hm-field value of a (sub)schema node, or "".
func FieldKind(node json.RawMessage) string {
	var n struct {
		Kind string `json:"x-hm-field"`
	}
	_ = json.Unmarshal(node, &n)
	return n.Kind
}

// parseSectionSchema validates raw against the supported subset and extracts
// the x-hm-section metadata, the canonical-name -> alias map from top-level
// properties and the x-hm-field kind of each top-level property.
func parseSectionSchema(raw json.RawMessage) (SectionMeta, map[string][]string, map[string]string, error) {
	if err := validateSchemaSubset(raw); err != nil {
		return SectionMeta{}, nil, nil, err
	}
	var ps parsedSchema
	if err := json.Unmarshal(raw, &ps); err != nil {
		return SectionMeta{}, nil, nil, fmt.Errorf("parse schema: %w", err)
	}
	aliases := map[string][]string{}
	kinds := map[string]string{}
	for name, prop := range ps.Properties {
		var p struct {
			Alias []string `json:"x-hm-alias"`
		}
		if err := json.Unmarshal(prop, &p); err != nil {
			return SectionMeta{}, nil, nil, fmt.Errorf("property %q: %w", name, err)
		}
		if len(p.Alias) > 0 {
			aliases[name] = p.Alias
		}
		if k := FieldKind(prop); k != "" {
			kinds[name] = k
		}
	}
	return ps.Section, aliases, kinds, nil
}

// validateSchemaSubset rejects schema features outside the supported subset
// (see bannedSchemaKeywords) and unknown x-hm-field kinds, walking nested
// properties and array items.
func validateSchemaSubset(raw json.RawMessage) error {
	var node map[string]json.RawMessage
	if err := json.Unmarshal(raw, &node); err != nil {
		return fmt.Errorf("schema is not a JSON object: %w", err)
	}
	return validateNode(node, "$")
}

func validateNode(node map[string]json.RawMessage, at string) error {
	for _, k := range bannedSchemaKeywords {
		if _, ok := node[k]; ok {
			return fmt.Errorf("schema %s: %q is not supported", at, k)
		}
	}
	if rawKind, ok := node["x-hm-field"]; ok {
		var kind string
		if err := json.Unmarshal(rawKind, &kind); err != nil || !allowedFieldKinds[kind] {
			return fmt.Errorf("schema %s: invalid x-hm-field %s", at, rawKind)
		}
	}
	if props, ok := node["properties"]; ok {
		var m map[string]map[string]json.RawMessage
		if err := json.Unmarshal(props, &m); err != nil {
			return fmt.Errorf("schema %s: properties: %w", at, err)
		}
		for name, child := range m {
			if err := validateNode(child, at+"."+name); err != nil {
				return err
			}
		}
	}
	if items, ok := node["items"]; ok {
		var child map[string]json.RawMessage
		if err := json.Unmarshal(items, &child); err != nil {
			return fmt.Errorf("schema %s: items: %w", at, err)
		}
		if err := validateNode(child, at+"[]"); err != nil {
			return err
		}
	}
	return nil
}
