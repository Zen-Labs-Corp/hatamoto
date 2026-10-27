package render

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// A plugin widget's own markup is sanitised once its template has run. The
// validator already refuses what it can see in the template source; this is the
// second wall, for whatever the validator's model of the markup got wrong.
//
// The core parts of the output are not the plugin's and must not be touched:
// the root attributes ({{.Attrs}}, with data-hm-* in the canvas) and the
// rendered child nodes. They go into the template as opaque placeholders (an
// attribute and an element whose names carry a per-process random nonce),
// pass through the sanitiser as the only unknown names it
// allows, and are spliced back afterwards. The nonce is not a secret (a
// template can print it); safety comes from html/template escaping every prop
// and from the sanitiser re-serialising the output, so only a real element or
// attribute can match a placeholder.

var packNonce = func() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("render: no randomness for the widget sanitiser: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}()

var (
	packAttrsHolder    = "hma" + packNonce + `=""`
	packChildrenTag    = "hmc" + packNonce
	packChildrenHolder = "<" + packChildrenTag + "></" + packChildrenTag + ">"
)

var errPackChildrenOnce = errors.New("widget prints its children more than once")

// packPolicy is the strict policy for plugin widget output: common structure
// and text elements, safe global attributes, http(s)/mailto/tel/relative URLs,
// and no script, style, frames, embeds, forms, handlers or inline style.
var packPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(
		"div", "span", "p", "br", "hr", "wbr", "a", "img", "picture", "ul", "ol", "li", "dl", "dt", "dd",
		"h1", "h2", "h3", "h4", "h5", "h6", "strong", "em", "b", "i", "u", "s", "small", "mark", "sub", "sup",
		"abbr", "cite", "q", "code", "kbd", "samp", "pre", "blockquote", "del", "ins", "time",
		"section", "article", "aside", "nav", "figure", "figcaption",
		"table", "caption", "thead", "tbody", "tfoot", "tr", "th", "td", "details", "summary",
		"button", "label", "input", "select", "option", "optgroup", "progress", "meter",
		packChildrenTag,
	)
	p.AllowAttrs("class", "id", "role", "title", "lang", "dir", "tabindex").Globally()
	p.AllowNoAttrs().OnElements(packChildrenTag)
	p.AllowAttrs("hma" + packNonce).Globally()
	p.AllowDataAttributes() // data-hm-* is stripped afterwards
	p.AllowAttrs("aria-label", "aria-labelledby", "aria-describedby", "aria-hidden", "aria-expanded", "aria-controls",
		"aria-current", "aria-live", "aria-atomic", "aria-busy", "aria-checked", "aria-disabled", "aria-haspopup",
		"aria-level", "aria-pressed", "aria-selected", "aria-valuemin", "aria-valuemax", "aria-valuenow",
		"aria-valuetext", "aria-orientation", "aria-modal", "aria-multiline", "aria-readonly", "aria-required",
		"aria-posinset", "aria-setsize", "aria-sort", "aria-relevant", "aria-owns", "aria-details", "aria-description",
		"aria-errormessage", "aria-invalid", "aria-colcount", "aria-colindex", "aria-colspan", "aria-rowcount",
		"aria-rowindex", "aria-rowspan", "aria-keyshortcuts", "aria-roledescription", "aria-autocomplete",
		"aria-activedescendant", "aria-flowto", "aria-placeholder", "aria-dropeffect", "aria-grabbed").Globally()
	// No target: a link that opens a new tab with rel="opener" would hand
	// the page over (reverse tabnabbing), so links stay in the tab.
	p.AllowAttrs("href", "rel").OnElements("a")
	p.AllowAttrs("src", "alt", "width", "height", "loading", "decoding").OnElements("img")
	p.AllowAttrs("colspan", "rowspan").OnElements("td", "th")
	p.AllowAttrs("datetime").OnElements("time", "del", "ins")
	p.AllowAttrs("cite").OnElements("blockquote", "q")
	p.AllowAttrs("open").OnElements("details")
	p.AllowAttrs("for").OnElements("label")
	p.AllowAttrs("type", "value", "placeholder", "checked", "disabled", "selected", "min", "max", "step", "name").OnElements("button", "input", "select", "option", "progress", "meter")
	p.AllowAttrs("value", "max", "min").OnElements("progress", "meter")
	p.AllowURLSchemes("http", "https", "mailto", "tel")
	p.AllowRelativeURLs(true)
	p.RequireParseableURLs(true)
	return p
}()

// sanitizePackOutput sanitises the template output of a pack widget, then
// splices the core attributes and children back. changed reports whether the
// sanitiser altered the plugin's markup.
func sanitizePackOutput(out []byte, attrs, children string) (res []byte, changed bool, err error) {
	san := packPolicy.SanitizeBytes(out)
	changed = !bytes.Equal(san, out)
	// bluemonday works on tokens and does not balance tags; an unclosed <a>
	// would turn the rest of the page into a link, a stray </main> would end
	// the theme's <main>. Parsing the output as the content of a <div> closes
	// what is open and drops stray end tags, so the widget stays in its box.
	// (Re-rendering also writes <img ... /> for void elements, so a byte
	// difference is not reported as a sanitiser change.)
	san, err = balancePackMarkup(san)
	if err != nil {
		return nil, changed, err
	}
	if bytes.Contains(san, []byte("data-hm-")) {
		san = stripHMData(san)
		changed = true
	}
	if n := bytes.Count(san, []byte(packChildrenHolder)); n > 1 {
		return nil, changed, errPackChildrenOnce
	}
	san = bytes.Replace(san, []byte(packChildrenHolder), []byte(children), 1)
	san = bytes.ReplaceAll(san, []byte(packAttrsHolder), []byte(attrs))
	return san, changed, nil
}

var errPackOneRoot = errors.New("widget output must be exactly one top-level element")

// balancePackMarkup re-parses a fragment in a <div> context and renders it
// again, so every element is closed inside the fragment and no end tag reaches
// out of it. The placeholders survive: they are ordinary elements/attributes.
func balancePackMarkup(b []byte) ([]byte, error) {
	nodes, err := html.ParseFragment(bytes.NewReader(b), &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	roots := 0
	for _, n := range nodes {
		if n.Type == html.TextNode && len(bytes.TrimSpace([]byte(n.Data))) == 0 {
			continue
		}
		if n.Type != html.ElementNode {
			return nil, errPackOneRoot
		}
		if roots++; roots > 1 {
			return nil, errPackOneRoot
		}
	}
	if roots != 1 {
		return nil, errPackOneRoot
	}
	for _, n := range nodes {
		if err := html.Render(&out, n); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

// optionalEnd are the elements whose end tag HTML lets an author leave out.
var optionalEnd = map[string]bool{
	"p": true, "li": true, "dt": true, "dd": true, "tr": true, "td": true, "th": true,
	"thead": true, "tbody": true, "tfoot": true, "caption": true, "option": true, "optgroup": true,
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true,
}

// checkPackBalance refuses markup with an element left open or an end tag that
// closes nothing. Elements with an optional end tag may stay open.
func checkPackBalance(file, what, markup string) error {
	z := html.NewTokenizer(strings.NewReader(markup))
	var stack []string
	for {
		switch z.Next() {
		case html.ErrorToken:
			for _, open := range stack {
				if !optionalEnd[open] {
					return packErr(file, "unbalanced-markup", "%s: <%s> is never closed", what, open)
				}
			}
			return nil
		case html.StartTagToken:
			if name := z.Token().Data; !voidElements[name] {
				stack = append(stack, name)
			}
		case html.EndTagToken:
			name := z.Token().Data
			if voidElements[name] {
				continue
			}
			i := len(stack) - 1
			for i >= 0 && stack[i] != name {
				if !optionalEnd[stack[i]] {
					return packErr(file, "unbalanced-markup", "%s: </%s> does not match the open <%s>", what, name, stack[i])
				}
				i--
			}
			if i < 0 {
				return packErr(file, "unbalanced-markup", "%s: </%s> closes nothing", what, name)
			}
			stack = stack[:i]
		}
	}
}

// stripHMData drops the attributes the plugin may not set: data-hm-* belongs
// to the core (node ids and editor hooks).
func stripHMData(b []byte) []byte {
	var out bytes.Buffer
	z := html.NewTokenizer(bytes.NewReader(b))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out.Bytes()
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(z.Raw())
			continue
		}
		tok := z.Token()
		keep := tok.Attr[:0]
		for _, a := range tok.Attr {
			if !strings.HasPrefix(strings.ToLower(a.Key), "data-hm-") {
				keep = append(keep, a)
			}
		}
		tok.Attr = keep
		out.WriteString(tok.String())
	}
}

// packSanitizeLog allows one log line per kind and minute.
var packSanitizeLog sync.Map

func packSanitizeLogDue(kind string) bool {
	now := time.Now()
	if last, ok := packSanitizeLog.Load(kind); ok && now.Sub(last.(time.Time)) < time.Minute {
		return false
	}
	packSanitizeLog.Store(kind, now)
	return true
}
