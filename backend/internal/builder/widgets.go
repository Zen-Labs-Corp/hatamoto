package builder

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"sort"
	"strings"
)

// widgetFS holds widgets/<kind>/schema.json. Each file is the JSON Schema of
// the node's props plus an "x-hm" object of palette metadata; property
// schemas may carry "x-hm-widget", an inspector control hint. The view side
// of a widget lives in the render package (this package stays a leaf).
//
//go:embed widgets
var widgetFS embed.FS

// widgetGroups are the allowed x-hm.group values, in palette order.
var widgetGroups = []string{"Layout", "Content", "Media", "Interactive", "Data"}

// SeedNode is a child inserted with a freshly dropped widget.
type SeedNode struct {
	T string            `json:"t"`
	P map[string]any    `json:"p,omitempty"`
	B map[string]string `json:"b,omitempty"`
	C []SeedNode        `json:"c,omitempty"`
}

// maxSeedDepth bounds nested seeds (the seed's own children count as depth 1).
const maxSeedDepth = 3

func (s SeedNode) clone() SeedNode {
	c := SeedNode{T: s.T, P: cloneMap(s.P), B: maps.Clone(s.B)}
	for _, x := range s.C {
		c.C = append(c.C, x.clone())
	}
	return c
}

// seedCount is the number of nodes of a seed forest, or -1 when it nests
// deeper than maxSeedDepth.
func seedCount(ss []SeedNode, depth int) int {
	if len(ss) > 0 && depth > maxSeedDepth {
		return -1
	}
	n := 0
	for _, s := range ss {
		c := seedCount(s.C, depth+1)
		if c < 0 {
			return -1
		}
		n += 1 + c
	}
	return n
}

// seedUnknown returns the first seed kind that is neither native nor a widget.
func seedUnknown(ss []SeedNode, known func(string) bool) string {
	for _, s := range ss {
		if !known(s.T) {
			return s.T
		}
		if u := seedUnknown(s.C, known); u != "" {
			return u
		}
	}
	return ""
}

// widgetMeta is the parsed x-hm object.
type widgetMeta struct {
	Label    string         `json:"label"`
	Hint     string         `json:"hint"`
	Group    string         `json:"group"`
	Order    int            `json:"order"`
	Children bool           `json:"children"`
	OnlyIn   string         `json:"only_in"` // the one parent kind this kind may sit under
	Seed     []SeedNode     `json:"seed"`
	Default  map[string]any `json:"default"`
}

// widgetSchema is the part of schema.json the loader reads itself;
// the jsonschema compiler ignores unknown keywords.
type widgetSchema struct {
	Meta       *widgetMeta `json:"x-hm"`
	Properties map[string]struct {
		Type   any    `json:"type"`
		Widget string `json:"x-hm-widget"`
		Bind   string `json:"x-hm-bind"`
	} `json:"properties"`
}

// loadWidgets reads every widgets/<kind>/schema.json of fsys. It returns an error
// and leaves it to the caller to decide what a bad widget means.
func loadWidgets(fsys fs.FS, native map[string]kindSpec) (map[string]kindSpec, error) {
	dirs, err := fs.ReadDir(fsys, "widgets")
	if err != nil {
		return nil, err
	}
	out := map[string]kindSpec{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		if strings.Contains(name, "_") {
			return nil, fmt.Errorf("widget %q: \"_\" is reserved for plugin widgets", name)
		}
		raw, err := fs.ReadFile(fsys, "widgets/"+name+"/schema.json")
		if err != nil {
			return nil, fmt.Errorf("widget %q: %w", name, err)
		}
		spec, err := parseWidget(name, raw)
		if err != nil {
			return nil, fmt.Errorf("widget %q: %w", name, err)
		}
		if _, dup := native[name]; dup {
			return nil, fmt.Errorf("widget %q: collides with a native kind", name)
		}
		out[name] = spec
	}
	for name, spec := range out {
		known := func(k string) bool {
			_, isNative := native[k]
			_, isWidget := out[k]
			return isNative || isWidget
		}
		if u := seedUnknown(spec.meta.Seed, known); u != "" {
			return nil, fmt.Errorf("widget %q: seed kind %q is unknown", name, u)
		}
	}
	return out, nil
}

func parseWidget(name string, raw []byte) (kindSpec, error) {
	return parseWidgetSpec(name, raw, false)
}

// parseWidgetSpec parses one widget schema. A plugin widget (pack) takes its
// group from PackGroup, whatever x-hm.group says.
func parseWidgetSpec(name string, raw []byte, pack bool) (kindSpec, error) {
	if !ValidID(name) {
		return kindSpec{}, fmt.Errorf("bad kind name")
	}
	var ws widgetSchema
	if err := json.Unmarshal(raw, &ws); err != nil {
		return kindSpec{}, err
	}
	if ws.Meta == nil {
		return kindSpec{}, fmt.Errorf("missing x-hm")
	}
	m := *ws.Meta
	if m.Label == "" {
		return kindSpec{}, fmt.Errorf("x-hm.label is required")
	}
	ok := pack
	if pack {
		m.Group = PackGroup
	}
	for _, g := range widgetGroups {
		ok = ok || g == m.Group
	}
	if !ok {
		return kindSpec{}, fmt.Errorf("x-hm.group %q is not one of %s", m.Group, strings.Join(widgetGroups, ", "))
	}
	if len(m.Seed) > 0 && !m.Children {
		return kindSpec{}, fmt.Errorf("x-hm.seed needs x-hm.children")
	}
	if n := seedCount(m.Seed, 1); n < 0 {
		return kindSpec{}, fmt.Errorf("x-hm.seed nests deeper than %d levels", maxSeedDepth)
	} else if n >= MaxNodes {
		return kindSpec{}, fmt.Errorf("x-hm.seed has too many nodes")
	}
	if m.OnlyIn != "" && !ValidID(m.OnlyIn) {
		return kindSpec{}, fmt.Errorf("bad x-hm.only_in")
	}
	widgets := map[string]string{}
	bind, err := bindProps(ws)
	if err != nil {
		return kindSpec{}, err
	}
	for key, p := range ws.Properties {
		if p.Type == "array" {
			return kindSpec{}, fmt.Errorf("prop %q: array props are not supported, use child nodes", key)
		}
		if ts, ok := p.Type.([]any); ok {
			for _, t := range ts {
				if t == "array" {
					return kindSpec{}, fmt.Errorf("prop %q: array props are not supported, use child nodes", key)
				}
			}
		}
		if p.Widget != "" {
			widgets[key] = p.Widget
		}
	}
	order, err := propertyOrder(raw)
	if err != nil {
		return kindSpec{}, err
	}
	return kindSpec{children: m.Children, schema: string(raw), meta: &m, order: order, widgets: widgets, bind: bind}, nil
}

// propertyOrder returns the keys of the top-level "properties" object in
// document order (a Go map loses it).
func propertyOrder(raw []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token() // {
	if err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("schema is not an object")
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if k != "properties" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
			continue
		}
		if tok, err = dec.Token(); err != nil || tok != json.Delim('{') {
			return nil, fmt.Errorf("properties is not an object")
		}
		var keys []string
		for dec.More() {
			pk, err := dec.Token()
			if err != nil {
				return nil, err
			}
			keys = append(keys, pk.(string))
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
		}
		return keys, nil
	}
	return nil, nil
}

// mergeWidgets returns native plus the embedded widgets. A broken embedded
// widget is a build defect, so it panics at init like a bad native schema.
func mergeWidgets(native map[string]kindSpec) map[string]kindSpec {
	ws, err := loadWidgets(widgetFS, native)
	if err != nil {
		panic("builder: " + err.Error())
	}
	all := make(map[string]kindSpec, len(native)+len(ws))
	for k, v := range native {
		var ws widgetSchema
		if err := json.Unmarshal([]byte(v.schema), &ws); err != nil {
			panic(fmt.Sprintf("builder: schema for kind %q: %v", k, err))
		}
		b, err := bindProps(ws)
		if err != nil {
			panic(fmt.Sprintf("builder: schema for kind %q: %v", k, err))
		}
		v.bind = b
		all[k] = v
	}
	for k, v := range ws {
		all[k] = v
	}
	return all
}

// widgetRank orders widget kinds: group, then x-hm.order, then name.
func widgetRank(kinds map[string]kindSpec, names []string) {
	gi := func(g string) int {
		for i, x := range widgetGroups {
			if x == g {
				return i
			}
		}
		return len(widgetGroups)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := kinds[names[i]].meta, kinds[names[j]].meta
		if a == nil || b == nil {
			if (a == nil) != (b == nil) {
				return a == nil // native kinds first
			}
			return names[i] < names[j]
		}
		if gi(a.Group) != gi(b.Group) {
			return gi(a.Group) < gi(b.Group)
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return names[i] < names[j]
	})
}

// NewTree returns a valid default node of kind with its seeded children,
// all with fresh ids. Like NewNode, a component is refused.
func NewTree(kind string) (Node, error) {
	id, err := freshID()
	if err != nil {
		return Node{}, err
	}
	n, err := NewNode(kind, id)
	if err != nil {
		return Node{}, err
	}
	if k, _ := kindOf(kind); k.meta != nil {
		m := k.meta
		for _, sn := range m.Seed {
			c, err := seedTree(sn)
			if err != nil {
				return Node{}, err
			}
			n.C = append(n.C, c)
		}
	}
	if err := (&Doc{V: 2, Nodes: []*Node{n}}).Validate(); err != nil {
		return Node{}, err
	}
	return *n, nil
}

// seedTree builds a node, with fresh ids, from a seed and its seeded children.
func seedTree(s SeedNode) (*Node, error) {
	id, err := freshID()
	if err != nil {
		return nil, err
	}
	p := cloneMap(defaultsOf(s.T))
	if p == nil && len(s.P) > 0 {
		p = map[string]any{}
	}
	for k, v := range s.P {
		p[k] = v
	}
	n, err := newNode(s.T, id, p)
	if err != nil {
		return nil, err
	}
	n.B = maps.Clone(s.B)
	for _, c := range s.C {
		cn, err := seedTree(c)
		if err != nil {
			return nil, err
		}
		n.C = append(n.C, cn)
	}
	return n, nil
}

func freshID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "n" + hex.EncodeToString(b[:]), nil
}
