package builder

import (
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strconv"
)

// PropInfo describes one prop of a node kind, derived from its compiled
// schema. Type is "string", "integer", "boolean" or "object".
type PropInfo struct {
	Key      string
	Type     string
	Enum     []any
	Min, Max *float64
	Required bool
	// Widget is the inspector control hint (x-hm-widget), "" for the default
	// control of the type. MaxLength is the string length limit, 0 for none.
	Widget    string
	MaxLength int
	// Bind is the class of source the prop accepts (x-hm-bind: text, url or
	// image), "" when the prop cannot be bound.
	Bind string
}

// KindInfo describes one node kind for the palette, inspector and outline.
// Default holds the props of a fresh node (nil when the kind needs none).
type KindInfo struct {
	Kind     string
	Children bool
	Props    []PropInfo
	Default  map[string]any
	// Universal lists the universal props (UniversalKeys) this kind takes.
	Universal []string
	// Interactive kinds render (or may render) links, buttons or other interactive
	// content, so a linked container cannot hold them (see InteractiveIn).
	Interactive bool
	// Widget kinds only: palette label, hint, group and the children a fresh
	// node is inserted with.
	Label, Hint, Group string
	// OnlyIn is the one parent kind a node of this kind may sit under ("" = any).
	OnlyIn string
	Seed   []SeedNode
	// Hidden kinds are read by the client but never offered by the stock
	// palette: the editor adds ref through blocks, slot in part mode and
	// loopempty from a loop's inspector.
	Hidden bool
	// Display is the CSS display a shown node of this kind has by default (the
	// root rule of its base or widget CSS; a container in grid layout is "grid").
	// The editor writes it when "show" has to undo a wider hide.
	Display string
}

// naturalDisplay lists the kinds whose root is not display:block. A test in
// render checks each against the CSS; every other kind is block.
var naturalDisplay = map[string]string{
	"container": "flex", "ref": "flex", "button": "inline-block", "icon": "inline-block", "table": "table",
	"columns": "grid", "gallery": "grid",
	"buttons": "flex", "stat": "flex", "loop": "flex", "loopempty": "flex",
}

// paletteOrder is the order Kinds reports. A kind added to the registry
// without an entry here is appended alphabetically, so none is ever hidden.
var paletteOrder = []string{"container", "heading", "text", "button", "image", "spacer", "divider", "component", "ref", "slot"}

// hiddenKinds are the reuse kinds the stock palette does not list.
var hiddenKinds = map[string]bool{"ref": true, "slot": true, "loopempty": true}

// defaultProps are the props of a fresh node. Kinds not listed start empty.
var defaultProps = map[string]map[string]any{
	"container": {"tag": "div"},
	"heading":   {"text": "Heading", "level": float64(2)},
	"text":      {"text": "Text"},
	"button":    {"text": "Button"},
}

// Kinds lists every node kind in palette order. The result is a fresh copy.
func Kinds() []KindInfo {
	r := reg.Load()
	kinds := r.kinds
	names := make([]string, 0, len(kinds))
	listed := map[string]bool{}
	for _, n := range paletteOrder {
		if _, ok := kinds[n]; ok {
			names = append(names, n)
			listed[n] = true
		}
	}
	var rest []string
	for n := range kinds {
		if !listed[n] {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	widgetRank(kinds, rest)
	names = append(names, rest...)

	out := make([]KindInfo, 0, len(names))
	for _, n := range names {
		ki := KindInfo{
			Kind:        n,
			Children:    kinds[n].children,
			Props:       propInfosIn(r, n),
			Default:     cloneMap(defaultsIn(kinds, n)),
			Universal:   universalFor(n, kinds[n].own),
			Interactive: interactiveKinds[n] || IsPackKind(n),
			Hidden:      hiddenKinds[n],
			Display:     "block",
		}
		if d, ok := naturalDisplay[n]; ok {
			ki.Display = d
		}
		if m := kinds[n].meta; m != nil {
			ki.Label, ki.Hint, ki.Group, ki.OnlyIn = m.Label, m.Hint, m.Group, m.OnlyIn
			for _, s := range m.Seed {
				ki.Seed = append(ki.Seed, s.clone())
			}
		}
		out = append(out, ki)
	}
	return out
}

// PropMaxLength is the schema maxLength of a string prop of kind, 0 when the
// kind or prop is unknown or has no limit.
func PropMaxLength(kind, key string) int {
	r := reg.Load()
	if _, ok := r.schemas[kind]; !ok {
		return 0
	}
	for _, p := range propInfosIn(r, kind) {
		if p.Key == key {
			return p.MaxLength
		}
	}
	return 0
}

// defaultsOf are the props of a fresh node of kind.
func defaultsOf(kind string) map[string]any {
	return defaultsIn(reg.Load().kinds, kind)
}

func defaultsIn(kinds map[string]kindSpec, kind string) map[string]any {
	if m := kinds[kind].meta; m != nil {
		return m.Default
	}
	return defaultProps[kind]
}

func propInfosIn(r *registry, kind string) []PropInfo {
	kinds := r.kinds
	s := r.schemas[kind]
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	infos := make([]PropInfo, 0, len(s.Properties))
	for key, ps := range s.Properties {
		if slices.Contains(UniversalKeys, key) && !kinds[kind].own[key] {
			continue // universal: edited by the style presets UI, the navigator rename and the Advanced tab; not prop fields
		}
		pi := PropInfo{Key: key, Required: required[key], Min: ratFloat(ps.Minimum), Max: ratFloat(ps.Maximum), Widget: kinds[kind].widgets[key], Bind: kinds[kind].bind[key]}
		if ps.MaxLength > 0 {
			pi.MaxLength = ps.MaxLength
		}
		if len(ps.Enum) > 0 {
			pi.Enum = append([]any(nil), ps.Enum...)
			pi.Type = "string"
		} else if len(ps.Types) > 0 {
			pi.Type = ps.Types[0]
		}
		infos = append(infos, pi)
	}
	if order := kinds[kind].order; kinds[kind].meta != nil {
		pos := map[string]int{}
		for i, key := range order {
			pos[key] = i
		}
		sort.Slice(infos, func(i, j int) bool {
			hi, hj := infos[i].Key == "hidden", infos[j].Key == "hidden"
			if hi != hj {
				return hj
			}
			return pos[infos[i].Key] < pos[infos[j].Key]
		})
		return infos
	}
	rank := func(p PropInfo) int {
		switch {
		case p.Required:
			return 0
		case p.Key == "hidden":
			return 2
		}
		return 1
	}
	sort.Slice(infos, func(i, j int) bool {
		if ri, rj := rank(infos[i]), rank(infos[j]); ri != rj {
			return ri < rj
		}
		return infos[i].Key < infos[j].Key
	})
	return infos
}

func ratFloat(r *big.Rat) *float64 {
	if r == nil {
		return nil
	}
	f, _ := r.Float64()
	return &f
}

// NewNode returns a valid default node of a native kind. The component kind
// needs a type, so it is refused here: use NewComponent.
func NewNode(kind, id string) (*Node, error) {
	if kind == "component" {
		return nil, fmt.Errorf("builder: a component needs a type, use NewComponent")
	}
	return newNode(kind, id, nil)
}

// NewComponent returns a component node of the given theme section type.
func NewComponent(typ, id string) (*Node, error) {
	return newNode("component", id, map[string]any{"type": typ})
}

func newNode(kind, id string, p map[string]any) (*Node, error) {
	r := reg.Load()
	if _, ok := r.kinds[kind]; !ok {
		return nil, fmt.Errorf("builder: unknown kind %q", kind)
	}
	if !ValidID(id) {
		return nil, fmt.Errorf("builder: bad node id %q", id)
	}
	if p == nil {
		p = cloneMap(defaultsIn(r.kinds, kind))
	}
	n := &Node{ID: id, T: kind, P: p}
	if err := validateProps(r, n); err != nil {
		return nil, fmt.Errorf("builder: %w", err)
	}
	return n, nil
}

// CoerceProp converts a posted form string to the typed value of a prop,
// checked against the kind's schema. An empty string returns nil, meaning
// "delete the prop". Object props take a JSON object.
func CoerceProp(kind, key, raw string) (any, error) {
	r := reg.Load()
	if _, ok := r.kinds[kind]; !ok {
		return nil, fmt.Errorf("builder: unknown kind %q", kind)
	}
	ps, ok := r.schemas[kind].Properties[key]
	if !ok {
		return nil, fmt.Errorf("builder: kind %q has no prop %q", kind, key)
	}
	if raw == "" {
		return nil, nil
	}
	var v any
	switch {
	case len(ps.Enum) > 0:
		v = raw
	case len(ps.Types) == 0:
		return nil, fmt.Errorf("builder: prop %q has no type", key)
	default:
		switch ps.Types[0] {
		case "string":
			v = raw
		case "integer":
			i, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("builder: %s: %q is not a whole number", key, raw)
			}
			v = float64(i)
		case "boolean":
			switch raw {
			case "on", "1", "true":
				v = true
			case "off", "0", "false":
				v = false
			default:
				return nil, fmt.Errorf("builder: %s: %q is not a boolean", key, raw)
			}
		case "object":
			var m map[string]any
			if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
				return nil, fmt.Errorf("builder: %s: not a JSON object", key)
			}
			v = m
		default:
			return nil, fmt.Errorf("builder: prop %q has unsupported type %q", key, ps.Types[0])
		}
	}
	if err := ps.Validate(v); err != nil {
		return nil, fmt.Errorf("builder: %s: %v", key, err)
	}
	return v, nil
}
