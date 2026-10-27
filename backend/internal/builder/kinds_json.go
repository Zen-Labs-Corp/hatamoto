package builder

import "encoding/json"

// BootKind is the JSON view of a KindInfo: what the admin builder boots from
// and what the MCP builder_kinds tool returns.
type BootKind struct {
	Kind     string         `json:"kind"`
	Children bool           `json:"children"`
	Props    []BootProp     `json:"props"`
	Default  map[string]any `json:"default,omitempty"`
	// Widget kinds: palette label, hint, group and the seeded children.
	Label string     `json:"label,omitempty"`
	Hint  string     `json:"hint,omitempty"`
	Group string     `json:"group,omitempty"`
	Seed  []SeedNode `json:"seed,omitempty"`
	// Hidden kinds (ref, slot) are not offered by the stock palette.
	Hidden bool `json:"hidden,omitempty"`
	// Display is the kind's default CSS display (see KindInfo.Display).
	Display string `json:"display,omitempty"`
	// Universal lists the element-setting props (anchor, aria, tip, ...) the kind takes.
	Universal []string `json:"u,omitempty"`
	// Interactive kinds may not sit inside a linked container (the editor checks it before saving).
	Interactive bool `json:"ia,omitempty"`
	// OnlyIn is the one parent kind the kind may sit under; set by BootKinds(true) only.
	OnlyIn string `json:"onlyIn,omitempty"`
}

// BootProp is the JSON view of a PropInfo.
type BootProp struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	Enum     []any    `json:"enum,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Required bool     `json:"required,omitempty"`
	// Widget is the inspector control hint, MaxLength the string limit.
	Widget    string `json:"widget,omitempty"`
	MaxLength int    `json:"maxLength,omitempty"`
	// Bind is the class of source the prop accepts (text, url, image), empty when it cannot be bound.
	Bind string `json:"bind,omitempty"`
	// Pattern is the regular expression a string value must match.
	Pattern string `json:"pattern,omitempty"`
}

// UniversalProps describes the element-setting props every kind may take
// (a kind lists the ones it takes in BootKind.Universal), with their limits.
func UniversalProps() []BootProp {
	const noControl = `^[^\x00-\x1f\x7f]*$`
	return []BootProp{
		{Key: "preset", Type: "string", Pattern: presetRe.String()},
		{Key: "navlabel", Type: "string", MaxLength: 80, Pattern: noControl},
		{Key: "anchor", Type: "string", Pattern: anchorPattern},
		{Key: "aria", Type: "string", MaxLength: 200, Pattern: noControl},
		{Key: "tip", Type: "string", MaxLength: 200, Pattern: noControl},
	}
}

// BootKinds lists every kind in palette order as JSON-ready values. The admin
// boot leaves withOnlyIn false (its payload is fixed); MCP asks for the rule.
func BootKinds(withOnlyIn bool) []BootKind {
	out := []BootKind{}
	for _, k := range Kinds() {
		bk := BootKind{Kind: k.Kind, Children: k.Children, Default: k.Default, Props: []BootProp{}, Label: k.Label, Hint: k.Hint, Group: k.Group, Seed: k.Seed, Hidden: k.Hidden, Display: k.Display, Universal: k.Universal, Interactive: k.Interactive}
		if withOnlyIn {
			bk.OnlyIn = k.OnlyIn
		}
		for _, p := range k.Props {
			bk.Props = append(bk.Props, BootProp{Key: p.Key, Type: p.Type, Enum: p.Enum, Min: p.Min, Max: p.Max, Required: p.Required, Widget: p.Widget, MaxLength: p.MaxLength, Bind: p.Bind})
		}
		out = append(out, bk)
	}
	return out
}

// ReadableNodes returns the top-level nodes of a tree template's content that
// this build accepts, each checked as a doc of its own.
func ReadableNodes(content []byte) []*Node {
	var c struct {
		Nodes []*Node `json:"nodes"`
	}
	if json.Unmarshal(content, &c) != nil {
		return nil
	}
	var out []*Node
	for _, n := range c.Nodes {
		if n == nil {
			continue
		}
		raw, err := json.Marshal(&Doc{V: 2, Nodes: []*Node{n}})
		if err != nil {
			continue
		}
		if _, err := Parse(raw); err == nil {
			out = append(out, n)
		}
	}
	return out
}
