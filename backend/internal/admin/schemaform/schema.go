// Package schemaform renders a JSON Schema as an HTML form and reads the form
// back into JSON. Section and entity editors share one set of
// precedence rules, so they look and behave alike. Output is built by html/template from schema and values only:
// no content string ever becomes markup.
package schemaform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Node is the subset of JSON Schema the admin forms understand. Properties keep
// their schema order. $ref/oneOf/anyOf/allOf are banned at theme load
// (render/schema.go), so nothing here resolves references.
type Node struct {
	Type, Format, Title, Description, Pattern string
	Enum                                      []string
	Default                                   any
	MinLength, MaxLength                      *int
	MinItems, MaxItems                        *int
	Minimum, Maximum                          *float64
	Items                                     *Node
	Props                                     []Prop
	Required                                  []string

	WriteOnly bool // writeOnly: a secret the form never shows

	XHm           string   // x-hm-field: plaintext richtext html url image code css lines (string arrays: one item per line)
	XCode         string   // x-code-size: "tall"
	XMedia        bool     // x-media
	XEntitySchema string   // x-entity-schema: slug of the referenced entity type
	XMultiple     *bool    // x-multiple
	Alias         []string // x-hm-alias
	Group         string   // x-hm-group: section heading of a top-level property
	ItemLabel     string   // x-hm-item-label: the Add button of a repeater reads "Add <label>"
}

// Prop is one named property.
type Prop struct {
	Name string
	Node *Node
}

func (n *Node) prop(name string) *Node {
	for _, p := range n.Props {
		if p.Name == name {
			return p.Node
		}
	}
	return nil
}

func (n *Node) isRequired(name string) bool {
	for _, r := range n.Required {
		if r == name {
			return true
		}
	}
	return false
}

// objectEntries decodes a JSON object preserving key order.
func objectEntries(raw json.RawMessage) ([]string, map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("expected an object")
	}
	var keys []string
	m := map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		if _, dup := m[k]; !dup {
			keys = append(keys, k)
		}
		m[k] = v
	}
	return keys, m, nil
}

// ParseSchema parses a JSON Schema document.
func ParseSchema(raw json.RawMessage) (*Node, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return &Node{Type: "object"}, nil
	}
	return parseNode(raw)
}

func parseNode(raw json.RawMessage) (*Node, error) {
	_, m, err := objectEntries(raw)
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	n := &Node{}
	str := func(key string, dst *string) {
		if v, ok := m[key]; ok {
			_ = json.Unmarshal(v, dst)
		}
	}
	str("type", &n.Type)
	str("format", &n.Format)
	str("title", &n.Title)
	str("description", &n.Description)
	str("pattern", &n.Pattern)
	str("x-hm-field", &n.XHm)
	str("x-code-size", &n.XCode)
	str("x-entity-schema", &n.XEntitySchema)
	str("x-hm-group", &n.Group)
	str("x-hm-item-label", &n.ItemLabel)
	if v, ok := m["writeOnly"]; ok {
		_ = json.Unmarshal(v, &n.WriteOnly)
	}
	if v, ok := m["x-media"]; ok {
		_ = json.Unmarshal(v, &n.XMedia)
	}
	if v, ok := m["x-multiple"]; ok {
		var b bool
		if json.Unmarshal(v, &b) == nil {
			n.XMultiple = &b
		}
	}
	if v, ok := m["x-hm-alias"]; ok {
		_ = json.Unmarshal(v, &n.Alias)
	}
	if v, ok := m["enum"]; ok {
		var anyEnum []any
		if json.Unmarshal(v, &anyEnum) == nil {
			for _, e := range anyEnum {
				n.Enum = append(n.Enum, fmt.Sprint(e))
			}
		}
	}
	if v, ok := m["default"]; ok {
		_ = json.Unmarshal(v, &n.Default)
	}
	if v, ok := m["required"]; ok {
		_ = json.Unmarshal(v, &n.Required)
	}
	intp := func(key string) *int {
		if v, ok := m[key]; ok {
			var f float64
			if json.Unmarshal(v, &f) == nil {
				i := int(f)
				return &i
			}
		}
		return nil
	}
	n.MinLength, n.MaxLength, n.MinItems, n.MaxItems = intp("minLength"), intp("maxLength"), intp("minItems"), intp("maxItems")
	fl := func(key string) *float64 {
		if v, ok := m[key]; ok {
			var f float64
			if json.Unmarshal(v, &f) == nil {
				return &f
			}
		}
		return nil
	}
	n.Minimum, n.Maximum = fl("minimum"), fl("maximum")
	if v, ok := m["items"]; ok {
		if it, err := parseNode(v); err == nil {
			n.Items = it
		}
	}
	if v, ok := m["properties"]; ok {
		keys, pm, err := objectEntries(v)
		if err != nil {
			return nil, fmt.Errorf("schema properties: %w", err)
		}
		for _, k := range keys {
			child, err := parseNode(pm[k])
			if err != nil {
				return nil, fmt.Errorf("schema property %q: %w", k, err)
			}
			n.Props = append(n.Props, Prop{Name: k, Node: child})
		}
	}
	if n.Type == "" && len(n.Props) > 0 {
		n.Type = "object"
	}
	if v, ok := m["x-hm-order"]; ok {
		var order []string
		if json.Unmarshal(v, &order) == nil {
			names := make([]string, len(n.Props))
			byName := make(map[string]*Node, len(n.Props))
			for i, p := range n.Props {
				names[i], byName[p.Name] = p.Name, p.Node
			}
			n.Props = n.Props[:0]
			for _, name := range SortNames(names, order) {
				n.Props = append(n.Props, Prop{Name: name, Node: byName[name]})
			}
		}
	}
	n.mapHint()
	return n, nil
}

// Humanize turns "full_description" into "Full description".
func Humanize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// label is the field label: the schema title, else the humanised name.
func (n *Node) label(name string) string {
	if n.Title != "" {
		return n.Title
	}
	return Humanize(name)
}

// mapHint rewrites x-hm-field into the format/x-media hints the dispatcher
// understands, exactly as the Next admin's mapHints does before it renders:
// a "code" field named "body" is therefore a monospace textarea (format
// textarea switches the name-based rich text rule off) and a "url" field named
// "hero_image" is an image field (format uri switches the name rule on).
func (n *Node) mapHint() {
	switch n.XHm {
	case "richtext":
		n.Format = "richtext"
	case "html", "code", "css":
		n.Format = "textarea"
	case "url":
		n.Format = "uri"
	case "image":
		n.Format = "uri"
		n.XMedia = true
	}
}

// SortNames orders property names by an x-hm-order list: the names it lists
// first, in its order, then the others as they were. The database stores
// schemas as jsonb, which does not keep the order of an object's keys, so the
// order a person chose in the schema editor is kept in this list.
func SortNames(names, order []string) []string {
	have := make(map[string]bool, len(names))
	for _, n := range names {
		have[n] = true
	}
	out := make([]string, 0, len(names))
	used := make(map[string]bool, len(names))
	for _, n := range order {
		if have[n] && !used[n] {
			out = append(out, n)
			used[n] = true
		}
	}
	for _, n := range names {
		if !used[n] {
			out = append(out, n)
		}
	}
	return out
}

// groupedProps orders top-level properties for display: ungrouped ones first,
// then each group in order of first appearance, keeping the property order
// inside a group. The heading of a group is the Group of its first property.
func groupedProps(props []Prop) []Prop {
	out := make([]Prop, 0, len(props))
	var groups []string
	for _, p := range props {
		g := strings.TrimSpace(p.Node.Group)
		if g == "" {
			out = append(out, p)
		} else if !containsString(groups, g) {
			groups = append(groups, g)
		}
	}
	for _, g := range groups {
		for _, p := range props {
			if strings.TrimSpace(p.Node.Group) == g {
				out = append(out, p)
			}
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
