// Package shortcode finds and expands [name key="value"] calls in text. It is
// pure: no sanitising (callers expand after sanitising, because a handler's
// markup would not survive the policy) and no knowledge of any one shortcode.
package shortcode

import (
	"regexp"
	"strings"
)

// Call is one shortcode found in a string; Start and End are byte offsets of
// the whole [..] text.
type Call struct {
	Name       string
	Attrs      map[string]string
	Start, End int
}

// Handler renders one call. block reports markup that is not valid inside a
// paragraph (a lone <p>[..]</p> is replaced by it). ok=false removes the call.
type Handler func(attrs map[string]string) (html string, block bool, ok bool)

// Registry maps a shortcode name to its handler.
type Registry map[string]Handler

// A quote is a plain quote or the entity forms the rich-text sanitiser emits.
const quote = `(?:"|'|&#34;|&quot;|&#39;)`

var (
	callRE = regexp.MustCompile(`\[([a-z][a-z0-9_]*)((?:\s+[a-z][a-z0-9_-]*\s*=\s*` + quote + `[^\]]*?` + quote + `)*)\s*\]`)
	attrRE = regexp.MustCompile(`([a-z][a-z0-9_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|&#34;(.*?)&#34;|&quot;(.*?)&quot;|&#39;(.*?)&#39;)`)
	slugRE = regexp.MustCompile(`^[a-z0-9_-]+$`)
	// loneRE is a paragraph holding nothing but one call.
	loneRE = regexp.MustCompile(`(?s)<p[^>]*>\s*$`)
)

// Find returns every call in s, in order. Names are case-sensitive; a slug
// attribute outside [a-z0-9_-]+ makes the call a non-match.
func Find(s string) []Call {
	if !strings.Contains(s, "[") {
		return nil
	}
	var out []Call
	for _, m := range callRE.FindAllStringSubmatchIndex(s, -1) {
		attrs := map[string]string{}
		for _, a := range attrRE.FindAllStringSubmatch(s[m[4]:m[5]], -1) {
			attrs[a[1]] = a[2] + a[3] + a[4] + a[5] + a[6]
		}
		if slug, has := attrs["slug"]; has && !slugRE.MatchString(slug) {
			continue
		}
		out = append(out, Call{Name: s[m[2]:m[3]], Attrs: attrs, Start: m[0], End: m[1]})
	}
	return out
}

// Slugs returns the slug attribute of every call named name, in order, once each.
func Slugs(s, name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range Find(s) {
		if c.Name != name || c.Attrs["slug"] == "" || seen[c.Attrs["slug"]] {
			continue
		}
		seen[c.Attrs["slug"]] = true
		out = append(out, c.Attrs["slug"])
	}
	return out
}

// Expand replaces every call whose name is in reg. Unknown names stay as
// written. A block result replaces a paragraph that holds only the call.
func Expand(s string, reg Registry) string {
	calls := Find(s)
	if len(calls) == 0 || len(reg) == 0 {
		return s
	}
	var b strings.Builder
	pos := 0
	for _, c := range calls {
		h := reg[c.Name]
		if h == nil || c.Start < pos {
			continue
		}
		html, block, ok := h(c.Attrs)
		start, end := c.Start, c.End
		if block || !ok {
			if ps, pe, lone := lonePara(s, pos, start, end); lone {
				start, end = ps, pe
			}
		}
		b.WriteString(s[pos:start])
		if ok {
			b.WriteString(html)
		}
		pos = end
	}
	b.WriteString(s[pos:])
	return b.String()
}

// lonePara widens [start,end) to the <p>..</p> around it when the call is the
// paragraph's only content; from bounds the search to text not yet consumed.
func lonePara(s string, from, start, end int) (int, int, bool) {
	loc := loneRE.FindStringIndex(s[from:start])
	if loc == nil {
		return 0, 0, false
	}
	rest := s[end:]
	trim := strings.TrimLeft(rest, " \t\r\n")
	if !strings.HasPrefix(trim, "</p>") {
		return 0, 0, false
	}
	return from + loc[0], end + (len(rest) - len(trim)) + len("</p>"), true
}
