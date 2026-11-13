package render

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// TOCEntry is one h2 entry of the toc func's items.
type TOCEntry struct{ ID, Text string }

// tocFn gives the h2 headings of an already sanitised body ids and lists them:
// {"html": the body with the ids, "items": []TOCEntry}. Existing ids are kept,
// duplicates get -2, -3, and headings inside pre or code are ignored. Empty or
// unparsable input gives {"html": "", "items": nil}.
func tocFn(body any) map[string]any {
	src := toStr(body)
	out := map[string]any{"html": template.HTML(""), "items": []TOCEntry(nil)}
	if strings.TrimSpace(src) == "" {
		return out
	}
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := html.ParseFragment(strings.NewReader(src), ctx)
	if err != nil {
		return out
	}
	used := map[string]bool{}
	for id := range docReservedIDs {
		used[id] = true
	}
	var heads []*html.Node
	var walk func(n *html.Node, code bool)
	walk = func(n *html.Node, code bool) {
		if n.Type == html.ElementNode {
			if v, ok := attrVal(n, "id"); ok {
				used[v] = true
			}
			if n.DataAtom == atom.Pre || n.DataAtom == atom.Code {
				code = true
			}
			if n.DataAtom == atom.H2 && !code {
				heads = append(heads, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, code)
		}
	}
	for _, n := range nodes {
		walk(n, false)
	}

	var items []TOCEntry
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
		items = append(items, TOCEntry{ID: id, Text: text})
	}

	var buf bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&buf, n); err != nil {
			return out
		}
	}
	out["html"] = template.HTML(buf.String()) // #nosec G203 -- input was sanitised; only id attributes added
	out["items"] = items
	return out
}
