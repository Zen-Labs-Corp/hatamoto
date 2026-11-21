package staticexport

import (
	"bytes"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/redirect"
)

// RedirectsFile is the name of the rules file read by Netlify and Cloudflare Pages.
const RedirectsFile = "_redirects"

// Line is one exact redirect taken from a live 3xx answer.
type Line struct {
	From, To string
	Status   int
}

// WriteRedirects renders the _redirects file: exact lines sorted by From,
// then pattern rules in redirect.SortPatterns order (the order the runtime
// tries them). warnings name every rule that was skipped.
func WriteRedirects(exact []Line, patterns []*redirect.Redirect) (body []byte, warnings []string) {
	var b bytes.Buffer
	lines := slices.Clone(exact)
	slices.SortStableFunc(lines, func(a, b Line) int { return strings.Compare(a.From, b.From) })
	for _, l := range lines {
		switch {
		case !redirect.ValidStatusCode(l.Status):
			warnings = append(warnings, fmt.Sprintf("redirect %s: status %d cannot be written", l.From, l.Status))
		case !plainToken(l.From) || !plainToken(l.To):
			warnings = append(warnings, fmt.Sprintf("redirect %s: contains whitespace, skipped", l.From))
		default:
			fmt.Fprintf(&b, "%s %s %d\n", l.From, l.To, l.Status)
		}
	}

	sorted := slices.Clone(patterns)
	redirect.SortPatterns(sorted)
	for _, r := range sorted {
		if !redirect.ValidStatusCode(r.StatusCode) {
			warnings = append(warnings, fmt.Sprintf("redirect rule %s: status %d cannot be written", r.SourcePath, r.StatusCode))
			continue
		}
		from, to, ok := translatePattern(r.SourcePath, r.TargetURL)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("redirect rule %s -> %s cannot be written to _redirects, skipped", r.SourcePath, r.TargetURL))
			continue
		}
		fmt.Fprintf(&b, "%s %s %d\n", from, to, r.StatusCode)
	}
	return b.Bytes(), warnings
}

func plainToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, " \t\r\n")
}

// translatePattern turns a wildcard rule into _redirects placeholders:
// "/old/*/x" + "/new/$1" -> "/old/:p1/x" "/new/:p1", and
// "/a/**" + "/b/$1" -> "/a/*" "/b/:splat". It refuses an invalid or unsafe
// rule, a $n with no matching wildcard and a placeholder glued to a word.
func translatePattern(src, dst string) (from, to string, ok bool) {
	if redirect.ValidatePatternRule(src, dst) != nil || !redirect.IsPatternSource(src) ||
		!redirect.SafeLocation(dst, true) || !plainToken(dst) {
		return "", "", false
	}
	segs := strings.Split(strings.Trim(src, "/"), "/")
	var names []string // placeholder per capture, in order
	for i, s := range segs {
		switch s {
		case "*":
			name := ":p" + strconv.Itoa(len(names)+1)
			names = append(names, name)
			segs[i] = name
		case "**":
			if i != len(segs)-1 {
				return "", "", false
			}
			names = append(names, ":splat")
			segs[i] = "*"
		}
	}
	if len(names) > 9 {
		return "", "", false
	}

	var b strings.Builder
	for i := 0; i < len(dst); i++ {
		if dst[i] != '$' || i+1 >= len(dst) || dst[i+1] < '1' || dst[i+1] > '9' {
			b.WriteByte(dst[i])
			continue
		}
		n := int(dst[i+1] - '1')
		if n >= len(names) {
			return "", "", false
		}
		if i+2 < len(dst) && isWordByte(dst[i+2]) {
			return "", "", false
		}
		b.WriteString(names[n])
		i++
	}
	return "/" + strings.Join(segs, "/"), b.String(), true
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Stub is the HTML written at a redirect source on hosts that ignore
// _redirects: meta refresh, canonical and a link. abs is the absolute target.
func Stub(abs string) []byte {
	a := html.EscapeString(abs)
	return []byte(`<!doctype html><meta charset="utf-8"><meta name="robots" content="noindex">` +
		`<link rel="canonical" href="` + a + `">` +
		`<meta http-equiv="refresh" content="0; url=` + a + `">` +
		`<a href="` + a + `">` + a + `</a>` + "\n")
}
