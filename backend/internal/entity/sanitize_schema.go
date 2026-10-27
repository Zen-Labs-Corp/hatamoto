package entity

import "sync/atomic"

// FieldKindResolver reports the x-hm-field kind ("richtext", "html", ...) of
// the section-content field at path for a section type, or "" when the type or
// field is unknown. path holds object keys only; array indices are skipped.
type FieldKindResolver func(sectionType string, path []string) string

var fieldKindResolver atomic.Pointer[FieldKindResolver]

// SetFieldKindResolver registers the active theme's field-kind lookup. The
// render engine depends on this package, so the server wires it at boot rather
// than entity importing render. nil clears it.
//
// Save-time schema sanitising exists only while a resolver is registered, and
// the server registers one only when Go theme rendering is on (a theme loaded).
// With rendering off there is no schema to consult, so only the schema-free
// passes run on save; schema-marked fields are then not sanitised until a
// theme is enabled and the content is saved again (render-time sanitising
// still protects the served HTML).
func SetFieldKindResolver(r FieldKindResolver) {
	if r == nil {
		fieldKindResolver.Store(nil)
		return
	}
	fieldKindResolver.Store(&r)
}

// sanitizeSchemaFields sanitizes, on save, every string that the active theme
// schema marks richtext (rich-text policy) or html (html-section policy) in
// attributes.page_sections[].content. Values equal to the stored ones at the
// same section/path are left alone, like the other save-time passes.
func sanitizeSchemaFields(v, old any, changed *bool) {
	rp := fieldKindResolver.Load()
	if rp == nil {
		return
	}
	root, ok := v.(map[string]any)
	if !ok {
		return
	}
	sections, ok := root["page_sections"].([]any)
	if !ok {
		return
	}
	var oldSections []any
	if oldRoot, ok := old.(map[string]any); ok {
		oldSections, _ = oldRoot["page_sections"].([]any)
	}
	for i, item := range sections {
		sec, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := sec["type"].(string)
		content, ok := sec["content"].(map[string]any)
		if !ok || typ == "" {
			continue
		}
		var oldContent any
		if m := matchStoredSection(oldSections, sec, i); m != nil && m["type"] == typ {
			oldContent = m["content"]
		}
		sanitizeSchemaValue(*rp, typ, content, oldContent, nil, "", 0, changed)
	}
}

// sanitizeSchemaValue walks v and returns it with every string under a
// richtext/html field sanitised. kind is the richtext/html kind inherited from
// an enclosing field; when a field of that kind holds an array or object
// instead of a string, every string leaf beneath it gets that policy, so no
// raw HTML survives whatever shape the client sent.
func sanitizeSchemaValue(resolve FieldKindResolver, typ string, v, old any, path []string, kind string, depth int, changed *bool) any {
	if depth > maxSanitizeDepth {
		return v
	}
	switch t := v.(type) {
	case string:
		if prev, same := old.(string); same && prev == t {
			return v
		}
		var clean string
		switch kind {
		case "richtext":
			clean = SanitizeRichText(t)
		case "html":
			clean = SanitizeHTMLSectionBody(t)
		default:
			return v
		}
		if clean != t {
			*changed = true
		}
		return clean
	case map[string]any:
		oldMap, _ := old.(map[string]any)
		for k, child := range t {
			p := append(path[:len(path):len(path)], k)
			t[k] = sanitizeSchemaValue(resolve, typ, child, oldMap[k], p, childKind(resolve, typ, p, kind), depth+1, changed)
		}
	case []any:
		oldArr, _ := old.([]any)
		for i, child := range t {
			var oc any
			if i < len(oldArr) {
				oc = oldArr[i]
			}
			t[i] = sanitizeSchemaValue(resolve, typ, child, oc, path, kind, depth+1, changed)
		}
	}
	return v
}

// childKind is the richtext/html kind in force at path: an inherited kind
// sticks, otherwise the schema decides.
func childKind(resolve FieldKindResolver, typ string, path []string, inherited string) string {
	if inherited != "" {
		return inherited
	}
	if k := resolve(typ, path); k == "richtext" || k == "html" {
		return k
	}
	return ""
}
