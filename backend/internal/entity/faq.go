package entity

import (
	"strings"

	"golang.org/x/net/html"
)

// ExtractFAQItems reads the question and answer pairs of the FAQ blocks the
// blog editor writes into rich text:
//
//	<div class="faq"><div class="faq-item"><h3 class="faq-q">Why?</h3><div class="faq-a"><p>Because.</p></div></div></div>
//
// Items without both a question and an answer are skipped. The public page and
// the editor's SEO panel both use it, so what the panel promises (FAQPage) is
// what the page emits.
func ExtractFAQItems(content string) []FAQItem {
	if !strings.Contains(content, "faq-item") {
		return nil
	}
	root, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return nil
	}
	var items []FAQItem
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && hasClass(n, "faq-item") {
			var q, a string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				switch {
				case c.Type != html.ElementNode:
				case hasClass(c, "faq-q"):
					q = nodeText(c)
				case hasClass(c, "faq-a"):
					a = nodeText(c)
				}
			}
			if q != "" && a != "" {
				items = append(items, FAQItem{Question: q, Answer: a})
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return items
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// nodeText is the text under n with runs of white space collapsed.
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		block := n.Type == html.ElementNode && blockTags[n.Data]
		if block {
			b.WriteByte(' ')
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			b.WriteByte(' ')
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

var blockTags = map[string]bool{"p": true, "div": true, "li": true, "br": true, "ul": true, "ol": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "tr": true, "blockquote": true}
