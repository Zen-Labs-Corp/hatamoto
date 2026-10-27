package wpimport

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// EntityRef is what an internal link resolves to.
type EntityRef struct {
	ID   uuid.UUID
	Path string // the new public path
}

// Rewriter rewrites the links of imported bodies.
type Rewriter struct {
	// Hosts are the hostnames of the old site (without port).
	Hosts map[string]bool
	// Media maps a URL of the old site's files to the new media URL. ok is
	// false when the file could not be imported.
	Media func(u *url.URL) (newURL string, ok bool)
	// Entity resolves a path (or ?p= / ?page_id= id) of the old site to an
	// imported entity.
	Entity func(path string, query url.Values) (EntityRef, bool)
	// Path maps any other old-site path to its new one (terms, authors); nil keeps it.
	Path func(path string) (string, bool)
}

// RewriteStats counts what a rewrite did.
type RewriteStats struct {
	MediaRewritten int
	LinksRewritten int
	// Unresolved are wp-content URLs that could not be mapped; the element
	// carrying them was removed (an img) or unlinked (an anchor).
	Unresolved []string
}

var wpContentRe = regexp.MustCompile(`/wp-(content|includes)/`)

// IsWPContent reports whether a URL points into wp-content or wp-includes.
func IsWPContent(s string) bool { return wpContentRe.MatchString(s) }

func (r *Rewriter) internal(u *url.URL) bool {
	if u.Host == "" {
		return u.Scheme == "" && strings.HasPrefix(u.Path, "/")
	}
	return r.Hosts[strings.ToLower(u.Hostname())]
}

// Rewrite returns body with every wp-content URL of the old site replaced by
// its imported media URL, and internal links turned into <a data-entity-id>
// links (or plain relative links when the target is not an entity).
func (r *Rewriter) Rewrite(body string) (string, RewriteStats) {
	var st RewriteStats
	nodes, err := xhtml.ParseFragment(strings.NewReader(body), &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Body, Data: "body"})
	if err != nil {
		return body, st
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Body, Data: "body"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	var drop, unwrap []*xhtml.Node
	var visit func(n *xhtml.Node)
	visit = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
		if n.Type != xhtml.ElementNode {
			return
		}
		switch n.DataAtom {
		case atom.Img, atom.Source, atom.Video, atom.Audio:
			r.media(n, "src", &st, &drop, nil)
			delAttr(n, "srcset")
		case atom.A:
			r.anchor(n, &st, &unwrap)
		}
	}
	visit(root)
	for _, n := range drop {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
	for _, n := range unwrap {
		if p := n.Parent; p != nil {
			for c := n.FirstChild; c != nil; {
				next := c.NextSibling
				n.RemoveChild(c)
				p.InsertBefore(c, n)
				c = next
			}
			p.RemoveChild(n)
		}
	}
	var b strings.Builder
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		_ = xhtml.Render(&b, c)
	}
	return strings.TrimSpace(b.String()), st
}

// media rewrites one URL attribute that points at a file of the old site.
func (r *Rewriter) media(n *xhtml.Node, key string, st *RewriteStats, drop, unwrap *[]*xhtml.Node) {
	raw := attr(n, key)
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || !IsWPContent(u.Path) {
		return // other hosts' wp-content files are imported too: nothing may point back into a WordPress tree
	}
	if nu, ok := r.Media(u); ok {
		setAttr(n, key, nu)
		st.MediaRewritten++
		return
	}
	st.Unresolved = append(st.Unresolved, raw)
	if drop != nil {
		*drop = append(*drop, n)
	}
	if unwrap != nil {
		*unwrap = append(*unwrap, n)
	}
}

func (r *Rewriter) anchor(n *xhtml.Node, st *RewriteStats, unwrap *[]*xhtml.Node) {
	href := attr(n, "href")
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
		return
	}
	u, err := url.Parse(href)
	if err != nil || !r.internal(u) {
		return
	}
	if IsWPContent(u.Path) {
		r.media(n, "href", st, nil, unwrap)
		return
	}
	if r.Entity != nil {
		if ref, ok := r.Entity(u.Path, u.Query()); ok {
			setAttr(n, "data-entity-id", ref.ID.String())
			setAttr(n, "href", ref.Path+fragment(u))
			st.LinksRewritten++
			return
		}
	}
	path := u.Path
	if r.Path != nil {
		if np, ok := r.Path(u.Path); ok {
			path = np
		}
	}
	if path == "" {
		path = "/"
	}
	q := ""
	if u.RawQuery != "" {
		q = "?" + u.RawQuery
	}
	setAttr(n, "href", path+q+fragment(u))
	st.LinksRewritten++
}

func fragment(u *url.URL) string {
	if u.Fragment != "" {
		return "#" + u.EscapedFragment()
	}
	return ""
}

// WPContentURLs returns every wp-content or wp-includes URL left in body, in
// attributes or text; the importer's "none remain" check.
func WPContentURLs(body string) []string {
	re := regexp.MustCompile(`https?://[^\s"'<>)]*/wp-(?:content|includes)/[^\s"'<>)]*|(?:^|["'(=\s])/wp-(?:content|includes)/[^\s"'<>)]*`)
	var out []string
	for _, m := range re.FindAllString(body, -1) {
		out = append(out, strings.TrimLeft(m, "\"'(= \t\n"))
	}
	return out
}
