package llmstxt

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/builder"
)

// RefResolver returns the doc of a reusable block, or nil when it is unknown.
type RefResolver func(id string) *builder.Doc

// componentProps are the string props a theme section may carry as text, in
// reading order.
var componentProps = []string{"title", "heading", "subtitle", "text", "body", "description"}

// DocToMarkdown reads a stored page_doc as Markdown, in document order. It
// never renders themed HTML. Hidden nodes, spacers, dividers, slots and loops
// are skipped; refs expand through resolve up to builder.RefMaxDepth levels
// and builder.RefMaxNodes nodes.
func DocToMarkdown(d *builder.Doc, resolve RefResolver) string {
	if d == nil {
		return ""
	}
	w := &docWalker{resolve: resolve}
	w.nodes(d.Nodes, 0)
	return strings.Join(w.blocks, "\n\n")
}

type docWalker struct {
	resolve RefResolver
	blocks  []string
	count   int
}

func (w *docWalker) add(s string) {
	if s = strings.TrimSpace(s); s != "" {
		w.blocks = append(w.blocks, s)
	}
}

func (w *docWalker) nodes(ns []*builder.Node, depth int) {
	for _, n := range ns {
		if n == nil {
			continue
		}
		if w.count++; w.count > builder.RefMaxNodes {
			return
		}
		if hidden, _ := n.P["hidden"].(bool); hidden {
			continue
		}
		switch n.T {
		case "heading":
			level := 2
			if l := intProp(n.P["level"]); l >= 1 && l <= 6 {
				level = l
			}
			text := propText(n.P)
			if h := strings.Join(strings.Fields(HTMLToMarkdown(str(n.P, "html"))), " "); h != "" {
				text = h
			}
			if text = OneLine(text, 0); text != "" {
				w.add(strings.Repeat("#", level) + " " + text)
			}
		case "text":
			if h := str(n.P, "html"); h != "" {
				w.add(HTMLToMarkdown(h))
			} else {
				w.add(str(n.P, "text"))
			}
		case "image":
			if src, ok := linkDest(str(n.P, "src")); ok {
				w.add(imageMarkdown(str(n.P, "alt"), src))
			}
		case "button":
			text := OneLine(str(n.P, "text"), 0)
			href, hrefOK := linkDest(str(n.P, "href"))
			switch {
			case text != "" && hrefOK:
				w.add("[" + bracketEscaper.Replace(text) + "](" + href + ")")
			case text != "":
				w.add(text)
			}
		case "ref":
			if w.resolve == nil || depth >= builder.RefMaxDepth {
				continue
			}
			id, _ := n.P["block"].(string)
			if sub := w.resolve(id); sub != nil {
				w.nodes(sub.Nodes, depth+1)
			}
		case "component":
			w.props(n.P, 0)
			if c, ok := n.P["content"].(map[string]any); ok {
				w.props(c, 0)
			}
		case "container":
			w.nodes(n.C, depth)
		}
	}
}

// props adds the readable string props of m, then recurses into arrays of
// objects (cards, items, slides).
func (w *docWalker) props(m map[string]any, depth int) {
	if depth > builder.MaxDepth {
		return
	}
	for _, k := range componentProps {
		if s := str(m, k); s != "" {
			if k == "title" || k == "heading" {
				w.add("## " + OneLine(s, 0))
			} else {
				w.add(s)
			}
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		arr, _ := m[k].([]any)
		for _, e := range arr {
			if obj, ok := e.(map[string]any); ok {
				w.props(obj, depth+1)
			}
		}
	}
}

func propText(p map[string]any) string { return str(p, "text") }

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// intProp reads a number prop: a float64 from Parse, raw text from ParseExact.
func intProp(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case fmt.Stringer:
		n, _ := strconv.Atoi(x.String())
		return n
	}
	return 0
}
