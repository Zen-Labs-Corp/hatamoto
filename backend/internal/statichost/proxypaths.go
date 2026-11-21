package statichost

import (
	"slices"
	"strings"
)

// liveSegments are the first path segments only Hatamoto answers in static mode.
var liveSegments = []string{"-", "api", "auth", "f", "forms", "mcp", "p", "plugin-ui", "preview"}

// LiveSegment reports whether seg is a first path segment only Hatamoto
// answers in static mode.
func LiveSegment(seg string) bool { return slices.Contains(liveSegments, seg) }

// liveScriptPath is the comment loader script. It is hard-coded because this
// leaf cannot import render; a server test pins it to the render constants.
const liveScriptPath = "/theme/assets/hm-live.js"

// ProxyPath is one path a web server in front of a static site passes to Hatamoto.
type ProxyPath struct {
	Path   string // "/forms/" (Prefix) or "/search" (exact)
	Prefix bool   // everything under Path
	Locale bool   // "/<one segment>" + Path, e.g. "/fr/search": a regex in web servers
}

// ProxyPaths lists every path a same-host web server must pass to Hatamoto:
// the live segments, search (also locale-prefixed), the admin path, the public
// API prefix and the comment loader script. adminPath and apiPrefix may be "".
// The result is deduplicated and sorted, so generated config is stable.
func ProxyPaths(adminPath, apiPrefix string) []ProxyPath {
	var out []ProxyPath
	for _, seg := range liveSegments {
		out = append(out, ProxyPath{Path: "/" + seg + "/", Prefix: true})
	}
	out = append(out,
		ProxyPath{Path: "/mcp"},
		ProxyPath{Path: "/search"},
		ProxyPath{Path: "/search", Locale: true},
		ProxyPath{Path: liveScriptPath},
	)
	for _, p := range []string{adminPath, apiPrefix} {
		if p = strings.TrimRight(strings.TrimSpace(p), "/"); strings.HasPrefix(p, "/") && p != "/" {
			out = append(out, ProxyPath{Path: p}, ProxyPath{Path: p + "/", Prefix: true})
		}
	}
	slices.SortFunc(out, func(a, b ProxyPath) int {
		return strings.Compare(a.sortKey(), b.sortKey())
	})
	return slices.Compact(out)
}

// sortKey orders by path, then exact before prefix, then plain before locale.
func (p ProxyPath) sortKey() string {
	k := p.Path + "\x00"
	if p.Prefix {
		k += "1"
	} else {
		k += "0"
	}
	if p.Locale {
		return k + "1"
	}
	return k + "0"
}
