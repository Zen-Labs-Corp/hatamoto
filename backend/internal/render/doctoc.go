package render

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// TOCItem is one entry of the on-page table of contents.
type TOCItem struct {
	ID, Text string
	Level    int // 2 or 3
}

// DocBody is a page body with heading ids and its table of contents.
type DocBody struct {
	HTML template.HTML
	TOC  []TOCItem
}

// docReservedIDs are ids the layouts own; headings never take them.
var docReservedIDs = map[string]bool{"main-content": true, "toc": true, "top": true}

// docBody adds unique ids to the h2 and h3 headings of an already sanitised
// body and lists them as TOC items. The ids exist only in the rendered page,
// never in stored content. Empty or unparsable input gives the zero value.
func docBody(body template.HTML) DocBody {
	if strings.TrimSpace(string(body)) == "" {
		return DocBody{}
	}
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := html.ParseFragment(strings.NewReader(string(body)), ctx)
	if err != nil {
		return DocBody{HTML: body}
	}
	used := map[string]bool{}
	for id := range docReservedIDs {
		used[id] = true
	}
	var heads []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if v, ok := attrVal(n, "id"); ok {
				used[v] = true
			}
			if n.DataAtom == atom.H2 || n.DataAtom == atom.H3 {
				heads = append(heads, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}

	var toc []TOCItem
	for _, h := range heads {
		text := strings.Join(strings.Fields(nodeText(h)), " ")
		id, has := attrVal(h, "id")
		if !has {
			base := slugifyHeading(text)
			id = base
			for n := 2; used[id]; n++ {
				id = base + "-" + strconv.Itoa(n)
			}
			used[id] = true
			h.Attr = append(h.Attr, html.Attribute{Key: "id", Val: id})
		}
		level := 2
		if h.DataAtom == atom.H3 {
			level = 3
		}
		toc = append(toc, TOCItem{ID: id, Text: text, Level: level})
	}

	var buf bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&buf, n); err != nil {
			return DocBody{HTML: body}
		}
	}
	return DocBody{HTML: template.HTML(buf.String()), TOC: toc} // #nosec G203 -- input was sanitised; only id attributes added
}

func attrVal(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c))
	}
	return b.String()
}

// slugifyHeading lowercases ASCII letters and digits and joins runs of
// anything else with single hyphens. An empty result is "section".
func slugifyHeading(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}
