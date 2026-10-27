// Package builder is the page_doc v2 model: a tree of layout nodes with a
// validator, pure edit ops, a closed style vocabulary and v1 section
// conversion. It is a leaf package (stdlib and jsonschema only) so render,
// entity and server can share one type without import cycles.
package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalid is wrapped by every validation failure.
var ErrInvalid = errors.New("builder: invalid doc")

// Limits on a stored doc.
const (
	MaxDepth = 8
	MaxNodes = 1000
	MaxBytes = 1 << 20
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidID reports whether id is usable as a node id (and so in a class
// selector or attribute value).
func ValidID(id string) bool { return idRe.MatchString(id) }

// Doc is page_doc v2.
type Doc struct {
	V     int     `json:"v"`
	Nodes []*Node `json:"nodes"`
}

// Node is one element of the tree.
//
//	t: "container" | widget kind | "component" (theme section)
//	p: props, validated per kind by a JSON Schema
//	s: styles per breakpoint ("base", "md", "sm") or breakpoint and state
//	   ("md:hover"; see ParseSKey), StyleSpec keys only
//	c: children (containers only)
//	b: prop bindings, prop key -> data source
type Node struct {
	ID string                       `json:"id"`
	T  string                       `json:"t"`
	P  map[string]any               `json:"p,omitempty"`
	S  map[string]map[string]string `json:"s,omitempty"`
	C  []*Node                      `json:"c,omitempty"`
	// B binds props to data: prop key -> source (see BindSources). While b[key]
	// exists p[key] is ignored; render resolves the source.
	B map[string]string `json:"b,omitempty"`
}

// Section is one v1 page section, as in attributes.page_sections[].
// It mirrors render.Section so the two convert without a shared dependency.
type Section struct {
	ID        string
	Type      string
	Title     string
	Content   map[string]any
	IsVisible *bool
	SortOrder int
	// Extra holds the section keys this model does not know. They ride along
	// in the component's p.extra so a round trip never drops them.
	Extra map[string]any
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// ValidateRaw parses and validates a stored doc, enforcing the 1 MB cap.
func ValidateRaw(raw []byte) error {
	_, err := Parse(raw)
	return err
}

// Parse decodes and validates a doc. Unknown fields are rejected.
func Parse(raw []byte) (*Doc, error) { return parse(raw, false) }

// ParseExact is Parse with numbers kept as json.Number, so a doc that is
// stored again keeps every digit (12345678901234567890, 9.90). Renderers use
// Parse, whose float64 values match what the v1 section reader produced.
func ParseExact(raw []byte) (*Doc, error) { return parse(raw, true) }

func parse(raw []byte, exact bool) (*Doc, error) {
	if len(raw) > MaxBytes {
		return nil, invalid("doc larger than %d bytes", MaxBytes)
	}
	var d Doc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if exact {
		dec.UseNumber()
	}
	if err := dec.Decode(&d); err != nil {
		return nil, invalid("%v", err)
	}
	if dec.More() {
		return nil, invalid("trailing data after doc")
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// Validate checks shape, limits, ids, kinds and props. Every error wraps ErrInvalid.
func (d *Doc) Validate() error {
	if d == nil {
		return invalid("nil doc")
	}
	if d.V != 2 {
		return invalid("v must be 2")
	}
	r := reg.Load() // one snapshot for the whole call: a swap cannot split it
	seen := map[string]bool{}
	count := 0
	var walk func(ns []*Node, parent *Node, depth int, inLoop bool) error
	walk = func(ns []*Node, parent *Node, depth int, inLoop bool) error {
		onlyIn := map[string]int{}
		for _, x := range ns {
			if depth > MaxDepth {
				return invalid("depth > %d", MaxDepth)
			}
			count++
			if count > MaxNodes {
				return invalid("more than %d nodes", MaxNodes)
			}
			if x == nil || !idRe.MatchString(x.ID) {
				return invalid("bad node id")
			}
			if seen[x.ID] {
				return invalid("duplicate id %s", x.ID)
			}
			seen[x.ID] = true
			k, ok := r.kinds[x.T]
			if !ok {
				if !IsPackKind(x.T) {
					return invalid("%s: unknown kind %q", x.ID, x.T)
				}
				// An orphan: its plugin widget is not installed. Keep it
				// (and its subtree) savable without interpreting its props.
				if err := validateOrphan(x); err != nil {
					return invalid("%s: %v", x.ID, err)
				}
				if err := validateStyles(x, parent); err != nil {
					return invalid("%s: %v", x.ID, err)
				}
				if err := walk(x.C, x, depth+1, inLoop); err != nil {
					return err
				}
				continue
			}
			if len(x.C) > 0 && !k.children {
				return invalid("%s: only containers have children", x.ID)
			}
			if err := validateStyles(x, parent); err != nil {
				return invalid("%s: %v", x.ID, err)
			}
			if err := validateProps(r, x); err != nil {
				return invalid("%s: %v", x.ID, err)
			}
			if m := k.meta; m != nil && m.OnlyIn != "" {
				if parent == nil || parent.T != m.OnlyIn {
					return invalid("%s: %s only belongs inside a %s", x.ID, x.T, m.OnlyIn)
				}
				if onlyIn[x.T]++; onlyIn[x.T] > 1 {
					return invalid("%s: only one %s per %s", x.ID, x.T, m.OnlyIn)
				}
			}
			if x.T == "loop" && inLoop {
				return invalid("%s: loops cannot be nested", x.ID)
			}
			if x.T == "loop" && x.P["from"] == "source" && x.P["source"] == nil {
				return invalid("%s: a loop from a source needs its source", x.ID)
			}
			if err := validateBinds(r, x, inLoop); err != nil {
				return invalid("%s: %v", x.ID, err)
			}
			if err := walk(x.C, x, depth+1, inLoop || x.T == "loop"); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(d.Nodes, nil, 1, false)
}

// validateProps checks the node's props against its kind schema in snapshot r. Props are
// round-tripped through JSON so Go-built docs (int, []string) validate like
// decoded ones.
func validateProps(r *registry, x *Node) error {
	raw, err := json.Marshal(x.P)
	if err != nil {
		return fmt.Errorf("props: %w", err)
	}
	var v any
	if x.P == nil {
		v = map[string]any{}
	} else if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("props: %w", err)
	}
	s := r.schemas[x.T]
	if s == nil {
		return fmt.Errorf("unknown kind %q", x.T)
	}
	if err := s.Validate(v); err != nil {
		return fmt.Errorf("props: %v", err)
	}
	return nil
}

// Find returns the node with id and its parent (nil at top level).
func (d *Doc) Find(id string) (node, parent *Node) {
	var walk func(ns []*Node, p *Node) bool
	walk = func(ns []*Node, p *Node) bool {
		for _, x := range ns {
			if x.ID == id {
				node, parent = x, p
				return true
			}
			if walk(x.C, x) {
				return true
			}
		}
		return false
	}
	walk(d.Nodes, nil)
	return
}

// Count is the number of nodes.
func (d *Doc) Count() int {
	n := 0
	var walk func(ns []*Node)
	walk = func(ns []*Node) {
		for _, x := range ns {
			n++
			walk(x.C)
		}
	}
	walk(d.Nodes)
	return n
}
