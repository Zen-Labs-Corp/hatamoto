package render

import (
	"errors"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Canvas text addressing. In canvas renders every editable text node of an
// html-kind field is stamped with data-hm-text="<field path>#<n>", where n is
// the 0-based ordinal of the node in a pre-order walk of the body parsed as a
// <div> fragment. The frontend (html-text.ts) walks the stored string with the
// same rules; testdata/canvas-text/*.golden.json pins both implementations.

// TextNode is one editable text node in walk order.
type TextNode struct {
	Index int    `json:"index"`
	Tag   string `json:"tag"` // parent element name
	Text  string `json:"text"`
}

// Errors returned by ReplaceTextNode.
var (
	ErrStaleText = errors.New("render: text node does not match expected text")
	ErrBlank     = errors.New("render: replacement text is blank")
	ErrRange     = errors.New("render: text node index out of range")
)

// maxStampBytes bounds the work spent stamping one body.
const maxStampBytes = 1 << 20

// skipText names elements whose subtrees are never editable.
var skipText = map[string]bool{
	"script": true, "style": true, "iframe": true, "svg": true, "math": true,
	"noscript": true, "template": true, "textarea": true, "pre": true,
	"code": true, "select": true, "option": true, "object": true,
}

// isBlank reports whether s has no visible character. The set is explicit
// (not unicode.IsSpace) so the browser twin can use the same literal class.
func isBlank(s string) bool {
	for _, r := range s {
		switch r {
		case '\t', '\n', '\f', '\r', ' ', '\u00a0':
		default:
			return false
		}
	}
	return true
}

// parseDivFragment parses body in a <div> context and returns a container
// element holding the fragment's top-level nodes.
func parseDivFragment(body string) (*html.Node, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	kids, err := html.ParseFragment(strings.NewReader(body), ctx)
	if err != nil {
		return nil, err
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, k := range kids {
		root.AppendChild(k)
	}
	return root, nil
}

// walkEditable visits editable text nodes under n in document order.
func walkEditable(n *html.Node, visit func(*html.Node)) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if !isBlank(c.Data) {
				visit(c)
			}
		case html.ElementNode:
			if !skipText[c.Data] {
				walkEditable(c, visit)
			}
		}
	}
}

func collectEditable(root *html.Node) []*html.Node {
	var out []*html.Node
	walkEditable(root, func(n *html.Node) { out = append(out, n) })
	return out
}

func renderChildren(root *html.Node) (string, error) {
	var b strings.Builder
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// EditableTextNodes lists the editable text nodes of body in walk order.
func EditableTextNodes(body string) ([]TextNode, error) {
	root, err := parseDivFragment(body)
	if err != nil {
		return nil, err
	}
	nodes := collectEditable(root)
	out := make([]TextNode, len(nodes))
	for i, n := range nodes {
		out[i] = TextNode{Index: i, Tag: n.Parent.Data, Text: n.Data}
	}
	return out, nil
}

// StampEditableText marks each editable text node of body with
// data-hm-text="<fieldPath>#<n>". A node that is the only child of a
// non-root element other than button or summary stamps that element; every other node is
// wrapped in a <span>. Returns body unchanged on parse/render error or when
// body is oversized.
func StampEditableText(body, fieldPath string) string {
	if len(body) > maxStampBytes {
		return body
	}
	root, err := parseDivFragment(body)
	if err != nil {
		return body
	}
	targets := collectEditable(root) // collect first, mutate after
	if len(targets) == 0 {
		return body
	}
	for i, n := range targets {
		val := fieldPath + "#" + strconv.Itoa(i)
		p := n.Parent
		if p != root && p.Data != "button" && p.Data != "summary" && p.FirstChild == n && p.LastChild == n {
			p.Attr = append(p.Attr, html.Attribute{Key: "data-hm-text", Val: val})
			continue
		}
		span := &html.Node{
			Type: html.ElementNode, Data: "span", DataAtom: atom.Span,
			Attr: []html.Attribute{{Key: "data-hm-text", Val: val}},
		}
		p.InsertBefore(span, n)
		p.RemoveChild(n)
		span.AppendChild(n)
	}
	out, err := renderChildren(root)
	if err != nil {
		return body
	}
	return out
}

// ReplaceTextNode is the Go twin of the frontend replaceTextNode (used by
// tests to pin behaviour): it sets text node index to value when its current
// text equals expect, and returns the re-serialised body.
func ReplaceTextNode(body string, index int, expect, value string) (string, error) {
	root, err := parseDivFragment(body)
	if err != nil {
		return "", err
	}
	nodes := collectEditable(root)
	if index < 0 || index >= len(nodes) {
		return "", ErrRange
	}
	if nodes[index].Data != expect {
		return "", ErrStaleText
	}
	if isBlank(value) {
		return "", ErrBlank
	}
	nodes[index].Data = value
	return renderChildren(root)
}
