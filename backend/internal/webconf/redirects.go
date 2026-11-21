package webconf

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// maxRedirectsBody bounds how much of a _redirects file is read.
const maxRedirectsBody = 1 << 20

// ParseRedirects reads a _redirects body: "from to status" per line, "#" lines
// and blanks ignored, malformed lines dropped. exact and wildcard count the
// rules a plain web server would not honour without config: exact ones (the
// export writes only meta-refresh stubs for them) and wildcard ones (lost).
func ParseRedirects(body []byte) (rules []Redirect, exact, wildcard int) {
	if len(body) > maxRedirectsBody {
		body = body[:maxRedirectsBody]
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(nil, maxRedirectsBody)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		status, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		r := Redirect{From: f[0], To: f[1], Status: status}
		rules = append(rules, r)
		if isWildcard(r.From) {
			wildcard++
		} else {
			exact++
		}
	}
	return rules, exact, wildcard
}

// isWildcard reports a source with a ":pN" segment or a "*" splat segment.
func isWildcard(from string) bool {
	for _, seg := range strings.Split(from, "/") {
		if seg == "*" || placeholderSeg.MatchString(seg) {
			return true
		}
	}
	return false
}

var (
	placeholderSeg = regexp.MustCompile(`^:p[0-9]+$`)
	placeholderRef = regexp.MustCompile(`:p([0-9]+)|:splat`)
	// ruleUnsafe cannot appear in a redirect source or target: they would end the
	// token, start a variable or a comment, or quote out of the config.
	ruleUnsafe = "{};#\"'\\$` \t\r\n"
)

// The capture classes leave out control characters: nginx matches the decoded
// URI and writes $N into Location unescaped, so a CR or LF in a capture would
// split the response.
const (
	segCapture   = "([^/[:cntrl:]]+)"
	splatCapture = "([^[:cntrl:]]*)"
)

// part is a piece of a redirect target: a literal or a capture group.
type part struct {
	lit   string
	group int // 1-based capture index, 0 = literal
}

// rule is one redirect ready to render, or the text of one that was skipped.
type rule struct {
	skip   string // non-empty: render only a "# skipped:" comment
	exact  bool
	from   string // exact: the path; wildcard: an anchored regexp
	to     []part
	status int
}

// planRedirects compiles the rules in file order (the order the runtime tries
// them) and lists the ones it cannot translate.
func planRedirects(rs []Redirect) (rules []rule, skipped []string) {
	for _, r := range rs {
		ru, ok := compileRedirect(r)
		if !ok {
			text := cleanLine(fmt.Sprintf("%s %s %d", r.From, r.To, r.Status))
			rules = append(rules, rule{skip: text})
			skipped = append(skipped, text)
			continue
		}
		rules = append(rules, ru)
	}
	return rules, skipped
}

// cleanLine drops control characters so a rule can never end its comment line.
func cleanLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func compileRedirect(r Redirect) (rule, bool) {
	switch r.Status {
	case 301, 302, 307, 308:
	default:
		return rule{}, false
	}
	if !strings.HasPrefix(r.From, "/") || r.To == "" || strings.ContainsAny(r.From+r.To, ruleUnsafe) || hasControl(r.From+r.To) {
		return rule{}, false
	}
	if !isWildcard(r.From) {
		if strings.Contains(r.From, "*") {
			return rule{}, false
		}
		return rule{exact: true, from: r.From, to: []part{{lit: r.To}}, status: r.Status}, true
	}
	segs := strings.Split(strings.TrimPrefix(r.From, "/"), "/")
	var re strings.Builder
	re.WriteString("^")
	groups, splat := 0, 0
	for i, seg := range segs {
		re.WriteString("/")
		switch {
		case placeholderSeg.MatchString(seg):
			groups++
			if seg != ":p"+strconv.Itoa(groups) || splat != 0 {
				return rule{}, false
			}
			re.WriteString(segCapture)
		case seg == "*":
			if i != len(segs)-1 {
				return rule{}, false
			}
			groups++
			splat = groups
			re.WriteString(splatCapture)
		case strings.Contains(seg, "*") || strings.Contains(seg, ":p") && placeholderRef.MatchString(seg):
			return rule{}, false // a placeholder glued to text
		default:
			re.WriteString(regexp.QuoteMeta(seg))
		}
	}
	if groups > 9 {
		return rule{}, false
	}
	re.WriteString("$")
	to, ok := targetParts(r.To, groups, splat)
	if !ok || buildsOffSite(r.To, to, splat) {
		return rule{}, false
	}
	return rule{from: re.String(), to: to, status: r.Status}, true
}

// buildsOffSite reports a wildcard target a visitor could steer off the site: it
// starts with a placeholder, with "//", or is "/" followed by the splat (a
// request for "//host" would redirect to "//host").
func buildsOffSite(to string, parts []part, splat int) bool {
	if strings.HasPrefix(to, "//") || len(parts) == 0 || parts[0].group > 0 {
		return true
	}
	return parts[0].lit == "/" && len(parts) > 1 && splat > 0 && parts[1].group == splat
}

// targetParts splits a target at its ":pN" and ":splat" placeholders.
func targetParts(to string, groups, splat int) ([]part, bool) {
	var out []part
	last := 0
	for _, m := range placeholderRef.FindAllStringSubmatchIndex(to, -1) {
		end := m[1]
		if end < len(to) && isWordByte(to[end]) {
			return nil, false
		}
		g := splat
		if m[2] >= 0 {
			n, err := strconv.Atoi(to[m[2]:m[3]])
			if err != nil {
				return nil, false
			}
			g = n
		}
		if g < 1 || g > groups {
			return nil, false
		}
		if m[0] > last {
			out = append(out, part{lit: to[last:m[0]]})
		}
		out = append(out, part{group: g})
		last = end
	}
	if last < len(to) {
		out = append(out, part{lit: to[last:]})
	}
	return out, true
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// target renders the parts with group(n) spelling a capture in the server's syntax.
func target(parts []part, group func(int) string) string {
	var b strings.Builder
	for _, p := range parts {
		if p.group > 0 {
			b.WriteString(group(p.group))
		} else {
			b.WriteString(p.lit)
		}
	}
	return b.String()
}

// caddyRedirects renders the rules as Caddy directives. A wildcard rule needs a
// named regexp matcher, numbered in file order.
func caddyRedirects(rules []rule) []string {
	var out []string
	n := 0
	for _, r := range rules {
		switch {
		case r.skip != "":
			out = append(out, "# skipped: "+r.skip)
		case r.exact:
			out = append(out, fmt.Sprintf("redir %s %s %d", r.from, target(r.to, nil), r.status))
		default:
			n++
			to := target(r.to, func(g int) string { return fmt.Sprintf("{re.r%d.%d}", n, g) })
			out = append(out,
				fmt.Sprintf("@r%d path_regexp r%d %s", n, n, r.from),
				fmt.Sprintf("redir @r%d %s %d", n, to, r.status))
		}
	}
	return out
}

// nginxRedirects renders the rules as nginx location blocks. Lines after the
// first of a block carry their own indent.
func nginxRedirects(rules []rule) []string {
	var out []string
	for _, r := range rules {
		switch {
		case r.skip != "":
			out = append(out, "# skipped: "+r.skip)
		case r.exact:
			out = append(out, "location = "+r.from+" {", fmt.Sprintf("\treturn %d %s;", r.status, target(r.to, nil)), "}")
		default:
			to := target(r.to, func(g int) string { return "$" + strconv.Itoa(g) })
			out = append(out, "location ~ "+r.from+" {", fmt.Sprintf("\treturn %d %s;", r.status, to), "}")
		}
	}
	return out
}
