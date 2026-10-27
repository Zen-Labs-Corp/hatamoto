// Package a11y is the no-browser accessibility checker run over every golden
// page and partial: structural rules on the HTML (names, landmarks, ids,
// headings, tables, dialogs, CSP-hostile attributes) plus contrast and CSS lint
// on the tokens. It cannot replace a screen-reader pass (docs/QA.md has the
// by-hand checklist) but it fails the build on the mistakes machines can see.
package a11y

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// Options selects the rule set: Page applies document-level rules (one h1,
// lang, title, landmarks, skip link) that make no sense for a fragment.
type Options struct{ Page bool }

type checker struct {
	root   *html.Node
	ids    map[string]*html.Node
	issues []string
	warns  []string // advice from CheckPublicWidgets
}

// Check returns one string per violation; empty means clean.
func Check(doc string, o Options) []string {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return []string{"unparseable html: " + err.Error()}
	}
	c := &checker{root: root, ids: map[string]*html.Node{}}
	c.collectIDs()
	c.references()
	c.names()
	c.forms()
	c.tables()
	c.dialogs()
	c.hostile()
	c.invalid()
	if o.Page {
		c.page()
	}
	return c.issues
}

func (c *checker) addf(format string, a ...any) {
	c.issues = append(c.issues, fmt.Sprintf(format, a...))
}

func walk(n *html.Node, f func(*html.Node)) {
	if n.Type == html.ElementNode {
		f(n)
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		walk(ch, f)
	}
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func attrv(n *html.Node, key string) string { v, _ := attr(n, key); return v }

func desc(n *html.Node) string {
	s := "<" + n.Data
	if id := attrv(n, "id"); id != "" {
		s += "#" + id
	}
	if name := attrv(n, "name"); name != "" {
		s += " name=" + name
	}
	return s + ">"
}

// text is the rendered text content, skipping aria-hidden subtrees, with <img>
// alt text included.
func text(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == html.ElementNode {
			if attrv(n, "aria-hidden") == "true" {
				return
			}
			if n.Data == "img" {
				b.WriteString(attrv(n, "alt"))
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			rec(ch)
		}
	}
	rec(n)
	return strings.TrimSpace(b.String())
}

func (c *checker) collectIDs() {
	walk(c.root, func(n *html.Node) {
		if id, ok := attr(n, "id"); ok && id != "" {
			if _, dup := c.ids[id]; dup {
				c.addf("duplicate id %q", id)
			}
			c.ids[id] = n
		}
	})
}

func (c *checker) references() {
	walk(c.root, func(n *html.Node) {
		for _, k := range []string{"aria-describedby", "aria-labelledby", "aria-controls"} {
			for _, id := range strings.Fields(attrv(n, k)) {
				if _, ok := c.ids[id]; !ok {
					c.addf("%s %s=%q points at a missing id", desc(n), k, id)
				}
			}
		}
		if n.Data == "label" {
			if f, ok := attr(n, "for"); ok {
				if _, found := c.ids[f]; !found {
					c.addf("label for=%q points at a missing id", f)
				}
			}
		}
	})
}

func (c *checker) labelFor(id string) string {
	var s string
	walk(c.root, func(n *html.Node) {
		if n.Data == "label" && attrv(n, "for") == id && id != "" {
			s += " " + text(n)
		}
	})
	return strings.TrimSpace(s)
}

func hasAncestor(n *html.Node, tag string) *html.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == tag {
			return p
		}
	}
	return nil
}

func (c *checker) accessibleName(n *html.Node) string {
	if v := strings.TrimSpace(attrv(n, "aria-label")); v != "" {
		return v
	}
	for _, id := range strings.Fields(attrv(n, "aria-labelledby")) {
		if t, ok := c.ids[id]; ok {
			if s := text(t); s != "" {
				return s
			}
		}
	}
	if n.Data == "input" || n.Data == "select" || n.Data == "textarea" {
		if s := c.labelFor(attrv(n, "id")); s != "" {
			return s
		}
		if l := hasAncestor(n, "label"); l != nil {
			return text(l)
		}
		return ""
	}
	return text(n)
}

func (c *checker) names() {
	walk(c.root, func(n *html.Node) {
		switch n.Data {
		case "input":
			switch attrv(n, "type") {
			case "hidden", "submit", "button", "reset", "image":
				if attrv(n, "type") != "hidden" && attrv(n, "value") == "" && c.accessibleName(n) == "" {
					c.addf("%s has no accessible name", desc(n))
				}
				return
			}
			if c.accessibleName(n) == "" {
				c.addf("%s has no accessible name (label, aria-label or aria-labelledby)", desc(n))
			}
		case "select", "textarea":
			if c.accessibleName(n) == "" {
				c.addf("%s has no accessible name", desc(n))
			}
		case "button":
			// A hidden default submit (aria-hidden, tabindex=-1) is out of the tree by design.
			if attrv(n, "aria-hidden") == "true" && attrv(n, "tabindex") == "-1" {
				return
			}
			if c.accessibleName(n) == "" {
				c.addf("button %s has no accessible text", desc(n))
			}
		case "a":
			if _, ok := attr(n, "href"); ok && c.accessibleName(n) == "" {
				c.addf("link %q has no accessible text", attrv(n, "href"))
			}
		case "img":
			if _, ok := attr(n, "alt"); !ok {
				c.addf("img %q has no alt attribute", attrv(n, "src"))
			}
		case "svg":
			if attrv(n, "aria-hidden") != "true" && attrv(n, "role") == "" && attrv(n, "aria-label") == "" {
				c.addf("svg is neither aria-hidden nor named")
			}
		case "nav":
			if attrv(n, "aria-label") == "" && attrv(n, "aria-labelledby") == "" {
				c.addf("nav without aria-label")
			}
		}
		if t := attrv(n, "tabindex"); t != "" && t != "0" && !strings.HasPrefix(t, "-") {
			c.addf("%s has positive tabindex %s", desc(n), t)
		}
		if attrv(n, "role") == "tab" {
			if _, ok := attr(n, "aria-selected"); !ok {
				c.addf("%s role=tab without aria-selected", desc(n))
			}
			if attrv(n, "aria-controls") == "" {
				c.addf("%s role=tab without aria-controls", desc(n))
			}
		}
	})
}

func (c *checker) forms() {
	walk(c.root, func(n *html.Node) {
		switch n.Data {
		case "input", "select", "textarea":
			t := attrv(n, "type")
			if t == "submit" || t == "button" || t == "reset" || t == "image" {
				return
			}
			if hasAncestor(n, "form") != nil && attrv(n, "name") == "" && attrv(n, "form") == "" {
				c.addf("%s inside a form has no name", desc(n))
			}
		case "form":
			if strings.EqualFold(attrv(n, "method"), "post") {
				has := false
				walk(n, func(i *html.Node) {
					if i.Data == "input" && attrv(i, "name") == "_csrf" {
						has = true
					}
				})
				if !has && attrv(n, "hx-post") == "" {
					c.addf("post form %q has no _csrf field", attrv(n, "action"))
				}
			}
		}
	})
}

func (c *checker) tables() {
	walk(c.root, func(t *html.Node) {
		if t.Data != "table" {
			return
		}
		named := attrv(t, "aria-label") != "" || attrv(t, "aria-labelledby") != ""
		walk(t, func(n *html.Node) {
			switch n.Data {
			case "caption":
				if text(n) != "" {
					named = true
				}
			case "th":
				if attrv(n, "scope") == "" {
					c.addf("table header %q has no scope", text(n))
				}
				if _, ok := attr(n, "aria-sort"); ok {
					found := false
					walk(n, func(i *html.Node) {
						if i.Data == "a" || i.Data == "button" {
							found = true
						}
					})
					if !found {
						c.addf("sortable header %q has no link or button", text(n))
					}
				}
			}
		})
		if !named {
			c.addf("table has no caption or aria-label")
		}
	})
}

func (c *checker) dialogs() {
	walk(c.root, func(n *html.Node) {
		if n.Data != "dialog" {
			return
		}
		ref := attrv(n, "aria-labelledby")
		t, ok := c.ids[ref]
		if !ok || len(t.Data) != 2 || t.Data[0] != 'h' {
			c.addf("dialog %s must be labelled by a heading", desc(n))
		}
	})
}

// hostile flags what the strict CSP breaks or what hides from assistive tech.
func (c *checker) hostile() {
	walk(c.root, func(n *html.Node) {
		for _, a := range n.Attr {
			k := strings.ToLower(a.Key)
			switch {
			case strings.HasPrefix(k, "on"):
				c.addf("%s has inline handler %s (blocked by the CSP)", desc(n), k)
			case k == "style":
				c.addf("%s has an inline style attribute (blocked by the CSP)", desc(n))
			case k == "href" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:"):
				c.addf("%s has a javascript: href", desc(n))
			case strings.HasPrefix(k, "hx-on"):
				c.addf("%s uses %s (needs eval, forbidden by the CSP)", desc(n), k)
			case (k == "hx-vals" || k == "hx-vars" || k == "hx-headers") && strings.HasPrefix(strings.TrimSpace(a.Val), "js:"):
				c.addf("%s uses %s js: (needs eval)", desc(n), k)
			case k == "hx-trigger" && strings.Contains(a.Val, "["):
				c.addf("%s has an hx-trigger filter (needs eval)", desc(n))
			}
		}
	})
}

// invalid: aria-invalid=true <=> the control points at an error element.
func (c *checker) invalid() {
	walk(c.root, func(n *html.Node) {
		if n.Data != "input" && n.Data != "select" && n.Data != "textarea" {
			return
		}
		inv := attrv(n, "aria-invalid") == "true"
		hasErr := false
		for _, id := range strings.Fields(attrv(n, "aria-describedby")) {
			if e, ok := c.ids[id]; ok && strings.Contains(attrv(e, "class"), "hm-field__error") {
				hasErr = true
			}
		}
		switch {
		case inv && !hasErr:
			c.addf("%s is aria-invalid but not described by an error element", desc(n))
		case !inv && hasErr:
			c.addf("%s has an error element but is not aria-invalid", desc(n))
		}
	})
}

func (c *checker) page() {
	var h1, mains int
	var levels []int
	var title string
	var firstFocusable *html.Node
	htmlLang := ""
	walk(c.root, func(n *html.Node) {
		switch n.Data {
		case "html":
			htmlLang = attrv(n, "lang")
		case "title":
			title = text(n)
		case "main":
			mains++
			if attrv(n, "id") != "hm-main" {
				c.addf("main must have id=hm-main")
			}
		case "h1", "h2", "h3", "h4", "h5", "h6":
			lvl := int(n.Data[1] - '0')
			if lvl == 1 {
				h1++
			}
			levels = append(levels, lvl)
		case "a", "button", "input", "select", "textarea":
			if firstFocusable == nil && attrv(n, "type") != "hidden" {
				if _, dis := attr(n, "disabled"); !dis {
					firstFocusable = n
				}
			}
		}
	})
	if h1 != 1 {
		c.addf("page has %d <h1>, want exactly 1", h1)
	}
	prev := 0
	for _, l := range levels {
		if prev != 0 && l > prev+1 {
			c.addf("heading level jumps from h%d to h%d", prev, l)
		}
		prev = l
	}
	if htmlLang == "" {
		c.addf("<html> has no lang")
	}
	if !strings.HasSuffix(title, "Hatamoto") {
		c.addf("title %q must end with the app name", title)
	}
	if mains != 1 {
		c.addf("page has %d <main>, want 1", mains)
	}
	if firstFocusable == nil || firstFocusable.Data != "a" || attrv(firstFocusable, "href") != "#hm-main" {
		c.addf("the skip link to #hm-main must be the first focusable element")
	}
}
