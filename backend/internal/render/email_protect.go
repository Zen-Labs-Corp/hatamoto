package render

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// emailAddr matches a plausible address in page text: a local part, and a
// domain with a dot and a letters-only top level of two or more characters.
var emailAddr = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)

// assetName matches a retina file name such as logo@2x.png that looks like an address.
var assetName = regexp.MustCompile(`(?i)@\d+x\.(png|jpe?g|gif|webp|avif|svg)$`)

// emailSkipTags hold text that is not shown as prose: code stays literal so a
// documented address can be copied, and raw-text elements are not markup.
var emailSkipTags = [...]string{"script", "style", "textarea", "title", "code", "pre", "noscript", "xmp", "plaintext", "noembed", "noframes", "iframe"}

// emailRawText are the skip tags whose content the tokenizer reads as one raw
// text token, even after a self-closing start tag such as <title/>.
var emailRawText = map[string]bool{"script": true, "style": true, "textarea": true, "title": true, "noscript": true, "xmp": true, "plaintext": true, "noembed": true, "noframes": true, "iframe": true}

// skipIndex is the index of name in emailSkipTags, or -1.
func skipIndex(name []byte) int {
	for i, t := range emailSkipTags {
		if string(name) == t {
			return i
		}
	}
	return -1
}

// protectEmails rewrites mailto: hrefs and addresses in page text to numeric
// character references. Browsers show and follow them as before; simple
// harvesters that match the literal address or "mailto:" find nothing. Every
// token is copied byte for byte unless it is rewritten, so markup without an
// address is returned unchanged. Scripts, styles, textareas, titles, code,
// pre, comments and everything in <head> stay literal, and only the href of a
// mailto link is touched among attributes.
func protectEmails(in []byte) []byte {
	if !emailAddr.Match(in) && !containsFold(in, "mailto") {
		return in
	}
	z := html.NewTokenizer(bytes.NewReader(in))
	out := make([]byte, 0, len(in)+len(in)/8)
	var skip [len(emailSkipTags)]int
	skipping := 0
	inHead := false
	rawNext := false // the next token is raw text after a self-closing <title/>
	for {
		tt := z.Next()
		raw := z.Raw()
		skipText := rawNext
		rawNext = false
		switch tt {
		case html.ErrorToken:
			return append(out, raw...) // an unterminated tag at the end
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			if string(name) == "head" && tt == html.StartTagToken {
				inHead = true
			}
			if i := skipIndex(name); i >= 0 {
				if tt == html.StartTagToken {
					skip[i]++
					skipping++
				} else if emailRawText[string(name)] {
					rawNext = true
				}
			}
			if skipping == 0 && !inHead && string(name) == "a" {
				raw = rewriteMailtoHref(raw)
			}
			out = append(out, raw...)
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "head" {
				inHead = false
			}
			if i := skipIndex(name); i >= 0 && skip[i] > 0 {
				skip[i]--
				skipping--
			}
			out = append(out, raw...)
		case html.TextToken:
			if skipping > 0 || inHead || skipText {
				out = append(out, raw...)
				continue
			}
			if bytes.IndexByte(raw, '@') < 0 {
				out = append(out, raw...)
				continue
			}
			out = appendEncodedAddrs(out, raw)
		default:
			out = append(out, raw...)
		}
	}
}

// appendEncodedAddrs appends text with every address replaced by character references.
func appendEncodedAddrs(out, text []byte) []byte {
	last := 0
	for _, m := range emailAddr.FindAllIndex(text, -1) {
		out = append(out, text[last:m[0]]...)
		if assetName.Match(text[m[0]:m[1]]) {
			last = m[0] // an image name like logo@2x.png, not an address
			continue
		}
		out = append(out, numRefs(string(text[m[0]:m[1]]))...)
		last = m[1]
	}
	return append(out, text[last:]...)
}

// numRefs encodes every rune of s as a decimal character reference.
func numRefs(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 6)
	for _, r := range s {
		b.WriteString("&#")
		b.WriteString(strconv.Itoa(int(r)))
		b.WriteByte(';')
	}
	return b.String()
}

// rewriteMailtoHref replaces the raw value of the href attribute of a start
// tag with character references when it is a mailto link; any other tag is
// returned as is.
func rewriteMailtoHref(tag []byte) []byte {
	start, end, ok := hrefSpan(tag)
	if !ok {
		return tag
	}
	val := strings.TrimSpace(html.UnescapeString(string(unquote(tag[start:end]))))
	if len(val) < len("mailto:") || !strings.EqualFold(val[:len("mailto:")], "mailto:") {
		return tag
	}
	out := make([]byte, 0, len(tag)*4)
	out = append(out, tag[:start]...)
	out = append(out, '"')
	out = append(out, numRefs(val)...)
	out = append(out, '"')
	return append(out, tag[end:]...)
}

func unquote(v []byte) []byte {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// hrefSpan locates the raw value (quotes included) of the href attribute in a
// start tag, scanning attribute by attribute so a value that merely contains
// "href=" is never mistaken for it.
func hrefSpan(tag []byte) (start, end int, ok bool) {
	i := 1 // after '<'
	for i < len(tag) && !isTagSpace(tag[i]) && tag[i] != '>' && tag[i] != '/' {
		i++
	}
	for i < len(tag) {
		for i < len(tag) && (isTagSpace(tag[i]) || tag[i] == '/') {
			i++
		}
		if i >= len(tag) || tag[i] == '>' {
			return 0, 0, false
		}
		ns := i
		for i < len(tag) && !isTagSpace(tag[i]) && tag[i] != '=' && tag[i] != '/' && tag[i] != '>' {
			i++
		}
		name := tag[ns:i]
		j := i
		for j < len(tag) && isTagSpace(tag[j]) {
			j++
		}
		if j >= len(tag) || tag[j] != '=' {
			continue // attribute without a value
		}
		j++
		for j < len(tag) && isTagSpace(tag[j]) {
			j++
		}
		vs := j
		if j < len(tag) && (tag[j] == '"' || tag[j] == '\'') {
			q := tag[j]
			j++
			for j < len(tag) && tag[j] != q {
				j++
			}
			if j < len(tag) {
				j++
			}
		} else {
			for j < len(tag) && !isTagSpace(tag[j]) && tag[j] != '>' {
				j++
			}
		}
		if strings.EqualFold(string(name), "href") {
			return vs, j, true
		}
		i = j
	}
	return 0, 0, false
}

func isTagSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// containsFold reports whether b holds the lower-case sub, ignoring ASCII
// case, without copying b.
func containsFold(b []byte, sub string) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		if b[i]|0x20 == sub[0] && strings.EqualFold(string(b[i:i+len(sub)]), sub) {
			return true
		}
	}
	return false
}
