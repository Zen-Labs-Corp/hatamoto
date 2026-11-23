package render

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
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
	// Example is content the builder stores when an editor inserts the section (optional, validated at load).
	Example jsontext.Value `json:"example,omitempty"`
}

// parsedSchema is the subset of a section schema the renderer reads itself.
type parsedSchema struct {
	Section    SectionMeta               `json:"x-hm-section"`
	Properties map[string]jsontext.Value `json:"properties"`
}

// FieldKind returns the x-hm-field value of a (sub)schema node, or "".
func FieldKind(node jsontext.Value) string {
	var n struct {
		Kind string `json:"x-hm-field"`
	}
	_ = jsonx.Unmarshal(node, &n, jsonx.StoredLoose)
	return n.Kind
}

// parseSectionSchema validates raw against the supported subset and extracts
// the x-hm-section metadata, the canonical-name -> alias map from top-level
// properties and the x-hm-field kind of each top-level property.
func parseSectionSchema(raw jsontext.Value) (SectionMeta, map[string][]string, map[string]string, error) {
	if err := validateSchemaSubset(raw); err != nil {
		return SectionMeta{}, nil, nil, err
	}
	var ps parsedSchema
	if err := jsonx.Unmarshal(raw, &ps, jsonx.StoredLoose); err != nil {
		return SectionMeta{}, nil, nil, fmt.Errorf("parse schema: %w", err)
	}
	if len(ps.Section.Example) > 0 {
		if err := validateExample(ps.Properties, ps.Section.Example); err != nil {
			return SectionMeta{}, nil, nil, err
		}
	}
	aliases := map[string][]string{}
	kinds := map[string]string{}
	for name, prop := range ps.Properties {
		var p struct {
			Alias []string `json:"x-hm-alias"`
		}
		if err := jsonx.Unmarshal(prop, &p, jsonx.StoredLoose); err != nil {
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

// validateExample checks an x-hm-section.example against the schema's
// top-level properties: it must be an object whose keys are canonical
// property names (aliases are rejected) and whose values match the declared
// types and enums.
func validateExample(props map[string]jsontext.Value, example jsontext.Value) error {
	if example.Kind() != '{' {
		return fmt.Errorf("x-hm-section.example: must be an object")
	}
	if err := rejectDuplicateNames(example); err != nil {
		return fmt.Errorf("x-hm-section.example: %w", err)
	}
	var fields map[string]jsontext.Value
	if err := jsonx.Unmarshal(example, &fields, jsonx.StoredLoose); err != nil {
		return fmt.Errorf("x-hm-section.example: %w", err)
	}
	for name, v := range fields {
		prop, ok := props[name]
		if !ok {
			return fmt.Errorf("x-hm-section.example.%s: not a property of the schema", name)
		}
		if err := checkExampleValue(prop, v, "."+name); err != nil {
			return err
		}
	}
	return nil
}

// rejectDuplicateNames fails on a repeated member name anywhere in v. The
// error names the JSON path. A duplicate would otherwise load and then make
// the builder boot JSON fail to encode.
func rejectDuplicateNames(v jsontext.Value) error {
	dec := jsontext.NewDecoder(strings.NewReader(string(v)))
	for {
		if _, err := dec.ReadToken(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// checkExampleValue checks v against one (sub)schema node, recursing through
// properties and items. at is the path used in errors, like ".ctas[1].url".
func checkExampleValue(node, v jsontext.Value, at string) error {
	var s struct {
		Type       string                    `json:"type"`
		Enum       []jsontext.Value          `json:"enum"`
		Properties map[string]jsontext.Value `json:"properties"`
		Items      jsontext.Value            `json:"items"`
	}
	if err := jsonx.Unmarshal(node, &s, jsonx.StoredLoose); err != nil {
		return fmt.Errorf("x-hm-section.example%s: schema: %w", at, err)
	}
	kind := v.Kind()
	if kind == 'n' {
		return fmt.Errorf("x-hm-section.example%s: null is not allowed", at)
	}
	bad := func() error {
		return fmt.Errorf("x-hm-section.example%s: expected %s", at, s.Type)
	}
	switch s.Type {
	case "string":
		if kind != '"' {
			return bad()
		}
	case "number":
		if kind != '0' {
			return bad()
		}
	case "integer":
		if kind != '0' || strings.ContainsAny(string(v), ".eE") {
			return bad()
		}
	case "boolean":
		if kind != 't' && kind != 'f' {
			return bad()
		}
	case "array":
		if kind != '[' {
			return bad()
		}
		var items []jsontext.Value
		if err := jsonx.Unmarshal(v, &items, jsonx.StoredLoose); err != nil {
			return fmt.Errorf("x-hm-section.example%s: %w", at, err)
		}
		if len(s.Items) > 0 {
			for i, it := range items {
				if err := checkExampleValue(s.Items, it, fmt.Sprintf("%s[%d]", at, i)); err != nil {
					return err
				}
			}
		}
	case "object":
		if kind != '{' {
			return bad()
		}
		var fields map[string]jsontext.Value
		if err := jsonx.Unmarshal(v, &fields, jsonx.StoredLoose); err != nil {
			return fmt.Errorf("x-hm-section.example%s: %w", at, err)
		}
		for name, fv := range fields {
			child, ok := s.Properties[name]
			if !ok {
				return fmt.Errorf("x-hm-section.example%s.%s: unknown field", at, name)
			}
			if err := checkExampleValue(child, fv, at+"."+name); err != nil {
				return err
			}
		}
	}
	if len(s.Enum) > 0 {
		var got any
		if err := jsonx.Unmarshal(v, &got, jsonx.StoredLoose); err != nil {
			return fmt.Errorf("x-hm-section.example%s: %w", at, err)
		}
		for _, e := range s.Enum {
			var want any
			if jsonx.Unmarshal(e, &want, jsonx.StoredLoose) == nil && reflect.DeepEqual(got, want) {
				return nil
			}
		}
		return fmt.Errorf("x-hm-section.example%s: %s is not one of the enum values", at, v)
	}
	return nil
}

// validateSchemaSubset rejects schema features outside the supported subset
// (see bannedSchemaKeywords) and unknown x-hm-field kinds, walking nested
// properties and array items.
func validateSchemaSubset(raw jsontext.Value) error {
	var node map[string]jsontext.Value
	if err := jsonx.Unmarshal(raw, &node, jsonx.StoredLoose); err != nil {
		return fmt.Errorf("schema is not a JSON object: %w", err)
	}
	return validateNode(node, "$")
}

func validateNode(node map[string]jsontext.Value, at string) error {
	for _, k := range bannedSchemaKeywords {
		if _, ok := node[k]; ok {
			return fmt.Errorf("schema %s: %q is not supported", at, k)
		}
	}
	if rawKind, ok := node["x-hm-field"]; ok {
		var kind string
		if err := jsonx.Unmarshal(rawKind, &kind, jsonx.StoredLoose); err != nil || !allowedFieldKinds[kind] {
			return fmt.Errorf("schema %s: invalid x-hm-field %s", at, rawKind)
		}
	}
	if props, ok := node["properties"]; ok {
		var m map[string]map[string]jsontext.Value
		if err := jsonx.Unmarshal(props, &m, jsonx.StoredLoose); err != nil {
			return fmt.Errorf("schema %s: properties: %w", at, err)
		}
		for name, child := range m {
			if err := validateNode(child, at+"."+name); err != nil {
				return err
			}
		}
	}
	if items, ok := node["items"]; ok {
		var child map[string]jsontext.Value
		if err := jsonx.Unmarshal(items, &child, jsonx.StoredLoose); err != nil {
			return fmt.Errorf("schema %s: items: %w", at, err)
		}
		if err := validateNode(child, at+"[]"); err != nil {
			return err
		}
	}
	return nil
}
