package render

import (
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

// MaxCustomCSS is the largest owner-supplied stylesheet accepted, in bytes.
const MaxCustomCSS = 64 << 10

// unsafeCSS matches constructs that run script or load code from a stylesheet.
// It is the one deny list: safeCSS (theme fields) and ValidateCustomCSS (owner
// CSS) both run it over the normalised text from scanCSS. Each alternative is
// its own group so a hit can be named. It is best-effort; breaking out of the
// <style> element is prevented by escaping, not by this.
var unsafeCSS = regexp.MustCompile(`(?i)(@import)|(expression\s*\()|(javascript:|vbscript:)|(behavior\s*:)|(-moz-binding)`)

// unsafeCSSMessages names each unsafeCSS group, in order.
var unsafeCSSMessages = [...]string{
	"@import is not allowed",
	"expression() is not allowed",
	"javascript: URLs are not allowed",
	"behavior is not allowed",
	"-moz-binding is not allowed",
}

// CSSIssue is why a stylesheet was refused. Line is 1-based, 0 when the
// problem is not tied to a line.
type CSSIssue struct {
	Line    int
	Message string
}

func (i *CSSIssue) Error() string {
	if i.Line > 0 {
		return "line " + strconv.Itoa(i.Line) + ": " + i.Message
	}
	return i.Message
}

// ValidateCustomCSS returns nil when s may be served as an owner stylesheet.
// It refuses oversize input, NUL bytes, unterminated comments, strings and
// url(), and any unsafeCSS construct, including ones hidden by comments or CSS
// escapes. A comment separates tokens, so java/**/script: is not a hit
// (browsers do not read it as javascript: either).
func ValidateCustomCSS(s string) *CSSIssue {
	if len(s) > MaxCustomCSS {
		return &CSSIssue{Message: "CSS is over the " + strconv.Itoa(MaxCustomCSS>>10) + " KB limit"}
	}
	_, issue := scanCSS(s)
	return issue
}

// SanitizeCustomCSS returns s when it is non-blank and passes
// ValidateCustomCSS. It is applied again at serve time.
func SanitizeCustomCSS(s string) (string, bool) {
	if strings.TrimSpace(s) == "" || ValidateCustomCSS(s) != nil {
		return "", false
	}
	return s, true
}

// scanCSS normalises s for deny-list matching: comments become one space,
// CSS escapes are decoded, strings and unquoted url() targets are kept as
// they are. It returns the first issue, naming the line of the offence.
func scanCSS(s string) (string, *CSSIssue) {
	var out []byte
	var lines []int // line of each byte of out
	line := 1
	emit := func(l int, bs ...byte) {
		for _, c := range bs {
			out = append(out, c)
			lines = append(lines, l)
		}
	}
	// escape decodes the escape starting at s[i] == '\\'; it returns the next
	// index and the decoded text ("" for a line continuation).
	escape := func(i int) (int, string) {
		i++
		if i >= len(s) {
			return i, ""
		}
		c := s[i]
		switch {
		case c == '\n' || c == '\f':
			line++
			return i + 1, ""
		case c == '\r':
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			line++
			return i + 1, ""
		case isHex(c):
			j := i
			for j < len(s) && j-i < 6 && isHex(s[j]) {
				j++
			}
			n, _ := strconv.ParseUint(s[i:j], 16, 32)
			if j < len(s) {
				switch s[j] {
				case ' ', '\t', '\n', '\f':
					if s[j] == '\n' || s[j] == '\f' {
						line++
					}
					j++
				case '\r':
					j++
					if j < len(s) && s[j] == '\n' {
						j++
					}
					line++
				}
			}
			if n == 0 || n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
				n = 0xFFFD
			}
			return j, string(rune(n))
		}
		return i + 1, string(s[i])
	}
	startsURL := func(i int) bool {
		return i >= 3 && strings.EqualFold(s[i-3:i+1], "url(")
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0:
			return "", &CSSIssue{Line: line, Message: "NUL bytes are not allowed"}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			start := line
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return "", &CSSIssue{Line: start, Message: "comment is not closed"}
			}
			body := s[i+2 : i+2+end]
			if strings.IndexByte(body, 0) >= 0 {
				return "", &CSSIssue{Line: start, Message: "NUL bytes are not allowed"}
			}
			line += strings.Count(body, "\n")
			emit(start, ' ')
			i += end + 4
		case c == '"' || c == '\'':
			start := line
			emit(line, c)
			i++
			closed := false
			for i < len(s) && !closed {
				switch d := s[i]; {
				case d == 0:
					return "", &CSSIssue{Line: line, Message: "NUL bytes are not allowed"}
				case d == c:
					emit(line, d)
					i++
					closed = true
				case d == '\n' || d == '\r' || d == '\f':
					return "", &CSSIssue{Line: start, Message: "string is not closed"}
				case d == '\\':
					l := line
					var dec string
					i, dec = escape(i)
					emit(l, []byte(dec)...)
				default:
					emit(line, d)
					i++
				}
			}
			if !closed {
				return "", &CSSIssue{Line: start, Message: "string is not closed"}
			}
		case c == '(' && startsURL(i):
			emit(line, c)
			i++
			for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f') {
				if s[i] == '\n' {
					line++
				}
				emit(line, s[i])
				i++
			}
			if i < len(s) && (s[i] == '"' || s[i] == '\'') {
				continue // a quoted target is handled as a string
			}
			start := line
			closed := false
			for i < len(s) && !closed {
				switch d := s[i]; {
				case d == 0:
					return "", &CSSIssue{Line: line, Message: "NUL bytes are not allowed"}
				case d == ')':
					emit(line, d)
					i++
					closed = true
				case d == '\\':
					l := line
					var dec string
					i, dec = escape(i)
					emit(l, []byte(dec)...)
				default:
					if d == '\n' {
						line++
					}
					emit(line, d)
					i++
				}
			}
			if !closed {
				return "", &CSSIssue{Line: start, Message: "url( is not closed"}
			}
		case c == '\\':
			l := line
			var dec string
			i, dec = escape(i)
			emit(l, []byte(dec)...)
		default:
			if c == '\n' {
				line++
				emit(line-1, c)
			} else {
				emit(line, c)
			}
			i++
		}
	}
	norm := string(out)
	if m := unsafeCSS.FindStringSubmatchIndex(norm); m != nil {
		for g := 1; g <= len(unsafeCSSMessages); g++ {
			if m[2*g] >= 0 {
				msg := unsafeCSSMessages[g-1]
				if g == 3 && strings.HasPrefix(strings.ToLower(norm[m[0]:]), "vbscript:") {
					msg = "vbscript: URLs are not allowed"
				}
				return "", &CSSIssue{Line: lines[m[0]], Message: msg}
			}
		}
	}
	return norm, nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// cssLT is a CSS hex escape for "<" plus the terminating space. It is valid in
// strings, comments and unquoted url().
const cssLT = `\3c `

// escapeLT escapes each "<" that could start a tag in any HTML tokenizer
// context (including <svg><style>, which browsers parse as markup): one
// followed by "/", "!", "?" or an ASCII letter, or the last byte of s, since a
// later value joined in the same <style> could supply the rest. Any other "<"
// stays, so media range syntax such as (width < 600px) keeps working.
// ">" needs no escape: without "<" it cannot open or close a tag.
func escapeLT(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '<' {
			b.WriteByte(c)
			continue
		}
		if i+1 == len(s) {
			b.WriteString(cssLT)
			continue
		}
		switch n := s[i+1]; {
		case n == '/' || n == '!' || n == '?', n >= 'a' && n <= 'z', n >= 'A' && n <= 'Z':
			b.WriteString(cssLT)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// safeCSS returns s as trusted CSS for a theme's <style> element, with each
// tag-starting "<" escaped, or "" when it holds an unsafe construct. It runs at render time
// only; stored content is never rewritten.
func safeCSS(s string) template.CSS {
	if _, issue := scanCSS(s); issue != nil {
		return ""
	}
	return template.CSS(escapeLT(s)) // #nosec G203 -- tag-starting "<" escaped, deny-list above; themes render it only inside <style>
}
