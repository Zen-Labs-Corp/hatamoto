package render

import (
	"html"
	"html/template"
	"strconv"
	"strings"
)

// ContractVersion is emitted as data-hm-contract on <html>; bump it when the
// data-hm-* marker contract changes incompatibly (canvas editor depends on it).
const ContractVersion = "1"

// The marker contract is isolated here (see docs/THEMES.md):
//
//	<section data-hm-section="{section.id}" data-hm-type="{type}">
//	any editable text element: data-hm-field="{canonical json path}"
//	<main data-hm-entity="{entity id}">
//
// Values are attribute-escaped; templates never build markers by hand.

// sectionMarker renders data-hm-section/data-hm-type for a section. It accepts
// a preparedSection (what templates receive) or a Section.
func sectionMarker(v any) template.HTMLAttr {
	var s Section
	switch t := v.(type) {
	case preparedSection:
		s = t.Section
	case *preparedSection:
		s = t.Section
	case Section:
		s = t
	case *Section:
		s = *t
	}
	attr := `data-hm-section="` + html.EscapeString(s.ID) + `" data-hm-type="` + html.EscapeString(s.Type) + `"`
	if sectionAnchorOK(s.ID) {
		// A readable section id doubles as an in-page anchor (/#quickstart).
		attr = `id="` + s.ID + `" ` + attr
	}
	return template.HTMLAttr(attr) // #nosec G203 -- values escaped or validated
}

// sectionAnchorOK reports whether a section id can be emitted as an HTML id:
// a short slug that cannot collide with ids the theme itself uses.
func sectionAnchorOK(id string) bool {
	if id == "" || len(id) > 64 || strings.HasPrefix(id, "hm-") {
		return false
	}
	switch id {
	case "main", "main-content", "content", "nav", "top", "header", "footer":
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '_'):
		default:
			return false
		}
	}
	return true
}

// fieldMarker renders data-hm-field for a canonical JSON path given as
// segments: fieldMarker("faqs", 2, "answer") -> data-hm-field="faqs.2.answer".
func fieldMarker(path ...any) template.HTMLAttr {
	parts := make([]string, 0, len(path))
	for _, p := range path {
		switch t := p.(type) {
		case string:
			parts = append(parts, t)
		case int:
			parts = append(parts, strconv.Itoa(t))
		case int64:
			parts = append(parts, strconv.FormatInt(t, 10))
		case uint:
			parts = append(parts, strconv.FormatUint(uint64(t), 10))
		default:
			parts = append(parts, "")
		}
	}
	return template.HTMLAttr(`data-hm-field="` + html.EscapeString(strings.Join(parts, ".")) + `"`) // #nosec G203 -- escaped
}

// entityMarker renders data-hm-entity for <main>.
func entityMarker(id string) template.HTMLAttr {
	if id == "" {
		return ""
	}
	return template.HTMLAttr(`data-hm-entity="` + html.EscapeString(id) + `"`) // #nosec G203 -- escaped
}

// nodeMarker renders data-hm-node for a builder node. Canvas output only: public
// HTML carries no data-hm-* node markers.
func nodeMarker(id string) string {
	return `data-hm-node="` + html.EscapeString(id) + `"`
}

// kindMarker renders data-hm-t, the node kind (widget type or section type) the
// canvas labels it with. Canvas output only, next to data-hm-node.
func kindMarker(kind string) string {
	return `data-hm-t="` + html.EscapeString(kind) + `"`
}
