package render

import (
	"bytes"
	"html"
	"regexp"
)

// Change marks exist only in a changeset preview (Preview && Changes != nil).
// Every other render takes the early return and is byte-identical to before.

const (
	changeNew     = "new"
	changeChanged = "changed"
)

// previewChangeStyle outlines marked parts and styles the banner. Inline, no
// script, no reference to admin CSS.
const previewChangeStyle = `<style data-hm-preview-style>` +
	`[data-hm-change=new]{outline:2px solid #2f6b4f;outline-offset:2px}` +
	`[data-hm-change=changed]{outline:2px solid #b7791f;outline-offset:2px}` +
	`.hm-preview-banner{margin:0;padding:.5rem 1rem;background:#1f2937;color:#fff;font:14px/1.4 system-ui,sans-serif;text-align:center}` +
	`</style>`

var (
	bodyOpenRe = regexp.MustCompile(`<body[^>]*>`)
	mainOpenRe = regexp.MustCompile(`<main\b[^>]*?data-hm-entity=`)
)

// previewMarks reports whether v is a changeset preview with change marks.
func previewMarks(v *PageView) bool { return v != nil && v.Preview && v.Changes != nil }

// changeAttr returns ` data-hm-change="new|changed"` for a node or section id,
// or "" when the id is unmarked or v is not a changeset preview.
func changeAttr(v *PageView, id string) string {
	if !previewMarks(v) {
		return ""
	}
	return changeAttrValue(v.Changes.Nodes[id])
}

func changeAttrValue(kind string) string {
	if kind != changeNew && kind != changeChanged {
		return ""
	}
	return ` data-hm-change="` + kind + `"`
}

// markPreviewChanges adds the banner, the style, the whole-page mark on <main>
// and the section marks to a rendered changeset preview. Other renders pass
// through untouched.
func markPreviewChanges(out []byte, v *PageView) []byte {
	if !previewMarks(v) {
		return out
	}
	for id, kind := range v.Changes.Nodes {
		attr := changeAttrValue(kind)
		if attr == "" {
			continue
		}
		needle := []byte(`data-hm-section="` + html.EscapeString(id) + `"`)
		out = bytes.ReplaceAll(out, needle, append([]byte(attr[1:]+" "), needle...))
	}
	if attr := changeAttrValue(v.Changes.Page); attr != "" {
		out = mainOpenRe.ReplaceAllFunc(out, func(m []byte) []byte {
			i := len(m) - len("data-hm-entity=")
			return append(append(append([]byte{}, m[:i]...), attr[1:]+" "...), m[i:]...)
		})
	}
	if i := bytes.Index(out, []byte("</head>")); i >= 0 {
		out = spliceAt(out, i, []byte(previewChangeStyle))
	}
	banner := `<p class="hm-preview-banner" role="note">Preview of changeset ` + html.EscapeString(v.Changes.Label) +
		`, not live. Nobody else can see this.</p>`
	if loc := bodyOpenRe.FindIndex(out); loc != nil {
		out = spliceAt(out, loc[1], []byte(banner))
	}
	return out
}

func spliceAt(out []byte, at int, ins []byte) []byte {
	res := make([]byte, 0, len(out)+len(ins))
	res = append(res, out[:at]...)
	res = append(res, ins...)
	return append(res, out[at:]...)
}
