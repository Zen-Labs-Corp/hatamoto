package wpimport

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Converted is the result of converting one body.
type Converted struct {
	HTML       string
	Shortcodes []string // names of shortcodes found (and removed)
	Forms      []string // form plugins whose markup or shortcode was removed
	Builders   []string // page builders whose markers were found
	Embeds     int      // YouTube and Vimeo embeds kept
	Dropped    []string // other things removed: unsupported iframes
}

var (
	blankLinesRe = regexp.MustCompile(`\n\s*\n(?:\s*\n)*`)
	commentRe    = regexp.MustCompile(`(?s)<!--.*?-->`)
	captionRe    = regexp.MustCompile(`(?s)\[caption[^\]]*\](.*?)\[/caption\]`)
	captionImg   = regexp.MustCompile(`(?s)^\s*(<a[^>]*>\s*<img[^>]*>\s*</a>|<img[^>]*>)\s*(.*?)\s*$`)
	embedSCRe    = regexp.MustCompile(`(?s)\[embed[^\]]*\](.*?)\[/embed\]`)
	shortcodeRe  = regexp.MustCompile(`\[(/?)([a-zA-Z][a-zA-Z0-9_-]{1,40})((?:\s[^\[\]]*)?)\]`)
	youtubeRe    = regexp.MustCompile(`^https?://(?:www\.|m\.)?(?:youtube\.com/(?:watch\?(?:[^#\s]*&)?v=|embed/|shorts/)|youtu\.be/|youtube-nocookie\.com/embed/)([A-Za-z0-9_-]{6,20})`)
	vimeoRe      = regexp.MustCompile(`^https?://(?:www\.)?(?:player\.)?vimeo\.com/(?:video/)?(\d{5,12})`)
	blockStart   = regexp.MustCompile(`(?i)^<(p|div|h[1-6]|ul|ol|li|table|blockquote|pre|figure|section|article|hr|iframe|form|aside|nav|header|footer|details|dl)[\s>/]`)
	// knownShortcodes are removed even without attributes (a bare "[gallery]").
	knownShortcodes = map[string]bool{"gallery": true, "caption": true, "embed": true, "audio": true, "video": true, "playlist": true, "wpforms": true}
)

// builderMarkers maps a page builder to substrings of its markup or shortcodes.
var builderMarkers = []struct {
	Name    string
	Markers []string
}{
	{"Elementor", []string{"data-elementor-type", `class="elementor`, "elementor-section", "elementor-widget", "_elementor_data"}},
	{"Divi", []string{"et_pb_", "et-boc", "et_builder"}},
	{"WPBakery", []string{"vc_row", "wpb_wrapper", "vc_column", "[vc_"}},
	{"Avada", []string{"fusion-builder", "fusion_builder", "[fusion_"}},
	{"Beaver Builder", []string{"fl-builder", "fl-row"}},
	{"Bricks", []string{"brxe-"}},
}

// formMarkers maps a form plugin to markup left in rendered content or to a shortcode name.
var formMarkers = []struct {
	Name      string
	Markers   []string
	Shortcode string
}{
	{"Contact Form 7", []string{"wpcf7"}, "contact-form-7"},
	{"Gravity Forms", []string{"gform_wrapper", "gform_wrapper"}, "gravityform"},
	{"WPForms", []string{"wpforms-container", "wpforms-form"}, "wpforms"},
	{"Ninja Forms", []string{"nf-form-cont"}, "ninja_form"},
	{"Formidable", []string{"frm_forms"}, "formidable"},
}

// DetectBuilders returns the page builders whose markers occur in s.
func DetectBuilders(s string) []string {
	var out []string
	for _, b := range builderMarkers {
		for _, m := range b.Markers {
			if strings.Contains(s, m) {
				out = append(out, b.Name)
				break
			}
		}
	}
	return out
}

// Convert turns a WordPress body into HTML the rich-text sanitiser keeps
// whole: block comments go, YouTube and Vimeo embeds become iframes (also from
// a bare URL), forms and shortcodes are removed and reported, responsive image
// attributes are dropped. raw is true for WXR bodies, which are stored as
// typed (shortcodes unexpanded, paragraphs not yet wrapped).
func Convert(in string, raw bool) Converted {
	var res Converted
	res.Builders = DetectBuilders(in)
	s := commentRe.ReplaceAllString(in, "")
	s = embedSCRe.ReplaceAllString(s, "<p>$1</p>")
	s = captionRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := captionRe.FindStringSubmatch(m)[1]
		if p := captionImg.FindStringSubmatch(inner); p != nil {
			cap := ""
			if strings.TrimSpace(p[2]) != "" {
				cap = "<figcaption>" + p[2] + "</figcaption>"
			}
			return "<figure>" + p[1] + cap + "</figure>"
		}
		return "<figure>" + inner + "</figure>"
	})
	if raw {
		s = autop(s)
	}
	nodes, err := xhtml.ParseFragment(strings.NewReader(s), &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Body, Data: "body"})
	if err != nil {
		res.HTML = s
		return res
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Body, Data: "body"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	cv := &converter{res: &res, shortcodes: map[string]bool{}, forms: map[string]bool{}}
	cv.walk(root)
	cv.finish()
	var b strings.Builder
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		_ = xhtml.Render(&b, c)
	}
	res.HTML = strings.TrimSpace(b.String())
	if !strings.Contains(res.HTML, "<pre") { // blank lines inside preformatted text are content
		res.HTML = blankLinesRe.ReplaceAllString(res.HTML, "\n")
	}
	return res
}

type converter struct {
	res        *Converted
	shortcodes map[string]bool
	forms      map[string]bool
	drop       []*xhtml.Node
	replace    map[*xhtml.Node]*xhtml.Node
}

func (c *converter) finish() {
	for n, with := range c.replace {
		if n.Parent != nil {
			n.Parent.InsertBefore(with, n)
			n.Parent.RemoveChild(n)
		}
	}
	for _, n := range c.drop {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
	for k := range c.shortcodes {
		c.res.Shortcodes = append(c.res.Shortcodes, k)
	}
	for k := range c.forms {
		c.res.Forms = append(c.res.Forms, k)
	}
	sort.Strings(c.res.Shortcodes)
	sort.Strings(c.res.Forms)
}

func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *xhtml.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, xhtml.Attribute{Key: key, Val: val})
}

func delAttr(n *xhtml.Node, keys ...string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		drop := false
		for _, k := range keys {
			if a.Key == k {
				drop = true
			}
		}
		if !drop {
			out = append(out, a)
		}
	}
	n.Attr = out
}

func hasClass(n *xhtml.Node, sub string) bool { return strings.Contains(attr(n, "class"), sub) }

func (c *converter) walk(n *xhtml.Node) {
	for ch := n.FirstChild; ch != nil; {
		next := ch.NextSibling
		c.visit(ch)
		ch = next
	}
}

func (c *converter) visit(n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		c.text(n)
		return
	case xhtml.ElementNode:
	default:
		return
	}
	if c.form(n) {
		return
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript:
		c.drop = append(c.drop, n)
		return
	case atom.Iframe:
		c.iframe(n)
		return
	case atom.Img:
		delAttr(n, "srcset", "sizes", "data-srcset", "data-sizes", "decoding", "fetchpriority")
		if attr(n, "src") == "" || strings.HasPrefix(attr(n, "src"), "data:") {
			if v := attr(n, "data-src"); v != "" {
				setAttr(n, "src", v)
			}
		}
		delAttr(n, "data-src", "data-lazy-src", "data-lazy-srcset")
		return
	case atom.Figure:
		if hasClass(n, "wp-block-embed") && c.embedWrapper(n) {
			return
		}
	case atom.P, atom.Div:
		if c.bareEmbed(n) {
			return
		}
	}
	c.walk(n)
}

// form removes the markup of a form plugin.
func (c *converter) form(n *xhtml.Node) bool {
	cls := attr(n, "class")
	if cls == "" && n.DataAtom != atom.Form {
		return false
	}
	for _, f := range formMarkers {
		for _, m := range f.Markers {
			if strings.Contains(cls, m) {
				c.forms[f.Name] = true
				c.drop = append(c.drop, n)
				return true
			}
		}
	}
	return false
}

// text removes shortcodes from a text node and reports them.
func (c *converter) text(n *xhtml.Node) {
	s := n.Data
	if !strings.Contains(s, "[") {
		return
	}
	closing := map[string]bool{}
	for _, m := range shortcodeRe.FindAllStringSubmatch(s, -1) {
		if m[1] == "/" {
			closing[m[2]] = true
		}
	}
	changed := false
	out := shortcodeRe.ReplaceAllStringFunc(s, func(m string) string {
		p := shortcodeRe.FindStringSubmatch(m)
		name, attrs := p[2], strings.TrimSpace(p[3])
		if p[1] != "/" && attrs == "" && !closing[name] && !knownShortcodes[name] {
			return m // "[sic]", "[1]": not a shortcode
		}
		changed = true
		c.shortcodes[name] = true
		for _, f := range formMarkers {
			if f.Shortcode == name {
				c.forms[f.Name] = true
			}
		}
		return ""
	})
	if !changed {
		return
	}
	n.Data = out
	if strings.TrimSpace(out) == "" {
		if p := n.Parent; p != nil && p.DataAtom == atom.P && p.FirstChild == n && p.LastChild == n {
			c.drop = append(c.drop, p)
		} else {
			c.drop = append(c.drop, n)
		}
	}
}

// iframe keeps YouTube and Vimeo players in the form the sanitiser allows and
// removes any other embed.
func (c *converter) iframe(n *xhtml.Node) {
	src := attr(n, "src")
	if src == "" {
		src = attr(n, "data-src")
	}
	if e := embedIframe(src); e != nil {
		n.Attr = e.Attr
		c.res.Embeds++
		return
	}
	c.res.Dropped = append(c.res.Dropped, "iframe "+src)
	c.drop = append(c.drop, n)
}

// embedWrapper converts a figure.wp-block-embed whose player was not rendered
// (only the URL is there); a rendered iframe is normalised by the walk.
func (c *converter) embedWrapper(fig *xhtml.Node) bool {
	if findAtom(fig, atom.Iframe) != nil {
		return false
	}
	u := strings.TrimSpace(textOf(fig))
	if e := embedIframe(u); e != nil {
		c.replaceWithEmbed(fig, e)
		return true
	}
	return false
}

// bareEmbed converts a paragraph or div that holds nothing but a video URL
// (alone or as a link to itself).
func (c *converter) bareEmbed(n *xhtml.Node) bool {
	if findAtom(n, atom.Iframe) != nil {
		return false
	}
	u := strings.TrimSpace(textOf(n))
	if u == "" || strings.ContainsAny(u, " \n\t") {
		return false
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == xhtml.ElementNode && ch.DataAtom != atom.A {
			return false
		}
	}
	if e := embedIframe(u); e != nil {
		c.replaceWithEmbed(n, e)
		return true
	}
	return false
}

func (c *converter) replaceWithEmbed(old, iframe *xhtml.Node) {
	fig := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Figure, Data: "figure", Attr: []xhtml.Attribute{{Key: "class", Val: "wp-block-embed is-type-video"}}}
	wrap := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div", Attr: []xhtml.Attribute{{Key: "class", Val: "wp-block-embed__wrapper"}}}
	wrap.AppendChild(iframe)
	fig.AppendChild(wrap)
	if c.replace == nil {
		c.replace = map[*xhtml.Node]*xhtml.Node{}
	}
	c.replace[old] = fig
	c.res.Embeds++
}

// embedIframe returns an <iframe> node for a YouTube or Vimeo URL (watch page
// or player), nil for anything else.
func embedIframe(u string) *xhtml.Node {
	u = html.UnescapeString(strings.TrimSpace(u))
	var src, title string
	switch {
	case youtubeRe.MatchString(u):
		src, title = "https://www.youtube.com/embed/"+youtubeRe.FindStringSubmatch(u)[1], "YouTube video"
	case vimeoRe.MatchString(u):
		src, title = "https://player.vimeo.com/video/"+vimeoRe.FindStringSubmatch(u)[1], "Vimeo video"
		if pu, err := url.Parse(u); err == nil && pu.Query().Get("h") != "" { // private video hash
			src += "?h=" + url.QueryEscape(pu.Query().Get("h"))
		}
	default:
		return nil
	}
	return &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Iframe, Data: "iframe", Attr: []xhtml.Attribute{
		{Key: "src", Val: src}, {Key: "width", Val: "560"}, {Key: "height", Val: "315"},
		{Key: "frameborder", Val: "0"}, {Key: "allowfullscreen", Val: ""}, {Key: "loading", Val: "lazy"}, {Key: "title", Val: title},
	}}
}

func findAtom(n *xhtml.Node, a atom.Atom) *xhtml.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.DataAtom == a {
			return c
		}
		if f := findAtom(c, a); f != nil {
			return f
		}
	}
	return nil
}

func textOf(n *xhtml.Node) string {
	var b strings.Builder
	var rec func(*xhtml.Node)
	rec = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return b.String()
}

// autop is a small wpautop: blank-line separated chunks that are not block
// elements become paragraphs, single newlines inside them line breaks.
func autop(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.Contains(s, "\n\n") && blockStart.MatchString(strings.TrimSpace(s)) {
		return s
	}
	var out []string
	for _, chunk := range regexp.MustCompile(`\n\s*\n`).Split(s, -1) {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		if blockStart.MatchString(chunk) {
			out = append(out, chunk)
			continue
		}
		out = append(out, "<p>"+strings.ReplaceAll(chunk, "\n", "<br>\n")+"</p>")
	}
	return strings.Join(out, "\n")
}

// String summarises the conversion for logs.
func (c Converted) String() string {
	return fmt.Sprintf("embeds=%d shortcodes=%v forms=%v builders=%v", c.Embeds, c.Shortcodes, c.Forms, c.Builders)
}
