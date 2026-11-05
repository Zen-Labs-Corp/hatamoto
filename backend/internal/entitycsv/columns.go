// Package entitycsv exports the entities of one content type as CSV and
// imports them back through entity.Service.Import.
package entitycsv

import (
	"encoding/json/jsontext"
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
)

// Column is one CSV column. ID is also the header cell, so an exported file
// maps to the same targets when it is imported again.
type Column struct {
	ID    string
	Label string
	// Default is whether the export chooser ticks the column at first.
	Default bool
}

// Fixed column ids.
const (
	ColKey         = "key"
	ColName        = "name"
	ColSlug        = "slug"
	ColStatus      = "status"
	ColPublishedAt = "published_at"
	ColLocale      = "locale"
	ColParentKey   = "parent_key"
	ColOrder       = "order"
	ColID          = "id"

	// AttrPrefix starts the id of an attribute column: attr:a.b.
	AttrPrefix = "attr:"
	// PageDocAttr is the one attribute exported as an opaque JSON cell.
	PageDocAttr = "page_doc"

	maxDepth = 3
)

// FixedColumns are the columns every type has. order is exported but not
// importable (rows append); id is informational.
var FixedColumns = []Column{
	{ColKey, "Key", true},
	{ColName, "Name", true},
	{ColSlug, "Slug", true},
	{ColStatus, "Status", true},
	{ColPublishedAt, "Published at", true},
	{ColLocale, "Locale", true},
	{ColParentKey, "Parent key", true},
	{ColOrder, "Order (not imported)", false},
	{ColID, "ID (not imported)", false},
}

// AttributeColumns lists the attribute columns of a type from its JSON schema:
// every property, nested objects as a.b down to three levels. Arrays and
// untyped values are one column. page_doc is offered as one opaque JSON
// column, off by default.
func AttributeColumns(schema jsontext.Value) []Column {
	var out []Column
	walkProperties(schema, "", 1, func(path string, isPageDoc bool) {
		label := path
		if isPageDoc {
			label = path + " (JSON, builder tree)"
		}
		out = append(out, Column{ID: AttrPrefix + path, Label: label, Default: !isPageDoc})
	})
	return out
}

// AvailableColumns is FixedColumns followed by AttributeColumns.
func AvailableColumns(schema jsontext.Value) []Column {
	return append(append([]Column(nil), FixedColumns...), AttributeColumns(schema)...)
}

// PickColumns returns the columns for ids, in the order given. An id the type
// does not offer, or a repeated one, is an error.
func PickColumns(schema jsontext.Value, ids []string) ([]Column, error) {
	byID := map[string]Column{}
	for _, c := range AvailableColumns(schema) {
		byID[c.ID] = c
	}
	seen := map[string]bool{}
	out := make([]Column, 0, len(ids))
	for _, id := range ids {
		c, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown column %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("column %q is chosen twice", id)
		}
		seen[id] = true
		out = append(out, c)
	}
	return out, nil
}

// walkProperties calls fn for each leaf property path of a JSON schema.
func walkProperties(schema jsontext.Value, prefix string, depth int, fn func(path string, isPageDoc bool)) {
	var doc struct {
		Properties jsontext.Value `json:"properties"`
	}
	if len(schema) == 0 || jsonx.Unmarshal(schema, &doc, jsonx.StoredLoose) != nil || len(doc.Properties) == 0 {
		return
	}
	entries, err := jsonx.ObjectEntries(doc.Properties)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := prefix + e.Key
		if depth == 1 && e.Key == PageDocAttr {
			fn(path, true)
			continue
		}
		if depth < maxDepth && schemaType(e.Value) == "object" && hasProperties(e.Value) {
			walkProperties(e.Value, path+".", depth+1, fn)
			continue
		}
		fn(path, false)
	}
}

// schemaType is the "type" of a property schema when it is one string.
func schemaType(prop jsontext.Value) string {
	var p struct {
		Type string `json:"type"`
	}
	if jsonx.Unmarshal(prop, &p, jsonx.StoredLoose) != nil {
		return ""
	}
	return p.Type
}

func hasProperties(prop jsontext.Value) bool {
	var p struct {
		Properties map[string]jsontext.Value `json:"properties"`
	}
	return jsonx.Unmarshal(prop, &p, jsonx.StoredLoose) == nil && len(p.Properties) > 0
}

// attrPath returns the dotted path of an attr: column id.
func attrPath(id string) (string, bool) {
	return strings.CutPrefix(id, AttrPrefix)
}
