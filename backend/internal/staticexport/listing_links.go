package staticexport

import (
	"bytes"
	"html"
	"net/url"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

var hrefAttrRe = regexp.MustCompile(`(?i)(\shref\s*=\s*)("[^"]*"|'[^']*'|[^\s>]+)`)

// RewriteListingLinks points same-origin links to listing pages (?page=N, the
// only query key) at their exported files in an HTML page fetched at doc
// (absolute, with its query): <a href>, and <link href> whose rel has prev or
// next. Root-relative and relative values become root-relative paths; absolute
// same-origin values stay absolute. Canonical, og:url, JSON-LD and scripts are
// not touched. body is returned as is when nothing changes.
func RewriteListingLinks(doc *url.URL, body []byte) []byte {
	var out []byte
	last, off := 0, 0
	z := xhtml.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		start := off
		off += len(z.Raw())
		if tt != xhtml.StartTagToken && tt != xhtml.SelfClosingTagToken {
			continue
		}
		name, hasAttr := z.TagName()
		tag := string(name)
		if !hasAttr || (tag != "a" && tag != "link") {
			continue
		}
		var href, rel string
		var hasHref bool
		for {
			k, v, more := z.TagAttr()
			switch string(k) {
			case "href":
				if !hasHref {
					href, hasHref = string(v), true
				}
			case "rel":
				rel = string(v)
			}
			if !more {
				break
			}
		}
		if !hasHref || (tag == "link" && !relHasPrevNext(rel)) {
			continue
		}
		repl, ok := listingHrefFor(doc, href)
		if !ok {
			continue
		}
		raw := body[start:off]
		m := hrefAttrRe.FindSubmatchIndex(raw)
		if m == nil || unquoteAttr(string(raw[m[4]:m[5]])) != href {
			continue
		}
		if out == nil {
			out = make([]byte, 0, len(body)+64)
		}
		out = append(out, body[last:start+m[4]]...)
		out = append(out, '"')
		out = append(out, html.EscapeString(repl)...)
		out = append(out, '"')
		last = start + m[5]
	}
	if out == nil {
		return body
	}
	return append(out, body[last:]...)
}

func relHasPrevNext(rel string) bool {
	for _, f := range strings.Fields(strings.ToLower(rel)) {
		if f == "prev" || f == "next" {
			return true
		}
	}
	return false
}

// unquoteAttr returns the decoded value of a raw attribute value.
func unquoteAttr(raw string) string {
	if n := len(raw); n >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[n-1] == raw[0] {
		raw = raw[1 : n-1]
	}
	return html.UnescapeString(raw)
}

// listingHrefFor maps a same-origin ?page=N href to its static address.
func listingHrefFor(doc *url.URL, href string) (string, bool) {
	ref, err := url.Parse(html.UnescapeString(strings.TrimSpace(href)))
	if err != nil {
		return "", false
	}
	u := doc.ResolveReference(ref)
	if !sameOrigin(doc, u) {
		return "", false
	}
	n, ok := ListingPage(u.RawQuery)
	if !ok {
		return "", false
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	repl := listingHref(p, n)
	switch {
	case ref.Host != "" && ref.Scheme != "":
		repl = u.Scheme + "://" + u.Host + repl
	case ref.Host != "":
		repl = "//" + u.Host + repl
	}
	if u.Fragment != "" {
		repl += "#" + u.EscapedFragment()
	}
	return repl, true
}
