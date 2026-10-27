package a11y

import (
	"strings"

	"golang.org/x/net/html"
)

// htmlWidgetClass marks the output of the html widget. Its body is author
// markup (sanitised, may carry style= and no alt), so the widget rules skip it.
const htmlWidgetClass = "hm-w-html"

// CheckPublicWidgets lints a public page-builder fragment (the inside of
// <main>, island applied or not). Fragment mode: no document rules. errs fail
// a build; warns are advice (heading order, header-less table). Canvas output is not meant for it.
func CheckPublicWidgets(doc string) (errs, warns []string) {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return []string{"unparseable html: " + err.Error()}, nil
	}
	dropHTMLWidgets(root)
	c := &checker{root: root, ids: map[string]*html.Node{}}
	c.issues = append(c.issues, nestedLinks(doc)...)
	c.collectIDs()
	c.references()
	c.names() // img alt present (empty is fine), links and buttons named, nav labelled
	c.widgetRules()
	return c.issues, append(c.warns, headingOrder(root)...)
}

func dropHTMLWidgets(n *html.Node) {
	for ch := n.FirstChild; ch != nil; {
		next := ch.NextSibling
		if ch.Type == html.ElementNode && hasClass(ch, htmlWidgetClass) {
			n.RemoveChild(ch)
		} else {
			dropHTMLWidgets(ch)
		}
		ch = next
	}
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attrv(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func (c *checker) widgetRules() {
	walk(c.root, func(n *html.Node) {
		if _, ok := attr(n, "style"); ok {
			c.addf("%s has an inline style attribute", desc(n))
		}
		switch n.Data {
		case "a":
			c.linkRules(n)
		case "table":
			hasRow, hasTH := false, false
			walk(n, func(i *html.Node) {
				hasRow = hasRow || i.Data == "tr"
				hasTH = hasTH || i.Data == "th"
			})
			if hasRow && !hasTH { // a header-less table is a valid choice of the author: advice only
				c.warns = append(c.warns, "table without a th header cell")
			}
		case "details":
			if !hasChildTag(n, "summary") {
				c.addf("details without a summary")
			}
		}
		if attrv(n, "role") == "tablist" {
			c.tablist(n)
		}
	})
}

func hasChildTag(n *html.Node, tag string) bool {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && ch.Data == tag {
			return true
		}
	}
	return false
}

// tablist: every tab is selected-or-not and controls an element with role=tabpanel.
func (c *checker) tablist(list *html.Node) {
	tabs, selected := 0, 0
	walk(list, func(t *html.Node) {
		if attrv(t, "role") != "tab" {
			return
		}
		tabs++
		switch attrv(t, "aria-selected") {
		case "true":
			selected++
		case "false":
		default:
			c.addf("%s role=tab without aria-selected", desc(t))
		}
		if p, ok := c.ids[attrv(t, "aria-controls")]; !ok || attrv(p, "role") != "tabpanel" {
			c.addf("%s aria-controls does not resolve to a role=tabpanel", desc(t))
		}
	})
	if tabs == 0 {
		c.addf("tablist without tabs")
	} else if selected != 1 {
		c.addf("tablist has %d selected tabs, want 1", selected)
	}
}

// headingOrder warns when a heading level jumps down by more than one.
func headingOrder(root *html.Node) []string {
	var warns []string
	prev := 0
	walk(root, func(n *html.Node) {
		if len(n.Data) != 2 || n.Data[0] != 'h' || n.Data[1] < '1' || n.Data[1] > '6' {
			return
		}
		lvl := int(n.Data[1] - '0')
		if prev != 0 && lvl > prev+1 {
			warns = append(warns, "heading level jumps from h"+string(rune('0'+prev))+" to "+n.Data)
		}
		prev = lvl
	})
	return warns
}

// linkRules: a target=_blank link must carry noopener. (A lightbox link around an
// empty-alt image without aria-label is already an error: link without accessible text.)
func (c *checker) linkRules(a *html.Node) {
	if attrv(a, "target") == "_blank" && !hasToken(attrv(a, "rel"), "noopener") {
		c.addf("%s opens a new tab without rel=noopener", desc(a))
	}
}

func hasToken(list, tok string) bool {
	for _, t := range strings.Fields(list) {
		if t == tok {
			return true
		}
	}
	return false
}

// nestedLinks reports an <a> opened inside another <a>. The HTML parser hoists
// such links out, so it is detected on the token stream, skipping html-widget bodies.
func nestedLinks(doc string) []string {
	var errs []string
	z := html.NewTokenizer(strings.NewReader(doc))
	open, skipTag, skipDepth := false, "", 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return errs
		}
		if tt != html.StartTagToken && tt != html.EndTagToken {
			continue
		}
		t := z.Token()
		start := tt == html.StartTagToken
		if skipDepth > 0 {
			if t.Data == skipTag {
				if start {
					skipDepth++
				} else {
					skipDepth--
				}
			}
			continue
		}
		if start {
			for _, a := range t.Attr {
				if a.Key == "class" && hasToken(a.Val, htmlWidgetClass) {
					skipTag, skipDepth = t.Data, 1
				}
			}
			if skipDepth > 0 {
				continue
			}
		}
		if t.Data != "a" {
			continue
		}
		switch {
		case start && open:
			errs = append(errs, "link nested inside another link")
		case start:
			open = true
		default:
			open = false
		}
	}
}
