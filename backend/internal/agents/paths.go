package agents

import "strings"

// PublicPath maps a backend request path to the public path a crawler asked
// for, and reports whether it is a crawler-facing file worth recording
// (sitemaps and llms.txt). Everything else under /api/v1/public (notably
// /resolve and /page/*, which Next.js calls server-side with its own UA) is not.
func PublicPath(p string) (string, bool) {
	p = strings.TrimPrefix(p, "/api/v1/public")
	switch {
	case p == "/sitemap.xml", p == "/llms.txt",
		strings.HasPrefix(p, "/sitemap-") && strings.HasSuffix(p, ".xml"):
		return p, true
	}
	return "", false
}

// IsFilePath reports whether p is a crawler-facing file (robots.txt, llms.txt,
// sitemaps) rather than a page. Files never belong to an entity, so they skip
// the resolver.
func IsFilePath(p string) bool {
	p = strings.TrimPrefix(p, "/api/v1/public")
	switch {
	case p == "/robots.txt", p == "/llms.txt", p == "/llms-full.txt", p == "/sitemap.xml",
		strings.HasPrefix(p, "/sitemap-") && strings.HasSuffix(p, ".xml"):
		return true
	}
	return false
}
