package render

import (
	"html/template"
	"regexp"
	"strings"
)

// unsafeCSS matches constructs that run script or load code from a stylesheet.
// It is defence in depth: a field containing any of them is dropped whole.
// It is best-effort (CSS escapes can bypass it). Breaking out of the
// <style> element is prevented by escaping, not by this.
var unsafeCSS = regexp.MustCompile(`(?i)@import|expression\s*\(|javascript:|behavior\s*:|-moz-binding`)

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
	if unsafeCSS.MatchString(s) {
		return ""
	}
	return template.CSS(escapeLT(s)) // #nosec G203 -- tag-starting "<" escaped, deny-list above; themes render it only inside <style>
}
