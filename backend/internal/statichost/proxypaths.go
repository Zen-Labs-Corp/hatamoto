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

// fallbackSegments are first segments the export folder may hold a copy of
// (media variants, theme assets, fonts; an ACME challenge a certificate client
// writes into the folder) but Hatamoto must answer when the file
// is missing: an upload or a theme update newer than the last export.
var fallbackSegments = []string{".well-known", "fonts", "media", "theme"}

// liveScriptPath is the comment loader script. It is hard-coded because this
// leaf cannot import render; a server test pins it to the render constants.
const liveScriptPath = "/theme/assets/hm-live.js"

// ProxyPath is one path a web server in front of a static site passes to Hatamoto.
type ProxyPath struct {
	Path   string // "/forms/" (Prefix) or "/search" (exact)
	Prefix bool   // everything under Path
	Locale bool   // "/<one segment>" + Path, e.g. "/fr/search": a regex in web servers
	// Fallback (with Prefix): serve the file when the folder has it, else pass to Hatamoto.
	Fallback bool
}

// ProxyPaths lists every path a same-host web server must pass to Hatamoto:
// the live segments, the file-or-Hatamoto prefixes (media, theme, fonts), search (also locale-prefixed), the admin path, the public
// API prefix and the comment loader script. adminPath and apiPrefix may be "".
// The result is deduplicated and sorted, so generated config is stable.
func ProxyPaths(adminPath, apiPrefix string) []ProxyPath {
	var out []ProxyPath
	for _, seg := range liveSegments {
		out = append(out, ProxyPath{Path: "/" + seg + "/", Prefix: true})
	}
	for _, seg := range fallbackSegments {
		out = append(out, ProxyPath{Path: "/" + seg + "/", Prefix: true, Fallback: true})
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
