package schemafield

import (
	"html"
	"net/url"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

// maxDecodeRounds bounds the decode loop, so a string built to decode forever
// cannot stall the gate. A string still changing after the last round is read
// in its last form.
const maxDecodeRounds = 8

// imageTags are elements that load a picture, a clip or an embedded document.
var imageTags = map[string]bool{
	"img": true, "picture": true, "source": true, "video": true, "audio": true,
	"svg": true, "object": true, "embed": true, "iframe": true,
}

// imageAttrs load a picture whatever element carries them.
var imageAttrs = map[string]bool{"srcset": true, "poster": true, "background": true}

var (
	cssURLRe   = regexp.MustCompile(`(?:^|[^a-z0-9_-])url\s*\(`)
	dataURIRe  = regexp.MustCompile(`(?:^|[^a-z0-9_-])data:[a-z0-9]`)
	svgImageRe = regexp.MustCompile(`<\s*(?:image|use)\b`)
)

// DecodeUntilStable undoes HTML entities and percent-encoding, repeatedly, so a
// doubly encoded string reads as the browser or the renderer would finally see
// it. It stops when a round changes nothing, or after a fixed number of rounds.
func DecodeUntilStable(s string) string {
	for i := 0; i < maxDecodeRounds; i++ {
		n := html.UnescapeString(s)
		if u, err := url.PathUnescape(n); err == nil {
			n = u
		}
		if n == s {
			break
		}
		s = n
	}
	return s
}

// MentionsImageMarkup reports whether a string carries image or media markup:
// an img, picture, source, video, audio, svg, object, embed or iframe element,
// a srcset, poster or background attribute, an SVG image or use element, a CSS
// url( in a style attribute or element, a markdown image or a data: URI. It
// looks at the string as written and after decoding entities and percent
// escapes until stable, case-insensitively. It is deliberately broad: a false
// positive only makes a change wait for a person.
func MentionsImageMarkup(s string) bool {
	if s == "" {
		return false
	}
	seen := map[string]bool{}
	for cur, i := s, 0; i <= maxDecodeRounds && !seen[cur]; i++ {
		seen[cur] = true
		if markupHit(strings.ToLower(cur)) {
			return true
		}
		n := html.UnescapeString(cur)
		if u, err := url.PathUnescape(n); err == nil {
			n = u
		}
		cur = n
	}
	return false
}

// scanHook, when set by a test, receives the length of every string a markup
// scan reads, so a test can bound the work done without timing it.
var scanHook func(n int)

// markupHit tests one already lowercased form.
func markupHit(l string) bool {
	if scanHook != nil {
		scanHook(len(l))
	}
	if strings.Contains(l, "![") || cssURLRe.MatchString(l) || dataURIRe.MatchString(l) || svgImageRe.MatchString(l) {
		return true
	}
	if !strings.Contains(l, "<") {
		return false
	}
	z := xhtml.NewTokenizer(strings.NewReader(l))
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return false
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if imageTags[string(name)] {
				return true
			}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				key := string(k)
				if imageAttrs[key] || (key == "style" && cssURLRe.MatchString(string(v))) {
					return true
				}
				if (key == "href" || key == "xlink:href") && (string(name) == "image" || string(name) == "use") {
					return true
				}
			}
			if string(name) == "image" || string(name) == "use" {
				return true
			}
		}
	}
}

// anyAttr reports whether pred holds for any attribute of any tag in an HTML
// string, tokenised once. The tokenizer decodes entities in the values, and a
// value is never parsed as markup again, so the work stays linear.
func anyAttr(s string, pred func(key, val string) bool) bool {
	if scanHook != nil {
		scanHook(len(s))
	}
	if !strings.Contains(s, "<") {
		return false
	}
	z := xhtml.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return false
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			_, hasAttr := z.TagName()
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if pred(string(k), string(v)) {
					return true
				}
			}
		}
	}
}
