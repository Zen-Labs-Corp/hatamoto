package commentfmt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// MaxBodyBytes is the longest body Render accepts.
const MaxBodyBytes = 16 << 10

// Rel is the rel value every rendered link carries.
const Rel = "ugc nofollow noopener"

// ErrEmpty is returned for a body with no visible content.
var ErrEmpty = errors.New("commentfmt: empty body")

// ErrTooLong is returned for a body longer than MaxBodyBytes.
var ErrTooLong = errors.New("commentfmt: body too long")

// Result is a rendered comment.
type Result struct {
	HTML        string
	HasLinks    bool
	LinkDomains []string
	BodyHash    string
}

var (
	reBlankLine = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)*`)
	reCode      = regexp.MustCompile("`([^`\n]+)`")
	reStrong    = regexp.MustCompile(`\*\*([^*\n](?:[^*\n]*[^*\n])?)\*\*`)
	reEm        = regexp.MustCompile(`\*([^*\s](?:[^*\n]*[^*\s])?)\*`)
	rePlace     = regexp.MustCompile("\x00(\\d+)\x00")
	reSpaces    = regexp.MustCompile(`\s+`)
	reRel       = regexp.MustCompile(`^ugc nofollow noopener$`)
)

// policy is the defence-in-depth allowlist applied to the generated markup.
var policy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "em", "strong", "code", "pre", "blockquote")
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("rel").Matching(reRel).OnElements("a")
	p.AllowURLSchemes("http", "https")
	p.RequireParseableURLs(true)
	return p
}()

// Render turns a plain-text comment body into safe HTML. The text is escaped
// first and only then wrapped in the few tags the format allows (paragraphs,
// line breaks, `code`, **strong**, *em*, indented code blocks, "> " quotes and
// http/https autolinks); the result is then sanitised and re-serialised.
func Render(raw string) (Result, error) {
	if len(raw) > MaxBodyBytes+MaxBodyBytes/4 {
		return Result{}, ErrTooLong
	}
	body := normalise(raw)
	if strings.TrimSpace(body) == "" {
		return Result{}, ErrEmpty
	}
	if len(body) > MaxBodyBytes {
		return Result{}, ErrTooLong
	}

	var b strings.Builder
	for _, block := range reBlankLine.Split(body, -1) {
		b.WriteString(renderBlock(block))
	}
	out := sanitise(b.String())
	return Result{
		HTML:        out,
		HasLinks:    HasLinks(raw) || strings.Contains(out, "<a "),
		LinkDomains: Domains(FindLinks(raw)),
		BodyHash:    BodyHash(raw),
	}, nil
}

// BodyHash is the hex SHA-256 of the body lowercased, with every whitespace run
// collapsed to one space and the ends trimmed.
func BodyHash(raw string) string {
	s := strings.TrimSpace(reSpaces.ReplaceAllString(strings.ToLower(raw), " "))
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// normalise unifies newlines and drops invalid UTF-8, NUL and other C0
// controls (except \n and \t), DEL and bidirectional overrides, then trims
// surrounding blank lines and trailing space.
func normalise(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) && isBidi(r):
			return -1
		}
		return r
	}, s)
	return strings.TrimRight(strings.Trim(s, "\n"), " \t\n")
}

func isBidi(r rune) bool {
	return r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 || r == 0x200E || r == 0x200F
}

func renderBlock(block string) string {
	lines := strings.Split(block, "\n")
	if allPrefixed(lines, "    ", "\t") {
		for i, l := range lines {
			lines[i] = strings.TrimPrefix(strings.TrimPrefix(l, "\t"), "    ")
		}
		return "<pre><code>" + html.EscapeString(strings.Join(lines, "\n")) + "</code></pre>"
	}
	if allPrefixed(lines, ">") {
		for i, l := range lines {
			l = strings.TrimPrefix(l, ">")
			lines[i] = strings.TrimPrefix(l, " ")
		}
		return "<blockquote><p>" + inline(strings.Join(lines, "\n")) + "</p></blockquote>"
	}
	return "<p>" + inline(block) + "</p>"
}

func allPrefixed(lines []string, prefixes ...string) bool {
	for _, l := range lines {
		ok := false
		for _, p := range prefixes {
			if strings.HasPrefix(l, p) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// inline renders the unescaped text of one block: code spans first (their
// contents are never processed further), then links, strong and em on the
// escaped remainder.
func inline(text string) string {
	var out strings.Builder
	last := 0
	for _, m := range reCode.FindAllStringSubmatchIndex(text, -1) {
		out.WriteString(inlineText(text[last:m[0]]))
		out.WriteString("<code>" + html.EscapeString(text[m[2]:m[3]]) + "</code>")
		last = m[1]
	}
	out.WriteString(inlineText(text[last:]))
	return out.String()
}

// inlineText handles a stretch of text without code spans. Links become
// placeholders so the escaping and emphasis passes cannot touch their markup.
func inlineText(text string) string {
	var anchors []string
	var b strings.Builder
	last := 0
	for _, l := range FindLinks(text) {
		if !l.Linkable {
			continue
		}
		b.WriteString(text[last:l.Start])
		href := l.URL
		if !isHTTP(href) {
			href = "http://" + href
		}
		anchors = append(anchors, `<a href="`+html.EscapeString(href)+`" rel="`+Rel+`">`+html.EscapeString(l.URL)+`</a>`)
		b.WriteString("\x00" + strconv.Itoa(len(anchors)-1) + "\x00")
		last = l.End
	}
	b.WriteString(text[last:])

	s := html.EscapeString(b.String())
	s = reStrong.ReplaceAllString(s, "<strong>$1</strong>")
	s = reEm.ReplaceAllString(s, "<em>$1</em>")
	s = strings.ReplaceAll(s, "\n", "<br>")
	return rePlace.ReplaceAllStringFunc(s, func(m string) string {
		i, _ := strconv.Atoi(m[1 : len(m)-1])
		return anchors[i]
	})
}

// sanitise runs the bluemonday allowlist, re-parses the result and writes it
// back in canonical form: only the allowed elements, links with exactly an
// http(s) href and the fixed rel (anything else is unwrapped to its text).
func sanitise(s string) string {
	clean := policy.Sanitize(s)
	div := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := xhtml.ParseFragment(strings.NewReader(clean), div)
	if err != nil {
		return html.EscapeString(clean)
	}
	var b strings.Builder
	for _, n := range nodes {
		write(&b, n)
	}
	return b.String()
}

var textEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func write(b *strings.Builder, n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		b.WriteString(textEsc.Replace(n.Data))
		return
	case xhtml.ElementNode:
	default:
		return
	}
	switch n.DataAtom {
	case atom.Br:
		b.WriteString("<br>")
	case atom.P, atom.Em, atom.Strong, atom.Code, atom.Pre, atom.Blockquote:
		b.WriteString("<" + n.Data + ">")
		writeChildren(b, n)
		b.WriteString("</" + n.Data + ">")
	case atom.A:
		if href := hrefOf(n); href != "" {
			b.WriteString(`<a href="` + strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(href) + `" rel="` + Rel + `">`)
			writeChildren(b, n)
			b.WriteString("</a>")
		} else {
			writeChildren(b, n)
		}
	default:
		writeChildren(b, n)
	}
}

func writeChildren(b *strings.Builder, n *xhtml.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		write(b, c)
	}
}

// hrefOf returns the href of an <a> node when it is an absolute http(s) URL.
func hrefOf(n *xhtml.Node) string {
	for _, a := range n.Attr {
		if a.Key != "href" {
			continue
		}
		u, err := url.Parse(a.Val)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !utf8.ValidString(a.Val) {
			return ""
		}
		return a.Val
	}
	return ""
}
