package schemaform

import "encoding/json"

// TopField describes one top-level property of a section schema for the
// builder Navigator: the content key, the label the Inspector form shows and a
// coarse kind the Navigator maps to an icon.
type TopField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	// Alias lists the x-hm-alias content keys the Inspector falls back to.
	Alias []string `json:"alias,omitempty"`
	// Setting marks a configuration field (enum, boolean, number, url): the
	// Navigator shows no row for it, the Content tab still edits it.
	Setting bool `json:"setting,omitempty"`
}

// TopFields lists the top-level properties of a schema in the order the
// Inspector form shows them (ungrouped first, then each group). It returns nil
// when the schema cannot be parsed or has no properties.
func TopFields(raw json.RawMessage) []TopField {
	n, err := ParseSchema(raw)
	if err != nil || n == nil {
		return nil
	}
	props := groupedProps(n.Props)
	if len(props) == 0 {
		return nil
	}
	out := make([]TopField, 0, len(props))
	for _, p := range props {
		out = append(out, TopField{Key: p.Name, Label: p.Node.label(p.Name), Kind: fieldKind(p.Node), Alias: p.Node.Alias, Setting: isSetting(p.Node)})
	}
	return out
}

// fieldKind names the kind of a property: richtext, image, link, code, list,
// object or text.
func fieldKind(n *Node) string {
	switch {
	case n.XHm == "richtext" || n.XHm == "html" || n.Format == "richtext":
		return "richtext"
	case n.XHm == "image" || n.XMedia:
		return "image"
	case n.XHm == "url" || n.Format == "uri":
		return "link"
	case n.XHm == "code" || n.XHm == "css":
		return "code"
	case n.Type == "array" || n.XHm == "lines":
		return "list"
	case n.Type == "object":
		return "object"
	}
	return "text"
}

// isSetting reports whether a property is configuration rather than content.
func isSetting(n *Node) bool {
	return len(n.Enum) > 0 || n.Type == "boolean" || n.Type == "integer" || n.Type == "number" || fieldKind(n) == "link"
}
