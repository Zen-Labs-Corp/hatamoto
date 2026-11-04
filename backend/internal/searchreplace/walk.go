package searchreplace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/entity"
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
)

// FieldHit is a hit with where it was found. Path is the "/"-joined keys and
// indices from the attributes root (a "/" or "~" inside a key is written
// "~1" or "~0").
type FieldHit struct {
	Path  string
	Scope Scope
	Hit
}

// walker visits the strings of one attributes object.
type walker struct {
	o      Options
	scopes Scope
	pick   func(path string, i int) bool
	hits   []FieldHit
	n      int // replacements
}

// shape tells how far the walk may descend below a page_doc key. The builder
// doc holds ids, kinds, styles and bindings next to the text, none of which
// is ever edited: only node props (p) are read, through nodes and children.
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
func Walk(attrs json.RawMessage, o Options, scopes Scope, pick func(path string, i int) bool) (out json.RawMessage, hits []FieldHit, replaced int, err error) {
	if o.Find == "" {
		return attrs, nil, 0, nil
	}
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(attrs))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil || root == nil {
		return attrs, nil, 0, nil // not an object: nothing to search
	}
	w := &walker{o: o, scopes: scopes, pick: pick}
	for k, v := range root {
		scope, ok := classify(k)
		if !ok || scope&scopes == 0 {
			continue
		}
		sh := shapeFree
		if k == "page_doc" {
			sh = shapeDoc
		}
		root[k] = w.visit(v, "/"+escapeKey(k), scope, sh)
	}
	sortHits(w.hits)
	if w.n == 0 {
		return attrs, w.hits, 0, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return attrs, nil, 0, fmt.Errorf("searchreplace: encode attributes: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), w.hits, w.n, nil
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

func (w *walker) visit(v any, path string, scope Scope, sh shape) any {
	switch x := v.(type) {
	case string:
		if sh != shapeFree {
			return x
		}
		return w.str(x, path, scope)
	case map[string]any:
		for k, child := range x {
			csh, ok := childShape(sh, k)
			if !ok {
				continue
			}
			x[k] = w.visit(child, path+"/"+escapeKey(k), scope, csh)
		}
		return x
	case []any:
		for i, child := range x {
			csh := sh
			if sh == shapeNodeList {
				csh = shapeNode
			}
			x[i] = w.visit(child, path+"/"+strconv.Itoa(i), scope, csh)
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
		case "p":
			return shapeFree, true
		case "c":
			return shapeNodeList, true
		}
	}
	return 0, false
}

func (w *walker) str(s, path string, scope Scope) string {
	var pick func(int) bool
	if w.pick != nil {
		pick = func(i int) bool { return w.pick(path, i) }
	}
	out, hits, n := transform(s, w.o, pick)
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
