package entity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/builder"
)

// docMode selects the page_doc write rules.
type docMode int

const (
	// docWrite is create/update/import: the incoming attributes are new input.
	docWrite docMode = iota
	// docRestore accepts a stored blob as-is (explicit user intent): a blob
	// with only page_sections converts to a doc, and nothing is ever refused
	// for carrying page_sections.
	docRestore
)

const (
	keyDoc      = "page_doc"
	keySections = "page_sections"
)

// normalizeDocRaw applies normalizeDoc to raw attributes blobs. It returns in
// byte-identical when nothing changes, so a no-op save stays a no-op version.
// eligible is false for entities whose page_sections is ordinary data (see
// EligibleForPageDoc): they are never converted and never refused.
func normalizeDocRaw(mode docMode, stored, in json.RawMessage, eligible bool) (json.RawMessage, error) {
	if err := rejectCaseVariantKeys(in); err != nil {
		return nil, err
	}
	if !eligible && !hasDocKey(in) && !hasDocKey(stored) {
		return in, nil
	}
	inMap, ok := decodeObject(in)
	if !ok {
		return in, nil // malformed: schema validation's job
	}
	storedMap, _ := decodeObject(stored)
	out, err := normalizeDoc(mode, storedMap, inMap, eligible)
	if err != nil {
		return nil, err
	}
	if reflect.DeepEqual(out, inMap) {
		return in, nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDoc, err)
	}
	return b, nil
}

// sameJSON reports whether a and b are the same JSON value, numbers compared
// as float64: the stored doc keeps exact numbers (4.0, 9.90, big ints) but a
// float64 client (the v1 editor, a decoding hook) sends them back normalised,
// and that must not read as a change. Trade-off: a write that only changes a
// number beyond float64 precision reads as "unchanged" and is dropped.
func sameJSON(a, b any) bool {
	return reflect.DeepEqual(floatCanon(a), floatCanon(b))
}

func floatCanon(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return v
	}
	return out
}

// hasDocKey reports whether raw is an object with a page_doc key, decided on
// the decoded key: JSON may spell it "page\u005fdoc".
func hasDocKey(raw []byte) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	_, ok := m[keyDoc]
	return ok
}

// rejectCaseVariantKeys refuses an attributes key that case-folds to page_doc
// or page_sections without being it: lenient readers would treat it as the
// real key while the seam ignored it.
func rejectCaseVariantKeys(raw []byte) error {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	for k := range m {
		for _, reserved := range []string{keyDoc, keySections} {
			if k != reserved && strings.EqualFold(k, reserved) {
				return fmt.Errorf("%w: attribute key %q is not allowed, use %q", ErrInvalidDoc, k, reserved)
			}
		}
	}
	return nil
}

func decodeObject(raw []byte) (map[string]any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	v, ok := decodeJSON(raw)
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// normalizeDoc is the single write seam for attributes.page_doc. It is pure,
// deterministic and idempotent (same stored, output fed back in = same
// output); neither map is mutated. It never computes page_sections: for an
// eligible entity the output carries the stored value forward verbatim (or has
// none), and the input's page_sections is never copied. W is the incoming
// attributes, S the stored ones.
//
//   - W.page_doc present and different from S.page_doc, or S has none: a doc
//     write. Validate, sanitise; W.page_sections is ignored.
//   - W.page_doc absent or equal, S has a doc: W.page_sections absent, equal to
//     the stored mirror or equal to the sections derived from the doc is a
//     no-op round trip; anything else is ErrPageSectionsRemoved.
//   - neither has a doc and a page_sections array exists (in W, else in S):
//     convert-once into page_doc. A conversion failure leaves the page as it is
//     unless the write itself carried a changed page_sections.
//   - otherwise untouched.
//
// docRestore: a blob with page_doc is a doc write; a blob with only
// page_sections converts to a doc, and never errors on the sections.
func normalizeDoc(mode docMode, stored, in map[string]any, eligible bool) (map[string]any, error) {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	// carryMirror applies D1: the stored mirror, verbatim, or none.
	carryMirror := func() {
		if !eligible {
			return
		}
		if v, ok := stored[keySections]; ok {
			out[keySections] = v
		} else {
			delete(out, keySections)
		}
	}
	storedDoc, hasStored := stored[keyDoc]
	inDoc, hasIn := in[keyDoc]
	inSecs, hasInSecs := in[keySections]

	if hasIn && (mode == docRestore || !hasStored || !sameJSON(inDoc, storedDoc)) {
		doc, err := parseDoc(inDoc)
		if err != nil {
			return nil, err
		}
		if err := doc.CheckSettings(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDoc, err)
		}
		if err := finishDoc(out, doc, baselines(stored)...); err != nil {
			return nil, err
		}
		carryMirror()
		return out, nil
	}

	if hasStored {
		out[keyDoc] = storedDoc
		if mode == docRestore && hasInSecs && eligible {
			// A pre-doc version: its sections are the content to bring back.
			doc, err := docFromSectionsValue(inSecs)
			if err != nil {
				return nil, err
			}
			if err := finishDoc(out, doc, baselines(stored)...); err != nil {
				return nil, err
			}
			carryMirror()
			return out, nil
		}
		if !eligible {
			return out, nil
		}
		if hasInSecs && !sameJSON(inSecs, stored[keySections]) && !sameJSON(inSecs, derivedSections(storedDoc)) {
			if _, err := parseDoc(storedDoc); err != nil {
				return nil, err
			}
			return nil, ErrPageSectionsRemoved
		}
		carryMirror()
		return out, nil
	}

	// No doc anywhere.
	if !eligible {
		return out, nil
	}
	src, hasSrc := inSecs, hasInSecs
	if !hasSrc {
		src, hasSrc = stored[keySections]
	}
	if !hasSrc {
		return out, nil
	}
	wrote := hasInSecs && (mode == docRestore || !sameJSON(inSecs, stored[keySections]))
	doc, err := docFromSectionsValue(src)
	if err == nil {
		err = finishDoc(out, doc, baselines(stored)...)
	}
	if err != nil {
		if wrote {
			return nil, err
		}
		slog.Warn("page left unconverted: page_sections do not convert to a valid page_doc", "error", err)
		return out, nil
	}
	if wrote {
		slog.Warn("page_sections write converted to page_doc (deprecated)")
	}
	carryMirror()
	return out, nil
}

// docFromSectionsValue converts a decoded page_sections value into a canonical
// doc. It refuses sections whose ids the conversion would have to repair
// (duplicate, empty-after-trim or unusable as a node id): the public HTML
// carries the section id, so repairing it would change the page.
func docFromSectionsValue(v any) (*builder.Doc, error) {
	bs, err := sectionsFromAny(v)
	if err != nil {
		return nil, err
	}
	doc, err := canonDoc(builder.FromSections(bs))
	if err != nil {
		return nil, err
	}
	want := make([]string, len(bs))
	for i, s := range bs {
		want[i] = s.ID
	}
	got := make([]string, 0, len(bs))
	for _, n := range builder.Components(doc) {
		got = append(got, n.ID)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(want, got) {
		return nil, fmt.Errorf("%w: section ids must be unique and plain (letters, digits, - and _)", ErrInvalidDoc)
	}
	return doc, nil
}

// derivedSections are the page_sections a page_doc stands for, in decoded form
// (nil when the doc is invalid or builder-only).
func derivedSections(storedDoc any) any {
	doc, err := parseDoc(storedDoc)
	if err != nil {
		return nil
	}
	ss, ok := builder.LegacySections(doc)
	if !ok {
		return nil
	}
	v, err := toAny(builder.SectionMaps(ss))
	if err != nil {
		return nil
	}
	return v
}

// baselines are the stored trees whose content counts as already accepted: the
// stored page_doc and the stored page_sections as a doc (a page that is being
// converted has only the latter). SanitizeDocComponents leaves values equal to
// them alone, like sanitizeAttributesAgainst does for page_sections.
func baselines(stored map[string]any) []*builder.Doc {
	var out []*builder.Doc
	if v, ok := stored[keyDoc]; ok {
		if d, err := parseDoc(v); err == nil {
			out = append(out, d)
		}
	}
	if v, ok := stored[keySections]; ok {
		if ss, err := sectionsFromAny(v); err == nil {
			out = append(out, builder.FromSections(ss))
		}
	}
	return out
}

// finishDoc sanitises doc against the olds and writes page_doc into out (never
// the page_sections mirror).
func finishDoc(out map[string]any, doc *builder.Doc, olds ...*builder.Doc) error {
	SanitizeDocComponents(doc, olds...)
	if err := CheckSanitisedLength(doc); err != nil {
		return err
	}
	v, err := toAny(doc)
	if err != nil {
		return err
	}
	out[keyDoc] = v
	return nil
}

// parseDoc validates a decoded page_doc value.
func parseDoc(v any) (*builder.Doc, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDoc, err)
	}
	d, err := builder.ParseExact(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDoc, err)
	}
	return d, nil
}

// canonDoc round-trips a doc through the validator.
func canonDoc(d *builder.Doc) (*builder.Doc, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDoc, err)
	}
	out, err := builder.ParseExact(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDoc, err)
	}
	return out, nil
}

// toAny converts a value to its decoded JSON form (numbers as json.Number).
func toAny(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDoc, err)
	}
	out, _ := decodeJSON(raw)
	return out, nil
}

// sectionsFromAny reads stored page_sections with the shared section reader
// (builder.SectionFromMap, the same one the public renderer uses).
func sectionsFromAny(v any) ([]builder.Section, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: page_sections is not an array", ErrInvalidDoc)
	}
	out := make([]builder.Section, 0, len(arr))
	for i, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, builder.SectionFromMap(i, m))
		}
	}
	return out, nil
}

// SanitizeDocComponents applies the write-boundary sanitiser to the content of
// every component node of doc (rich-text `content` strings and html-section
// bodies) and to the `html` prop of every richtext, html, heading and text node. Values equal to the node with the same id in one of olds (the first
// match wins; nil entries are ignored) are left alone, as for page_sections.
// It reports whether anything changed.
func SanitizeDocComponents(doc *builder.Doc, olds ...*builder.Doc) bool {
	oldByID := map[string]*builder.Node{}
	for _, old := range olds {
		for _, n := range builder.Components(old) {
			if oldByID[n.ID] == nil {
				oldByID[n.ID] = n
			}
		}
	}
	changed := sanitizeWidgetHTML(doc, olds)
	for _, n := range builder.Components(doc) {
		content, ok := n.P["content"].(map[string]any)
		if !ok {
			continue
		}
		prev := oldByID[n.ID]
		var oldContent map[string]any
		var oldType string
		if prev != nil {
			oldContent, _ = prev.P["content"].(map[string]any)
			oldType, _ = prev.P["type"].(string)
		}
		sanitizeValue(content, oldContent, 0, &changed)
		if extra, ok := n.P["extra"].(map[string]any); ok {
			var oldExtra map[string]any
			if prev != nil {
				oldExtra, _ = prev.P["extra"].(map[string]any)
			}
			sanitizeValue(extra, oldExtra, 0, &changed)
		}
		typ, _ := n.P["type"].(string)
		// The theme's richtext/html fields, as sanitizeSchemaFields does for
		// page_sections: a page_doc write is sanitised exactly like a v1 one.
		if rp := fieldKindResolver.Load(); rp != nil && typ != "" {
			var oldC any
			if oldType == typ && oldContent != nil {
				oldC = oldContent
			}
			sanitizeSchemaValue(*rp, typ, content, oldC, nil, "", 0, &changed)
		}
		if !isHTMLSectionType(typ) {
			continue
		}
		body, ok := content["body"].(string)
		if !ok {
			continue
		}
		if prevBody, ok := oldContent["body"].(string); ok && isHTMLSectionType(oldType) && prevBody == body {
			continue
		}
		if clean := sanitizeHTMLBody(body); clean != body {
			content["body"] = clean
			changed = true
		}
	}
	return changed
}

// widgetHTMLSanitisers maps the widget kinds that hold markup in their `html`
// prop to the sanitiser of that kind (the one the renderer applies too);
// heading and text hold inline marks only.
var widgetHTMLSanitisers = map[string]func(string) string{
	"richtext": SanitizeRichText,
	"html":     SanitizeHTMLSectionBody,
	"heading":  SanitizeInlineHTML,
	"text":     SanitizeInlineHTML,
}

// sanitizeWidgetHTML sanitises the html prop of richtext, html, heading and text nodes. A
// value equal to the one of the same node (same id and kind) in olds is left
// alone, like component content.
func sanitizeWidgetHTML(doc *builder.Doc, olds []*builder.Doc) bool {
	if doc == nil {
		return false
	}
	type key struct{ id, t string }
	old := map[key]string{}
	var collect func(ns []*builder.Node, into func(*builder.Node))
	collect = func(ns []*builder.Node, into func(*builder.Node)) {
		for _, n := range ns {
			if n == nil {
				continue
			}
			into(n)
			collect(n.C, into)
		}
	}
	for _, o := range olds {
		if o == nil {
			continue
		}
		collect(o.Nodes, func(n *builder.Node) {
			if h, ok := n.P["html"].(string); ok && widgetHTMLSanitisers[n.T] != nil {
				if _, seen := old[key{n.ID, n.T}]; !seen {
					old[key{n.ID, n.T}] = h
				}
			}
		})
	}
	changed := false
	collect(doc.Nodes, func(n *builder.Node) {
		san := widgetHTMLSanitisers[n.T]
		h, ok := n.P["html"].(string)
		if san == nil || !ok {
			return
		}
		if prev, same := old[key{n.ID, n.T}]; same && prev == h {
			return
		}
		if clean := san(h); clean != h {
			n.P["html"] = clean
			changed = true
		}
	})
	return changed
}

// CheckSanitisedLength refuses a doc whose html prop grew past the schema's
// maxLength while it was sanitised (the doc was validated before). Storing it
// would leave a doc that no longer parses.
func CheckSanitisedLength(doc *builder.Doc) error {
	if doc == nil {
		return nil
	}
	var err error
	var walk func(ns []*builder.Node)
	walk = func(ns []*builder.Node) {
		for _, n := range ns {
			if n == nil || err != nil {
				continue
			}
			if h, ok := n.P["html"].(string); ok && widgetHTMLSanitisers[n.T] != nil {
				// The kind's own schema limit: 20,000 inline, 100,000 richtext/html.
				if limit := builder.PropMaxLength(n.T, "html"); limit > 0 && utf8.RuneCountInString(h) > limit {
					err = fmt.Errorf("%w: the %s %s is too long once cleaned (%d characters, the limit is %d); shorten it", ErrInvalidDoc, n.T, n.ID, utf8.RuneCountInString(h), limit)
				}
			}
			walk(n.C)
		}
	}
	walk(doc.Nodes)
	return err
}

// SanitizeStoredAttributes is the raw-SQL maintenance form of the write-boundary
// sanitiser (sanitize-html-sections): html section bodies in page_sections plus
// the component content of page_doc. A changed doc counts as one in the
// returned total. The frozen page_sections mirror is only cleaned in place
// (never created, never re-derived from the doc). Nothing changed returns the input verbatim. Malformed shapes are left alone.
func SanitizeStoredAttributes(raw json.RawMessage) (json.RawMessage, int, error) {
	out, n, err := SanitizeHTMLSections(raw)
	if err != nil {
		return raw, 0, err
	}
	var root map[string]any
	if json.Unmarshal(out, &root) != nil {
		return out, n, nil
	}
	docVal, ok := root[keyDoc].(map[string]any)
	if !ok {
		return out, n, nil
	}
	doc, err := parseDoc(docVal)
	if err != nil || !SanitizeDocComponents(doc) {
		return out, n, nil
	}
	if root[keyDoc], err = toAny(doc); err != nil {
		return raw, 0, err
	}
	b, err := json.Marshal(root)
	if err != nil {
		return raw, 0, err
	}
	return b, n + 1, nil
}

// NormalizeAttributes is the page_doc write seam as a pure function: the
// attributes UpdateEntity would store for in against the stored ones. `builder
// convert` verifies the render of exactly this result and writes it verbatim.
func NormalizeAttributes(stored, in json.RawMessage) (json.RawMessage, error) {
	return normalizeDocRaw(docWrite, stored, in, true)
}

// SectionsFromAttributes decodes attributes.page_sections with the shared
// reader, numbers kept exact (used by `builder convert`).
func SectionsFromAttributes(raw json.RawMessage) ([]builder.Section, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if _, ok := m[keySections]; !ok {
		return nil, nil
	}
	return builder.SectionsFromJSON(raw, true), nil
}
