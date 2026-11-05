package searchreplace

import (
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// Scope says which kind of attribute a string was found in. It is a bit set
// when passed to Walk.
type Scope uint8

const (
	// ScopeContent is the body text: content, body and the page builder doc.
	ScopeContent Scope = 1 << iota
	// ScopeSEO is the SEO attributes.
	ScopeSEO
	// ScopeOther is every other string attribute.
	ScopeOther
	// ScopeMenus is menu item labels and URLs. Walk never reports it; the
	// service uses it to choose whether menus are searched.
	ScopeMenus
)

// FieldHit is a hit with where it was found. Path is the "/"-joined keys and
// indices from the attributes root (a "/" or "~" inside a key is written
// "~1" or "~0").
type FieldHit struct {
	Path  string
	Scope Scope
	Hit
}

// Kinds looks up the kind of a field from its schema. Either func may be nil.
// Only "richtext" and "html" matter: they make a string markup. A field with
// no kind keeps the key-name rule (content, body, html).
type Kinds struct {
	// Attr is the kind of the entity attribute at path (object keys only,
	// array indices skipped).
	Attr func(path []string) string
	// Component is the kind of the content field at path of a page builder
	// component of the given type.
	Component entity.FieldKindResolver
}

// markup reports whether the schema marks the field at keys as rich text or
// HTML. A kind on an enclosing field sticks, as in the save-time sanitiser.
func (k *Kinds) markup(inComp bool, comp string, keys []string) bool {
	if k == nil {
		return false
	}
	for n := len(keys); n > 0; n-- {
		var kind string
		switch {
		case inComp && k.Component != nil:
			kind = k.Component(comp, keys[:n])
		case !inComp && k.Attr != nil:
			kind = k.Attr(keys[:n])
		}
		if kind == "richtext" || kind == "html" {
			return true
		}
	}
	return false
}

// walker visits the strings of one attributes object.
type walker struct {
	o      Options
	scopes Scope
	pick   func(path string, i int) bool
	kinds  *Kinds
	inComp bool   // visiting the content of a component
	comp   string // its type
	hits   []FieldHit
	n      int // replacements
}

// shape tells how far the walk may descend below a page_doc key. The builder
// doc holds ids, kinds, styles and bindings next to the text, none of which
// is ever edited: only the text props of a node (p) are read, through nodes
// and children.
type shape uint8

const (
	shapeFree     shape = iota // every string below
	shapeDoc                   // the page_doc object: only "nodes"
	shapeNodeList              // an array of nodes
	shapeNode                  // a node: only "p" and "c"
)

// Walk finds o.Find in the string values of an attributes object, limited to
// the given scopes. With pick == nil it only collects hits. Otherwise it
// replaces the occurrences pick accepts and returns the new attributes; the
// input bytes are returned untouched when nothing was replaced.
//
// page_sections (the legacy section list) is skipped: it converts to a page
// doc on the next write. Map keys, numbers and booleans are never edited.
func Walk(attrs jsontext.Value, o Options, scopes Scope, kinds *Kinds, pick func(path string, i int) bool) (out jsontext.Value, hits []FieldHit, replaced int, err error) {
	if o.Find == "" {
		return attrs, nil, 0, nil
	}
	var root map[string]any
	if err := jsonx.Unmarshal(attrs, &root, jsonx.ExactNumbers); err != nil || root == nil {
		return attrs, nil, 0, nil // not an object: nothing to search
	}
	w := &walker{o: o, scopes: scopes, pick: pick, kinds: kinds}
	for k, v := range root {
		scope, ok := classify(k)
		if !ok || scope&scopes == 0 {
			continue
		}
		sh := shapeFree
		if k == "page_doc" {
			sh = shapeDoc
		}
		root[k] = w.visit(v, []string{k}, "/"+escapeKey(k), scope, sh, isHTMLKey(k))
	}
	sortHits(w.hits)
	if w.n == 0 {
		return attrs, w.hits, 0, nil
	}
	// v1 left <, > and & alone but still escaped U+2028 and U+2029.
	buf, err := jsonx.Marshal(root, jsonx.Output, jsontext.EscapeForJS(true))
	if err != nil {
		return attrs, nil, 0, fmt.Errorf("searchreplace: encode attributes: %w", err)
	}
	return buf, w.hits, w.n, nil
}

// classify maps a top-level attribute key to its scope; ok is false for keys
// that are never searched.
func classify(k string) (Scope, bool) {
	switch {
	case k == "page_sections":
		return 0, false
	case k == "content", k == "body", k == "page_doc":
		return ScopeContent, true
	case entity.IsSEOAttr(k):
		return ScopeSEO, true
	}
	return ScopeOther, true
}

func escapeKey(k string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(k)
}

func (w *walker) visit(v any, keys []string, path string, scope Scope, sh shape, isHTML bool) any {
	switch x := v.(type) {
	case string:
		if sh != shapeFree {
			return x
		}
		return w.str(x, path, scope, isHTML || w.kinds.markup(w.inComp, w.comp, keys))
	case map[string]any:
		kind, _ := x["t"].(string)
		for k, child := range x {
			csh, ok := childShape(sh, k)
			if !ok {
				continue
			}
			if sh == shapeNode && k == "p" {
				if props, ok := child.(map[string]any); ok {
					w.visitProps(kind, props, path+"/p", scope)
				}
				continue
			}
			x[k] = w.visit(child, append(keys[:len(keys):len(keys)], k), path+"/"+escapeKey(k), scope, csh, isHTMLKey(k))
		}
		return x
	case []any:
		for i, child := range x {
			csh := sh
			if sh == shapeNodeList {
				csh = shapeNode
			}
			x[i] = w.visit(child, keys, path+"/"+strconv.Itoa(i), scope, csh, isHTML)
		}
		return x
	}
	return v
}

// childShape is the shape of the value under key k, and whether to visit it.
func childShape(sh shape, k string) (shape, bool) {
	switch sh {
	case shapeFree:
		return shapeFree, true
	case shapeDoc:
		return shapeNodeList, k == "nodes"
	case shapeNode:
		switch k {
		case "p": // visited by visitProps
			return shapeFree, true
		case "c":
			return shapeNodeList, true
		}
	}
	return 0, false
}

// isHTMLKey says a string under key k is markup: the rich-text body and the
// html prop. Every other string is plain text.
func isHTMLKey(k string) bool {
	return k == "content" || k == "body" || k == "html"
}

// textProps are the node props that hold visible text or a link, besides the
// ones a kind binds as text or url. Everything else under p is structure: the
// component type, preset, anchor, rel, target, tag, layout, ref.block, ids.
var textProps = map[string]bool{
	"text": true, "html": true, "alt": true, "caption": true, "title": true,
	"aria": true, "tip": true, "href": true, "src": true,
}

// freeProps are the component props that hold content objects.
var freeProps = map[string]bool{"content": true, "extra": true}

// visitProps edits the text props of one node, in place.
func (w *walker) visitProps(kind string, props map[string]any, path string, scope Scope) {
	for k, v := range props {
		p := path + "/" + escapeKey(k)
		switch {
		case kind == "component" && freeProps[k]:
			if k == "content" {
				w.inComp = true
				w.comp, _ = props["type"].(string)
			}
			props[k] = w.visit(v, nil, p, scope, shapeFree, false)
			w.inComp, w.comp = false, ""
		case textProps[k] || builder.PropBindClass(kind, k) == "text" || builder.PropBindClass(kind, k) == "url":
			if s, ok := v.(string); ok {
				props[k] = w.str(s, p, scope, isHTMLKey(k))
			}
		}
	}
}

func (w *walker) str(s, path string, scope Scope, isHTML bool) string {
	var pick func(int) bool
	if w.pick != nil {
		pick = func(i int) bool { return w.pick(path, i) }
	}
	out, hits, n := transform(s, w.o, isHTML, pick)
	for _, h := range hits {
		w.hits = append(w.hits, FieldHit{Path: path, Scope: scope, Hit: h})
	}
	w.n += n
	return out
}

// sortHits puts hits in path order, then occurrence order: maps are walked in
// random order and a preview has to be stable.
func sortHits(h []FieldHit) {
	slices.SortFunc(h, func(a, b FieldHit) int {
		if c := comparePath(a.Path, b.Path); c != 0 {
			return c
		}
		return a.Index - b.Index
	})
}

// comparePath orders two paths segment by segment, array indices by value, so
// node 2 comes before node 10.
func comparePath(a, b string) int {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, aerr := strconv.Atoi(as[i])
		bi, berr := strconv.Atoi(bs[i])
		if aerr == nil && berr == nil && ai != bi {
			return ai - bi
		}
		if c := strings.Compare(as[i], bs[i]); c != 0 && (aerr != nil || berr != nil) {
			return c
		}
	}
	return len(as) - len(bs)
}
