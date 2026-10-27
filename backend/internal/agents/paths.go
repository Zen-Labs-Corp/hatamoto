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
