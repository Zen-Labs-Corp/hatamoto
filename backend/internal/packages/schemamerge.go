package packages

import (
	"bytes"
	"encoding/json/jsontext"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
)

// SchemaMerge is what applying a plugin's shipped schema to the stored schema
// of a content type it owns does. Only top-level properties are compared.
type SchemaMerge struct {
	Added   []string       // property names the stored schema lacked, in shipped document order
	Labels  []string       // their labels (property "title", else humanised name), same order
	Retyped []string       // in both, shipped "type" differs: kept as stored (sorted)
	Missing []string       // stored only (plugin dropped it, or the owner added it): kept (sorted)
	Merged  jsontext.Value // stored + Added; nil when Added is empty
}

// MergeSchema adds to stored the properties shipped has and stored lacks. It
// never removes or changes a stored property, never touches "required" or any
// other top-level key, and appends added names to x-hm-order when stored has one.
func MergeSchema(stored, shipped jsontext.Value) (SchemaMerge, error) {
	var res SchemaMerge
	var top map[string]jsontext.Value
	if err := jsonx.Unmarshal(stored, &top, jsonx.Stored); err != nil {
		return res, err
	}
	if top == nil {
		top = map[string]jsontext.Value{}
	}
	props, err := propertyMap(top["properties"])
	if err != nil {
		return res, err
	}
	var shippedTop map[string]jsontext.Value
	if err := jsonx.Unmarshal(shipped, &shippedTop, jsonx.Stored); err != nil {
		return res, err
	}
	var entries []jsonx.Entry
	if raw := shippedTop["properties"]; len(raw) > 0 && string(raw) != "null" {
		if entries, err = jsonx.ObjectEntries(raw); err != nil {
			return res, err
		}
	}

	inShipped := map[string]bool{}
	for _, e := range entries {
		inShipped[e.Key] = true
		old, ok := props[e.Key]
		if !ok {
			props[e.Key] = e.Value
			res.Added = append(res.Added, e.Key)
			res.Labels = append(res.Labels, propLabel(e.Key, e.Value))
			continue
		}
		if !bytes.Equal(propType(old), propType(e.Value)) {
			res.Retyped = append(res.Retyped, e.Key)
		}
	}
	for name := range props {
		if !inShipped[name] {
			res.Missing = append(res.Missing, name)
		}
	}
	slices.Sort(res.Retyped)
	slices.Sort(res.Missing)
	if len(res.Added) == 0 {
		return res, nil
	}

	if raw, ok := top["x-hm-order"]; ok {
		var order []string
		if jsonx.Unmarshal(raw, &order, jsonx.Stored) == nil {
			for _, name := range res.Added {
				if !slices.Contains(order, name) {
					order = append(order, name)
				}
			}
			if top["x-hm-order"], err = jsonx.Marshal(order); err != nil {
				return res, err
			}
		}
	}
	if top["properties"], err = jsonx.Marshal(props); err != nil {
		return res, err
	}
	res.Merged, err = jsonx.Marshal(top)
	return res, err
}

func propertyMap(raw jsontext.Value) (map[string]jsontext.Value, error) {
	props := map[string]jsontext.Value{}
	if len(raw) == 0 {
		return props, nil
	}
	if err := jsonx.Unmarshal(raw, &props, jsonx.Stored); err != nil {
		return nil, err
	}
	if props == nil {
		props = map[string]jsontext.Value{}
	}
	return props, nil
}

// propType is the compact "type" member of a property, nil when it has none.
func propType(v jsontext.Value) []byte {
	var doc struct {
		Type jsontext.Value `json:"type"`
	}
	if err := jsonx.UnmarshalPinned(v, &doc); err != nil || len(doc.Type) == 0 {
		return nil
	}
	t := slices.Clone(doc.Type)
	if err := t.Compact(); err != nil {
		return doc.Type
	}
	return t
}

// propLabel is the property title, else the humanised name.
func propLabel(name string, v jsontext.Value) string {
	var doc struct {
		Title string `json:"title"`
	}
	if err := jsonx.UnmarshalPinned(v, &doc); err == nil && doc.Title != "" {
		return doc.Title
	}
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
