package builder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A v1 section is stored in a doc as an unstyled container(tag=section, v1=true)
// holding exactly one component. Docs made only of such nodes are v1-expressible:
// they render through the theme section partial and convert to a doc and back without loss.

// sectionNodeID is the deterministic id for the i-th section when its own id
// is unusable: "n" + hex(sha256("<i>|<type>"))[:10]. No randomness, so
// converting twice yields identical JSON.
func sectionNodeID(i int, s Section) string {
	if idRe.MatchString(s.ID) {
		return s.ID
	}
	return hashID("n", fmt.Sprintf("%d|%s", i, s.Type))
}

func hashID(prefix, seed string) string {
	h := sha256.Sum256([]byte(seed))
	return prefix + hex.EncodeToString(h[:5])
}

// idAllocator hands out ids that are valid and unused, deterministically.
type idAllocator map[string]bool

// take returns want when it is valid and free, else a hash-derived id (with a
// counter suffix in the unlikely event that is taken too).
func (a idAllocator) take(want, fallback string) string {
	id := want
	for n := 0; !idRe.MatchString(id) || a[id]; n++ {
		id = fallback
		if n > 0 {
			id += strconv.Itoa(n)
		}
	}
	a[id] = true
	return id
}

// FromSections converts v1 page_sections into a v2 doc: each section becomes
// container(tag=section, v1) > component. Document order is the renderer's
// order: stable by sort_order, then array position. ToSections writes
// sort_order = position, so a round trip is equal up to sort_order renumbering
// and repair of unusable or duplicate section ids. Content is copied.
func FromSections(in []Section) *Doc {
	secs := make([]Section, len(in))
	copy(secs, in)
	idx := make([]int, len(in)) // original index, stable seed for generated ids
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return secs[idx[a]].SortOrder < secs[idx[b]].SortOrder })

	used := idAllocator{}
	d := &Doc{V: 2, Nodes: make([]*Node, 0, len(in))}
	for _, i := range idx {
		s := secs[i]
		cid := used.take(sectionNodeID(i, s), hashID("n", fmt.Sprintf("%d|%s", i, s.Type)))
		wrapID := used.take("c-"+cid, hashID("c", fmt.Sprintf("%d|%s", i, cid)))

		content := cloneMap(s.Content)
		if content == nil {
			content = map[string]any{}
		}
		p := map[string]any{"type": s.Type, "content": content}
		// Section keys in Extra would shadow the mirror's own on the way back.
		if extra := cloneMap(s.Extra); len(extra) > 0 {
			for k := range extra {
				if sectionKeys[k] {
					delete(extra, k)
				}
			}
			if len(extra) > 0 {
				p["extra"] = extra
			}
		}
		if s.Title != "" {
			p["title"] = s.Title
		}
		cp := map[string]any{"tag": "section", "v1": true}
		if s.IsVisible != nil && !*s.IsVisible {
			cp["hidden"] = true
		}
		d.Nodes = append(d.Nodes, &Node{ID: wrapID, T: "container", P: cp,
			C: []*Node{{ID: cid, T: "component", P: p}}})
	}
	return d
}

// ToSections converts a doc back to v1 sections: ok is false once the doc holds anything
// v1 cannot express. sort_order is the position.
func ToSections(d *Doc) ([]Section, bool) {
	if d == nil {
		return nil, false
	}
	out := make([]Section, 0, len(d.Nodes))
	for i, n := range d.Nodes {
		c, ok := v1Shaped(n)
		if !ok {
			return nil, false
		}
		hidden, _ := n.P["hidden"].(bool)
		s := Section{ID: c.ID, SortOrder: i}
		s.Type, _ = c.P["type"].(string)
		s.Title, _ = c.P["title"].(string)
		s.Content = cloneMap(asMap(c.P["content"]))
		s.Extra = cloneMap(asMap(c.P["extra"]))
		if hidden {
			f := false
			s.IsVisible = &f
		}
		out = append(out, s)
	}
	return out, true
}

// LegacySections is the section list of a page, derived from its doc: what
// readers that still want v1-shaped data (the public JSON, plugins, hooks) see
// instead of the frozen page_sections mirror. ok is false when the doc holds
// anything v1 cannot express (a builder-only page has no sections).
func LegacySections(d *Doc) ([]Section, bool) { return ToSections(d) }

// V1Expressible reports whether every top-level node is v1-shaped.
func V1Expressible(d *Doc) bool {
	_, ok := ToSections(d)
	return ok
}

// V1Shaped reports whether a top-level node is exactly what a v1 section
// converts to, and returns its component. Renderers use it to send such nodes
// through the theme section partial (byte-identical to v1).
func V1Shaped(n *Node) (*Node, bool) { return v1Shaped(n) }

func v1Shaped(n *Node) (*Node, bool) {
	if n == nil || n.T != "container" || len(n.S) != 0 || len(n.B) != 0 || len(n.C) != 1 {
		return nil, false
	}
	if tag, _ := n.P["tag"].(string); tag != "section" {
		return nil, false
	}
	for k, v := range n.P {
		switch k {
		case "tag":
		case "navlabel": // the editor's private row name: never rendered, so not a shape change
			if _, ok := v.(string); !ok {
				return nil, false
			}
		case "hidden", "v1":
			if _, ok := v.(bool); !ok {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	c := n.C[0]
	if c == nil || c.T != "component" || len(c.S) != 0 || len(c.B) != 0 || len(c.C) != 0 {
		return nil, false
	}
	if t, _ := c.P["type"].(string); t == "" {
		return nil, false
	}
	for k, v := range c.P {
		switch k {
		case "type", "title", "navlabel":
			if _, ok := v.(string); !ok {
				return nil, false
			}
		case "content", "extra":
			if _, ok := v.(map[string]any); !ok {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return c, true
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// Components returns every component node in document order, for readers that
// scan theme-section content (forms, entity links).
func Components(d *Doc) []*Node {
	if d == nil {
		return nil
	}
	var out []*Node
	var walk func(ns []*Node)
	walk = func(ns []*Node) {
		for _, n := range ns {
			if n == nil {
				continue
			}
			if n.T == "component" {
				out = append(out, n)
			}
			walk(n.C)
		}
	}
	walk(d.Nodes)
	return out
}

// SectionMaps renders sections as the JSON shape stored in
// attributes.page_sections (the mirror of a v1-expressible doc).
func SectionMaps(ss []Section) []any {
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		m := make(map[string]any, len(s.Extra)+6)
		for k, v := range s.Extra {
			m[k] = cloneAny(v)
		}
		m["id"], m["type"], m["content"], m["sort_order"] = s.ID, s.Type, s.Content, s.SortOrder
		if m["content"] == nil {
			m["content"] = map[string]any{}
		}
		if s.Title != "" {
			m["title"] = s.Title
		}
		if s.IsVisible != nil {
			m["isVisible"] = *s.IsVisible
		}
		out = append(out, m)
	}
	return out
}

// sectionKeys are the page_sections keys Section models; the rest is Extra.
var sectionKeys = map[string]bool{"id": true, "type": true, "title": true, "content": true, "isVisible": true, "sort_order": true}

// SectionFromMap reads the i-th stored page_sections item. It is the one
// reader of that shape (the public renderer, the write seam and the
// convert CLI all use it), so they cannot disagree: an id missing or not a
// string becomes its printed value or "s<i>", sort_order may be a number or a
// numeric string, and every other key lands in Extra.
func SectionFromMap(i int, m map[string]any) Section {
	s := Section{}
	switch x := m["id"].(type) {
	case nil:
	case string:
		s.ID = x
	default:
		s.ID = fmt.Sprint(x)
	}
	if s.ID == "" {
		s.ID = fmt.Sprintf("s%d", i)
	}
	s.Type, _ = m["type"].(string)
	s.Title, _ = m["title"].(string)
	s.Content, _ = m["content"].(map[string]any)
	if b, ok := m["isVisible"].(bool); ok {
		s.IsVisible = &b
	}
	s.SortOrder = sortOrder(m["sort_order"])
	for k, v := range m {
		if !sectionKeys[k] {
			if s.Extra == nil {
				s.Extra = map[string]any{}
			}
			s.Extra[k] = v
		}
	}
	return s
}

// sortOrder coerces a stored sort_order (number or numeric string); anything
// else sorts as 0.
func sortOrder(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return int(f)
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return int(f)
		}
	}
	return 0
}

// SectionsFromJSON decodes attributes.page_sections one item at a time, so a
// malformed sibling costs that section and not the page. exact keeps numbers
// as json.Number (the write path); otherwise they are float64 (the render path).
func SectionsFromJSON(attrs json.RawMessage, exact bool) []Section {
	var top map[string]json.RawMessage // exact key: a struct tag matches "Page_Sections" too
	if len(attrs) == 0 || json.Unmarshal(attrs, &top) != nil {
		return nil
	}
	var rawSections []json.RawMessage
	_ = json.Unmarshal(top["page_sections"], &rawSections)
	out := make([]Section, 0, len(rawSections))
	for i, raw := range rawSections {
		dec := json.NewDecoder(bytes.NewReader(raw))
		if exact {
			dec.UseNumber()
		}
		var m map[string]any
		if dec.Decode(&m) != nil || m == nil {
			continue // not an object (string, number, null, array)
		}
		out = append(out, SectionFromMap(i, m))
	}
	return out
}

// NodesOfKind returns every node of kind in document order, for readers that
// need a widget's props (the forms a page embeds).
func NodesOfKind(d *Doc, kind string) []*Node {
	if d == nil {
		return nil
	}
	var out []*Node
	var walk func(ns []*Node)
	walk = func(ns []*Node) {
		for _, n := range ns {
			if n == nil {
				continue
			}
			if n.T == kind {
				out = append(out, n)
			}
			walk(n.C)
		}
	}
	walk(d.Nodes)
	return out
}
