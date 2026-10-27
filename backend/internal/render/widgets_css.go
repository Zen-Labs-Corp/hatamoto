package render

import "strings"

// minifyCSS drops comments and the whitespace that carries no meaning: around
// { } ; , > and after :, and runs of it elsewhere become one space. Quoted
// strings pass through untouched. It is for the small hand-written widget.css
// files, not a general CSS minifier (no calc() surgery, no shorthand merging).
func minifyCSS(src string) string {
	var out strings.Builder
	out.Grow(len(src))
	space := false // a pending whitespace run
	last := byte(0)
	for i := 0; i < len(src); i++ {
		ch := src[i]
		switch {
		case ch == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				i = len(src)
			} else {
				i += end + 3
			}
			space = out.Len() > 0
		case ch == '"' || ch == '\'':
			if space && !tight(last, ch) {
				out.WriteByte(' ')
			}
			space = false
			j := i + 1
			for j < len(src) && src[j] != ch {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				j = len(src) - 1
			}
			out.WriteString(src[i : j+1])
			i, last = j, ch
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			space = out.Len() > 0
		default:
			if space && !tight(last, ch) {
				out.WriteByte(' ')
			}
			space = false
			if ch == '}' && last == ';' { // the last declaration needs no ;
				s := out.String()
				out.Reset()
				out.WriteString(s[:len(s)-1])
			}
			out.WriteByte(ch)
			last = ch
		}
	}
	return out.String()
}

// tight reports whether no space is needed between prev and next.
func tight(prev, next byte) bool {
	return strings.IndexByte("{};,:>", prev) >= 0 || strings.IndexByte("{};,>", next) >= 0
}
