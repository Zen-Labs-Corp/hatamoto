package adminpath

import (
	"slices"
	"strings"
)

// redirectOwned are the first segments, beyond ReservedPrefixes, that core
// serves and a plugin may therefore not take over with a request.redirect
// path. The icon names are served from the site icon directory; the server
// test checks them against the live list.
var redirectOwned = []string{
	"admin", "mcp", "uploads", "sitemap.xsl", "-",
	"favicon-16.png", "favicon-32.png", "apple-icon.png", "icon-192.png", "icon-512.png",
}

// RedirectPathRefusal returns "" when a plugin may declare p as a
// request.redirect path, otherwise a short reason. p is expected to be
// well-formed (the SDK checks the shape); "/" itself is allowed.
func RedirectPathRefusal(p string) string {
	if p == "/" {
		return ""
	}
	seg, _, nested := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	if slices.Contains(ReservedPrefixes, seg) || slices.Contains(redirectOwned, seg) {
		return "core serves /" + seg
	}
	if !nested && (strings.HasSuffix(seg, ".txt") || strings.HasSuffix(seg, ".xml")) {
		return "root-level text and XML files belong to core (verification keys, sitemaps)"
	}
	return ""
}
