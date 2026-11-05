package entity

import (
	"bytes"
	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/jsonx"
)

// postEditorSlugs are the built-in entity types whose body is written in the
// blog editor, not the page editor, even when they carry sections.
var postEditorSlugs = map[string]bool{"post": true, "article": true, "blog_post": true, "blog": true}

// IsPostEditorSchema reports whether sc is edited in the post editor: a
// built-in post slug, or a schema whose settings.editor is "post".
func IsPostEditorSchema(sc *EntitySchema) bool {
	if sc == nil {
		return false
	}
	if postEditorSlugs[sc.Slug] {
		return true
	}
	st, _ := sc.URLSettings()
	return st.Editor == EditorPost
}

// EligibleForPageDoc reports whether entities of sc take part in page_sections
// to page_doc conversion: everything except post-editor types and persons,
// whose page_sections (if any) is ordinary data.
func EligibleForPageDoc(sc *EntitySchema) bool {
	return !IsPostEditorSchema(sc) && (sc == nil || sc.Slug != personSchemaSlug)
}

// LegacyPageDoc converts the page_sections stored in attrs into a doc, the same
// deterministic conversion a write applies, without touching anything. ok is
// false when attrs has no page_sections array or it does not convert. The
// caller decides whether a page_doc also exists.
func LegacyPageDoc(attrs jsontext.Value) (*builder.Doc, bool) {
	doc, err := LegacyPageDocErr(attrs)
	return doc, doc != nil && err == nil
}

// LegacyPageDocErr is LegacyPageDoc with the reason: (nil, nil) when attrs has
// no page_sections array, the conversion error when it does not convert.
func LegacyPageDocErr(attrs jsontext.Value) (*builder.Doc, error) {
	m, ok := decodeObject(attrs)
	if !ok {
		return nil, nil
	}
	if _, isArr := m[keySections].([]any); !isArr {
		return nil, nil
	}
	return docFromSectionsValue(m[keySections])
}

// DerivedAttributes is the read-side view of a page's attributes for consumers
// that want section-shaped data (public JSON, plugins, hooks): page_sections is
// the sections of page_doc as of now, never the frozen stored mirror. raw is
// returned untouched when it has no page_doc; an invalid one drops page_sections (fail closed, as the renderer does); a builder-only doc (v1
// cannot express it) has no page_sections, so the key is removed. Pure.
func DerivedAttributes(raw jsontext.Value) jsontext.Value {
	var top map[string]jsontext.Value // exact keys
	if jsonx.Unmarshal(raw, &top, jsonx.StoredLoose) != nil || len(top[keyDoc]) == 0 {
		return raw
	}
	doc, err := builder.ParseExact(top[keyDoc])
	if err != nil {
		if string(top[keyDoc]) == "null" {
			return raw
		}
		// The renderer shows no body for an invalid doc; the frozen mirror must
		// not resurface elsewhere.
		delete(top, keySections)
		if b, err := jsonx.Marshal(top, jsonx.HTMLSafe); err == nil {
			return b
		}
		return raw
	}
	if ss, ok := builder.LegacySections(doc); ok {
		b, err := jsonx.Marshal(builder.SectionMaps(ss), jsonx.HTMLSafe)
		if err != nil {
			return raw
		}
		top[keySections] = b
	} else {
		delete(top, keySections)
	}
	b, err := jsonx.Marshal(top, jsonx.HTMLSafe)
	if err != nil {
		return raw
	}
	return b
}

// convertUnconverted is the lazy conversion of a write that carries no
// attributes of its own (a rename, a status change): the stored page_sections
// of an eligible page without a doc become its doc. changed is false for every
// other entity, and for a page whose sections do not convert (it stays as it is).
func convertUnconverted(e *Entity) (jsontext.Value, bool) {
	if e == nil || !bytes.Contains(e.Attributes, []byte(keySections)) || hasDocKey(e.Attributes) {
		return nil, false
	}
	out, err := normalizeDocRaw(docWrite, e.Attributes, e.Attributes, EligibleForPageDoc(e.Schema))
	if err != nil || bytes.Equal(out, e.Attributes) {
		return nil, false
	}
	return out, true
}
