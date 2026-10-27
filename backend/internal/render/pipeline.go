package render

import (
	"encoding/json"
	"html/template"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/entity"
)

// preparedSection is a Section ready for its template: visibility and order
// resolved, aliases normalised and rich fields sanitised.
type preparedSection struct {
	Section
	// Comment is non-empty for unknown types: the template renders only an
	// HTML comment, never a <section>.
	Comment string
	// Page is the page-level context sections may read (form definitions,
	// submit state, current path).
	Page sectionPage
	// Settings are the active theme's settings, as .Settings in layouts.
	Settings map[string]any
	// Native is the pre-rendered markup of a builder node. When set it is what
	// CommentHTML returns, and Comment holds a sentinel, so the theme's section
	// partial prints it with no <section> wrapper.
	Native template.HTML
}

// sectionPage is the slice of PageView exposed to section templates as .Page.
type sectionPage struct {
	Forms map[string]FormView
	// PluginDirectory is nil unless the page has a plugin_directory section.
	PluginDirectory *PluginDirectoryView
	// ThemeDirectory is nil unless the page has a theme_directory section.
	ThemeDirectory *ThemeDirectoryView
	Path           string
	Submitted      bool
	FormError      bool
	// PageNum and Params are the request's ?page= and query string (pageNum
	// and queryParam read them from a section's ".").
	PageNum int
	Params  url.Values
}

// CommentHTML returns the unknown-type marker as an HTML comment. Comment is
// reduced to a safe character set by commentSafe, so wrapping it is safe.
func (p preparedSection) CommentHTML() template.HTML {
	if p.Native != "" {
		return p.Native
	}
	return template.HTML("<!-- " + p.Comment + " -->") // #nosec G203 -- Comment is sanitised by commentSafe
}

// schemaNode is the renderer's view of a (sub)schema.
type schemaNode struct {
	Type       string                 `json:"type"`
	Kind       string                 `json:"x-hm-field"`
	Alias      []string               `json:"x-hm-alias"`
	Properties map[string]*schemaNode `json:"properties"`
	Items      *schemaNode            `json:"items"`
}

var schemaTrees sync.Map // *SectionDef -> *schemaNode

func treeFor(def *SectionDef) *schemaNode {
	if def == nil || len(def.Schema) == 0 {
		return nil
	}
	if v, ok := schemaTrees.Load(def); ok {
		return v.(*schemaNode)
	}
	var n schemaNode
	if err := json.Unmarshal(def.Schema, &n); err != nil {
		return nil
	}
	schemaTrees.Store(def, &n)
	return &n
}

// prepareSections drops hidden sections, orders by SortOrder (stable), maps
// unknown types to a comment, normalises legacy aliases to canonical keys and
// sanitises richtext/html fields (stripping forged data-hm-* markers). The
// input is not modified.
func prepareSections(t *Theme, in []Section, canvas bool) []preparedSection {
	visible := make([]Section, 0, len(in))
	for _, s := range in {
		if s.IsVisible != nil && !*s.IsVisible {
			continue
		}
		visible = append(visible, s)
	}
	sort.SliceStable(visible, func(i, j int) bool { return visible[i].SortOrder < visible[j].SortOrder })

	out := make([]preparedSection, 0, len(visible))
	for _, s := range visible {
		def := t.Sections[s.Type]
		if def == nil {
			out = append(out, preparedSection{ID: s.ID, Type: s.Type, Comment: "unknown section type: " + commentSafe(s.Type)})
			continue
		}
		s.Content = prepareContent(def, s.Content, canvas)
		if s.Type == "content" && isBlankBody(s.Content["body"]) {
			continue
		}
		out = append(out, preparedSection{Section: s})
	}
	return out
}

func prepareContent(def *SectionDef, content map[string]any, canvas bool) map[string]any {
	tree := treeFor(def)
	if tree == nil {
		return deepCopyMap(content)
	}
	m, _ := prepareValue(tree, content, 0, "", canvas).(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

const maxPrepareDepth = 16

// prepareValue copies v while applying the schema node: alias -> canonical,
// sanitise + wrap richtext/html strings. path is the canonical field path of
// v; with canvas set, html strings are stamped with editable-text addresses.
func prepareValue(n *schemaNode, v any, depth int, path string, canvas bool) any {
	if depth > maxPrepareDepth {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			var cn *schemaNode
			if n != nil {
				cn = n.Properties[k]
			}
			out[k] = prepareValue(cn, child, depth+1, joinPath(path, k), canvas)
		}
		if n != nil {
			for canon, pn := range n.Properties {
				if !isEmptyValue(out[canon]) {
					continue
				}
				for _, a := range pn.Alias {
					if av, ok := out[a]; ok && !isEmptyValue(av) {
						out[canon] = prepareValue(pn, t[a], depth+1, joinPath(path, canon), canvas)
						break
					}
				}
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		var item *schemaNode
		if n != nil {
			item = n.Items
		}
		for i, child := range t {
			// Stored content is not schema-validated: an item that should be
			// an object but is a scalar would crash the section template.
			if _, isObj := child.(map[string]any); !isObj && item != nil && item.Type == "object" {
				continue
			}
			out = append(out, prepareValue(item, child, depth+1, joinPath(path, strconv.Itoa(i)), canvas))
		}
		return out
	case string:
		if n == nil {
			return t
		}
		switch n.Kind {
		case "richtext":
			return template.HTML(entity.SanitizeRichText(t)) // #nosec G203 -- sanitised above
		case "html":
			s := entity.SanitizeHTMLSectionBody(t)
			if canvas {
				s = StampEditableText(s, path)
			}
			return template.HTML(s) // #nosec G203 -- sanitised above
		case "css":
			return safeCSS(t)
		}
		return t
	}
	return v
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case template.HTML:
		return t == ""
	case []any:
		return len(t) == 0
	}
	return false
}

func deepCopyMap(m map[string]any) map[string]any {
	out, _ := prepareValue(nil, m, 0, "", false).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

var (
	tagRe         = regexp.MustCompile(`<[^>]*>`)
	embeddedMedia = regexp.MustCompile(`(?i)<(img|iframe|video|svg)\b`)
)

// isBlankBody ports render-sections.tsx isBlankBody: a body that would render
// as nothing (no text and no media).
func isBlankBody(body any) bool {
	var s string
	switch t := body.(type) {
	case string:
		s = t
	case template.HTML:
		s = string(t)
	default:
		return true
	}
	if embeddedMedia.MatchString(s) {
		return false
	}
	s = tagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(strings.ReplaceAll(s, "&nbsp;", " "), "\u00a0", " ")
	return strings.TrimSpace(s) == ""
}

// commentSafe reduces s to text that cannot terminate or escape an HTML
// comment: no angle brackets, ampersands, control chars or "--".
func commentSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '<' || r == '>' || r == '&' {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "")
	}
	out = strings.TrimRight(out, "-")
	if len(out) > 80 {
		out = strings.TrimRight(out[:80], "-")
	}
	return out
}

// FieldKind returns the x-hm-field kind of the content field at path (object
// keys only, array indices skipped) for a section type of the current theme,
// or "" when the type or field is not in its schema. The server registers it
// with entity.SetFieldKindResolver so save-time sanitising follows the schema.
func (e *Engine) FieldKind(sectionType string, path []string) string {
	n := treeFor(e.current().theme.Sections[sectionType])
	for _, k := range path {
		for n != nil && n.Properties == nil && n.Items != nil {
			n = n.Items
		}
		if n == nil {
			return ""
		}
		next := n.Properties[k]
		for _, pn := range n.Properties {
			for _, a := range pn.Alias {
				if next == nil && a == k {
					next = pn
				}
			}
		}
		if next == nil {
			return ""
		}
		n = next
	}
	if n == nil {
		return ""
	}
	return n.Kind
}
