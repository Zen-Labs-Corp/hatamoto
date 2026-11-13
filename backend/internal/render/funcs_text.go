package render

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"hatamoto.dev/core/internal/entity"
)

var rawBlockRe = regexp.MustCompile(`(?is)<(script|style)\b.*?(?:</(?:script|style)\s*>|$)`)

// plainTextMax is the richtext field limit, so nothing is cut.
const plainTextMax = 100000

// plainTextFn returns v as plain text: block tags become spaces, entities are
// decoded and whitespace is collapsed. "" when nothing is left.
func plainTextFn(v any) string { return entity.PlainText(toStr(v), plainTextMax) }

// excerptFn returns the plain text of s (markup removed, entities decoded,
// whitespace collapsed) cut to at most n runes at a word boundary, with "..."
// appended when it was cut. n <= 0 gives "".
func excerptFn(s, n any) string {
	limit := toInt(n)
	if limit <= 0 {
		return ""
	}
	text := rawBlockRe.ReplaceAllString(toStr(s), " ")
	text = tagRe.ReplaceAllString(text, " ")
	text = strings.ToValidUTF8(html.UnescapeString(text), "")
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	r := []rune(text)
	cut := limit
	if r[limit] != ' ' {
		if i := strings.LastIndex(string(r[:limit]), " "); i > 0 {
			cut = utf8.RuneCountInString(string(r[:limit])[:i])
		}
	}
	return strings.TrimSpace(string(r[:cut])) + "..."
}

// initialFn returns the first letter of s, upper-cased, or "" when s has none.
func initialFn(s any) string {
	for _, r := range toStr(s) {
		if unicode.IsLetter(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return ""
}

// mediaElements make a fragment non-blank even without text.
var mediaElements = map[atom.Atom]bool{
	atom.Img: true, atom.Iframe: true, atom.Video: true, atom.Audio: true,
	atom.Embed: true, atom.Object: true, atom.Svg: true, atom.Picture: true,
	atom.Canvas: true,
}

// isBlankHTML reports whether s shows nothing: no visible text (non-breaking
// spaces count as blank) and no media element. Empty or invalid input is blank.
func isBlankHTML(s any) bool {
	src := toStr(s)
	if strings.TrimSpace(src) == "" {
		return true
	}
	ctx := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := xhtml.ParseFragment(strings.NewReader(src), ctx)
	if err != nil {
		return true
	}
	var visible func(n *xhtml.Node) bool
	visible = func(n *xhtml.Node) bool {
		switch n.Type {
		case xhtml.TextNode:
			return strings.TrimFunc(n.Data, unicode.IsSpace) != ""
		case xhtml.ElementNode:
			if n.DataAtom == atom.Script || n.DataAtom == atom.Style {
				return false
			}
			if mediaElements[n.DataAtom] {
				return true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if visible(c) {
				return true
			}
		}
		return false
	}
	for _, n := range nodes {
		if visible(n) {
			return false
		}
	}
	return true
}

var (
	youtubeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	vimeoIDRe   = regexp.MustCompile(`^[0-9]+$`)
)

// videoID extracts the video id from a YouTube or Vimeo URL; anything else
// gives "".
func videoID(u any) string {
	parsed, err := url.Parse(strings.TrimSpace(toStr(u)))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	host = strings.TrimPrefix(host, "m.")
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	var id string
	switch host {
	case "youtube.com", "youtube-nocookie.com":
		switch {
		case len(parts) == 1 && parts[0] == "watch":
			id = parsed.Query().Get("v")
		case len(parts) == 2 && (parts[0] == "embed" || parts[0] == "shorts" || parts[0] == "live"):
			id = parts[1]
		}
		if !youtubeIDRe.MatchString(id) {
			return ""
		}
	case "youtu.be":
		if len(parts) >= 1 {
			id = parts[0]
		}
		if !youtubeIDRe.MatchString(id) {
			return ""
		}
	case "vimeo.com":
		if len(parts) == 1 {
			id = parts[0]
		}
		if !vimeoIDRe.MatchString(id) {
			return ""
		}
	case "player.vimeo.com":
		if len(parts) == 2 && parts[0] == "video" {
			id = parts[1]
		}
		if !vimeoIDRe.MatchString(id) {
			return ""
		}
	}
	return id
}

// percent returns a/b as a whole percent, rounded half up; b == 0 gives 0.
func percent(a, b any) int {
	x, y := int64(toInt(a)), int64(toInt(b))
	if y == 0 {
		return 0
	}
	if y < 0 {
		x, y = -x, -y
	}
	num, den := 200*x+y, 2*y
	q := num / den
	if num%den != 0 && num < 0 {
		q--
	}
	return int(q)
}
