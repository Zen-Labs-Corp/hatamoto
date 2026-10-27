package entity

import (
	"bytes"
	"html"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// hmAttrPrefix marks the renderer's marker attributes (data-hm-section,
// data-hm-field, ...). Tenant content must never carry them, otherwise it
// could spoof editor markers.
const hmAttrPrefix = "data-hm-"

// SanitizeHTMLSectionBody sanitizes the body of an html section, or any HTML
// fragment a theme builds, with the html-section policy and then strips every
// data-hm-* attribute. The policy keeps class, id, role, style (checked),
// aria-* and data-* attributes; it never keeps script, event handlers, forms
// or an iframe off the embed allowlist (see EmbedHostsEnv).
func SanitizeHTMLSectionBody(s string) string {
	return stripHMAttrs(sanitizeHTMLBody(s))
}

// SanitizeRichText sanitizes rich text with the rich-text policy and then
// strips every data-hm-* attribute.
func SanitizeRichText(s string) string {
	return stripHMAttrs(richText().Sanitize(s))
}

// AllowedInlineHref reports whether raw is a link target the inline profile
// keeps: http, https, mailto, tel, sms or a relative reference (path, ?query,
// #fragment). It is the render safeURL rule (render cannot be imported here);
// internal/entity/testdata/inline_href_cases.json keeps the two in step.
func AllowedInlineHref(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, `/\`) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "tel", "sms":
		return true
	case "":
		return u.Host == ""
	}
	return false
}

// SanitizeInlineHTML sanitizes the `html` prop of a heading or text node:
// strong em b i u s code br and links, nothing else. Links keep href (only an
// AllowedInlineHref one; otherwise the link is unwrapped and its text kept)
// and target=_blank, which always carries rel="noopener noreferrer". The
// result is re-parsed in a <div> and rendered again, so tags are balanced and
// sanitising twice changes nothing.
func SanitizeInlineHTML(s string) string {
	clean := inlineText().Sanitize(s)
	nodes, err := xhtml.ParseFragment(strings.NewReader(clean), &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return ""
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	fixInlineLinks(root)
	var buf strings.Builder
	writeInline(&buf, root)
	return buf.String()
}

// inlineEscaper is the canonical text escape: & < > only. The bridge (`ser` in
// canvas_bridge.js) and the inspector's normaliser write the same form;
// testdata/inline/*.json keeps the three in step.
var (
	// A CR is written as LF: the tokenizer folds a raw CR to LF on the next pass,
	// so keeping it would make the sanitiser not idempotent (`&#13;`).
	inlineTextEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "\n")
	inlineAttrEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
)

// writeInline renders the children of n in the canonical inline form: `'` and
// `"` literal in text, `"` as &quot; in attributes, <br> without a slash. Only
// text, br, the marks and links survive the sanitiser, so nothing else is
// rendered; any other element is unwrapped.
func writeInline(b *strings.Builder, n *xhtml.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == xhtml.TextNode:
			b.WriteString(inlineTextEsc.Replace(c.Data))
		case c.Type != xhtml.ElementNode:
		case c.DataAtom == atom.Br:
			b.WriteString("<br>")
		case c.DataAtom == atom.A, c.DataAtom == atom.Strong, c.DataAtom == atom.Em, c.DataAtom == atom.B,
			c.DataAtom == atom.I, c.DataAtom == atom.U, c.DataAtom == atom.S, c.DataAtom == atom.Code:
			b.WriteString("<" + c.Data)
			for _, a := range c.Attr {
				if !strings.HasPrefix(strings.ToLower(a.Key), hmAttrPrefix) {
					b.WriteString(" " + a.Key + `="` + inlineAttrEsc.Replace(a.Val) + `"`)
				}
			}
			b.WriteString(">")
			writeInline(b, c)
			b.WriteString("</" + c.Data + ">")
		default:
			writeInline(b, c)
		}
	}
}

// fixInlineLinks unwraps unsafe links and rewrites the attributes of the rest
// into the canonical href, target, rel form.
func fixInlineLinks(n *xhtml.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == xhtml.ElementNode && c.DataAtom == atom.A {
			fixInlineLinks(c)
			href, blank := "", false
			for _, a := range c.Attr {
				switch a.Key {
				case "href":
					href = a.Val
				case "target":
					blank = a.Val == "_blank"
				}
			}
			if !AllowedInlineHref(href) {
				for k := c.FirstChild; k != nil; {
					kn := k.NextSibling
					c.RemoveChild(k)
					n.InsertBefore(k, c)
					k = kn
				}
				n.RemoveChild(c)
			} else {
				c.Attr = []xhtml.Attribute{{Key: "href", Val: href}}
				if blank {
					c.Attr = append(c.Attr, xhtml.Attribute{Key: "target", Val: "_blank"}, xhtml.Attribute{Key: "rel", Val: "noopener noreferrer"})
				}
			}
		} else {
			fixInlineLinks(c)
		}
		c = next
	}
}

// SanitizeSectionContent returns a sanitized deep copy of a page section's
// content for render-time use (e.g. draft preview of unsaved attributes).
// For html-type sections content.body is treated as html; any string stored
// under a key named "content" is treated as rich text, matching the write path.
func SanitizeSectionContent(sectionType string, content map[string]any) map[string]any {
	if content == nil {
		return nil
	}
	out, _ := sanitizeContentValue(content, isHTMLSectionType(sectionType), 0).(map[string]any)
	return out
}

func sanitizeContentValue(v any, htmlSection bool, depth int) any {
	if depth > maxSanitizeDepth {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			if s, ok := child.(string); ok {
				switch {
				case k == "content":
					out[k] = SanitizeRichText(s)
					continue
				case htmlSection && depth == 0 && k == "body":
					out[k] = SanitizeHTMLSectionBody(s)
					continue
				}
			}
			out[k] = sanitizeContentValue(child, htmlSection, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = sanitizeContentValue(child, htmlSection, depth+1)
		}
		return out
	}
	return v
}

// stripHMAttrs removes every attribute whose name starts with "data-hm-" from
// the start tags of an HTML fragment. All other bytes pass through untouched.
func stripHMAttrs(s string) string {
	if !strings.Contains(strings.ToLower(s), hmAttrPrefix) {
		return s
	}
	z := xhtml.NewTokenizer(strings.NewReader(s))
	var buf bytes.Buffer
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			return buf.String()
		}
		if tt != xhtml.StartTagToken && tt != xhtml.SelfClosingTagToken {
			buf.Write(z.Raw())
			continue
		}
		raw := append([]byte(nil), z.Raw()...)
		name, hasAttr := z.TagName()
		if !hasAttr {
			buf.Write(raw)
			continue
		}
		buf.WriteByte('<')
		buf.Write(name)
		for hasAttr {
			var key, val []byte
			key, val, hasAttr = z.TagAttr()
			if strings.HasPrefix(strings.ToLower(string(key)), hmAttrPrefix) {
				continue
			}
			buf.WriteByte(' ')
			buf.Write(key)
			buf.WriteString(`="`)
			buf.WriteString(html.EscapeString(string(val)))
			buf.WriteByte('"')
		}
		if tt == xhtml.SelfClosingTagToken {
			buf.WriteString("/")
		}
		buf.WriteByte('>')
	}
}
