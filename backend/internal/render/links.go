package render

import (
	"html/template"
	"strings"

	"golang.org/x/net/html"
)

// Internal links are stored in rich text as <a data-entity-id="UUID" href="...">
// (the editor's entity-link picker). The stored href is only a snapshot: a slug
// or URL-pattern change can leave it stale. At render time the server resolves
// every referenced entity to its current public path (PageView.EntityLinks) and
// RewriteEntityLinks swaps the href in. Entities that are not in the map
// (unpublished, deleted) keep their stored href.

const entityIDAttr = "data-entity-id"

// EntityLinkIDs returns the distinct data-entity-id values of the <a> tags in
// html, in order of first appearance.
func EntityLinkIDs(htmlStr string) []string {
	if !strings.Contains(htmlStr, entityIDAttr) {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	z := html.NewTokenizer(strings.NewReader(htmlStr))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return ids
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		if tok.Data != "a" {
			continue
		}
		for _, a := range tok.Attr {
			if a.Key == entityIDAttr && a.Val != "" && !seen[a.Val] {
				seen[a.Val] = true
				ids = append(ids, a.Val)
			}
		}
	}
}

// RewriteEntityLinks sets the href of every <a data-entity-id="X"> whose X is a
// key of hrefs. Everything else in html is copied through byte for byte. The
// hrefs come from the URL registry and are root-relative paths; a value that
// is not (no leading "/", or "//") is ignored.
func RewriteEntityLinks(htmlStr string, hrefs map[string]string) string {
	if len(hrefs) == 0 || !strings.Contains(htmlStr, entityIDAttr) {
		return htmlStr
	}
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(htmlStr))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return b.String()
		}
		if tt != html.StartTagToken {
			b.Write(z.Raw())
			continue
		}
		raw := append([]byte(nil), z.Raw()...)
		tok := z.Token()
		href, ok := "", false
		if tok.Data == "a" {
			for _, a := range tok.Attr {
				if a.Key == entityIDAttr {
					href, ok = hrefs[a.Val]
				}
			}
		}
		if !ok || !strings.HasPrefix(href, "/") || strings.HasPrefix(href, "//") {
			b.Write(raw)
			continue
		}
		replaced := false
		for i := range tok.Attr {
			if tok.Attr[i].Key == "href" {
				tok.Attr[i].Val, replaced = href, true
			}
		}
		if !replaced {
			tok.Attr = append(tok.Attr, html.Attribute{Key: "href", Val: href})
		}
		b.WriteString(tok.String())
	}
}

// rewriteLinksIn rewrites entity links in every template.HTML value inside v
// (rich-text and html fields, the only values prepareValue marks as HTML),
// in place. v must be a fresh copy, which prepareContent guarantees.
func rewriteLinksIn(v any, hrefs map[string]string) any {
	switch t := v.(type) {
	case template.HTML:
		return template.HTML(RewriteEntityLinks(string(t), hrefs)) // #nosec G203 -- t was sanitised; only href is replaced with a registry path
	case map[string]any:
		for k, c := range t {
			t[k] = rewriteLinksIn(c, hrefs)
		}
	case []any:
		for i, c := range t {
			t[i] = rewriteLinksIn(c, hrefs)
		}
	}
	return v
}
