// Package commentfmt turns a comment's plain-text body into safe HTML and
// reports its links. Pure: no database, no network.
package commentfmt

import (
	"regexp"
	"sort"
	"strings"
)

// maxScan caps how much of a body the link detector looks at.
const maxScan = 64 << 10

// Link is one detected link. Linkable is true only for http/https URLs written
// out in full (or www.), which the renderer turns into <a>.
type Link struct {
	Start, End int
	URL        string
	Domain     string
	Linkable   bool
}

// knownTLDs is the set of top-level domains a bare "label.tld" must end in to
// count as a link: common generic ones plus the country-code domains. Kept as
// one string so the list stays compact.
var knownTLDs = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range strings.Fields(`com net org info biz xyz top online site club shop app dev io co ai me tv cc ly ru cn
		edu gov mil int name pro mobi tech store blog link live life world today news cloud space website
		email page icu vip work fun asia agency click download review stream win bid loan men party trade
		ac ad ae af ag al am ao aq ar as at au aw ax az ba bb bd be bf bg bh bi bj bm bn bo br bs bt bw by bz
		ca cd cf cg ch ci ck cl cm cr cu cv cw cx cy cz de dj dk dm do dz ec ee eg er es et eu fi fj fk fm fo fr
		ga gd ge gf gg gh gi gl gm gn gp gq gr gs gt gu gw gy hk hm hn hr ht hu id ie il im in iq ir is it je jm
		jo jp ke kg kh ki km kn kp kr kw ky kz la lb lc li lk lr ls lt lu lv ly ma mc md mg mh mk ml mm mn mo mp
		mq mr ms mt mu mv mw mx my mz na nc ne nf ng ni nl no np nr nu nz om pa pe pf pg ph pk pl pm pn pr ps pt
		pw py qa re ro rs rw sa sb sc sd se sg sh si sk sl sm sn so sr ss st su sv sx sy sz tc td tf tg th tj tk
		tl tm tn to tr tt tw tz ua ug uk us uy uz va vc ve vg vi vn vu wf ws ye yt za zm zw`) {
		m[t] = true
	}
	return m
}()

const (
	urlTail = `[^\s<>"']*`
	dotSep  = `(?:\s*(?:\[\.\]|\(\.\)|\{\.\}|\[dot\]|\(dot\)|\{dot\})\s*|\s+dot\s+|\.)`
	label   = `[a-z0-9][a-z0-9-]*`
)

var (
	reMarkdown = regexp.MustCompile(`\[[^\]\n]*\]\(([^)\s]+)\)`)
	reScheme   = regexp.MustCompile(`(?i)\b(?:https?|ftp|hxxps?|h\*\*ps?)://` + urlTail)
	reWWW      = regexp.MustCompile(`(?i)\bwww\.` + label + `(?:\.` + label + `)+` + urlTail)
	reBare     = regexp.MustCompile(`(?i)\b` + label + `(?:` + dotSep + label + `)+(?:/` + urlTail + `)?`)
	reObfDot   = regexp.MustCompile(`(?i)\s*(?:\[\.\]|\(\.\)|\{\.\}|\[dot\]|\(dot\)|\{dot\})\s*|\s+dot\s+`)
	reScheme2  = regexp.MustCompile(`(?i)^[a-z*]+://`)
)

// FindLinks reports every link-like token in s: scheme URLs (http, https, ftp),
// www. prefixes, bare domains (label.tld with tld from knownTLDs), markdown
// links [t](u), and obfuscated forms: hxxp(s)://, h**p://, example[.]com,
// example(.)com, example{.}com, "example dot com", "example [dot] com".
// Links are returned in order of position and never overlap.
func FindLinks(s string) []Link {
	if len(s) > maxScan {
		s = s[:maxScan]
	}
	var out []Link
	free := func(a, b int) bool {
		for _, l := range out {
			if a < l.End && l.Start < b {
				return false
			}
		}
		return true
	}
	add := func(a, b int, url, domain string, linkable bool) {
		if domain != "" && free(a, b) {
			out = append(out, Link{Start: a, End: b, URL: url, Domain: domain, Linkable: linkable})
		}
	}

	for _, m := range reMarkdown.FindAllStringSubmatchIndex(s, -1) {
		u := s[m[2]:m[3]]
		add(m[0], m[1], u, hostOf(u), isHTTP(u))
	}
	for _, m := range reScheme.FindAllStringIndex(s, -1) {
		end := m[0] + len(trimTail(s[m[0]:m[1]]))
		u := s[m[0]:end]
		add(m[0], end, u, hostOf(u), isHTTP(u))
	}
	for _, m := range reWWW.FindAllStringIndex(s, -1) {
		end := m[0] + len(trimTail(s[m[0]:m[1]]))
		u := s[m[0]:end]
		if d := hostOf(u); knownTLDs[lastLabel(d)] {
			add(m[0], end, u, d, true)
		}
	}
	for _, m := range reBare.FindAllStringIndex(s, -1) {
		end := m[0] + len(trimTail(s[m[0]:m[1]]))
		u := reObfDot.ReplaceAllString(s[m[0]:end], ".")
		if d := hostOf(u); knownTLDs[lastLabel(d)] {
			add(m[0], end, u, d, false)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// HasLinks reports whether FindLinks finds anything.
func HasLinks(s string) bool { return len(FindLinks(s)) > 0 }

// Domains returns the lowercased, deduplicated domains (leading "www." removed), sorted.
func Domains(links []Link) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range links {
		if d := strings.TrimPrefix(strings.ToLower(l.Domain), "www."); d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// trimTail drops sentence punctuation that trails a URL.
func trimTail(u string) string { return strings.TrimRight(u, ".,;:!?)") }

func isHTTP(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func lastLabel(host string) string {
	return host[strings.LastIndexByte(host, '.')+1:]
}

// hostOf extracts the lowercased host of a URL or bare "host/path" token,
// without userinfo, port or a leading "www.".
func hostOf(u string) string {
	u = reScheme2.ReplaceAllString(u, "")
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndexByte(u, '@'); i >= 0 {
		u = u[i+1:]
	}
	if i := strings.LastIndexByte(u, ':'); i >= 0 {
		u = u[:i]
	}
	return strings.TrimPrefix(strings.ToLower(u), "www.")
}
