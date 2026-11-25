package schemaform

import (
	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/entity"
)

// Attr is a top-level schema property a query loop can sort or filter by.
type Attr struct {
	Key   string   `json:"key"`
	Label string   `json:"label"`
	Type  string   `json:"type"` // date, number, enum or text
	Enum  []string `json:"enum,omitempty"`
}

// Attrs lists raw's usable top-level properties in schema order: dates
// (format date only), numbers, enums and plain text. Secrets, rich kinds,
// date-times and keys a loop cannot address are left out.
func Attrs(raw jsontext.Value) []Attr {
	n, err := ParseSchema(raw)
	if err != nil || n == nil {
		return nil
	}
	var out []Attr
	for _, p := range n.Props {
		nd := p.Node
		if nd.WriteOnly || fieldKind(nd) != "text" || !entity.ValidAttrKey(p.Name) {
			continue
		}
		a := Attr{Key: p.Name, Label: nd.label(p.Name)}
		switch {
		case len(nd.Enum) > 0:
			a.Type, a.Enum = "enum", nd.Enum
		case nd.Type == "number" || nd.Type == "integer":
			a.Type = "number"
		case nd.Type == "string" && nd.Format == "date":
			a.Type = "date"
		case nd.Type == "string" && nd.Format == "":
			a.Type = "text"
		default:
			continue
		}
		out = append(out, a)
	}
	return out
}
