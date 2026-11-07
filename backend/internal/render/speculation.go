package render

import (
	"html/template"
	"sync"

	"hatamoto.dev/core/internal/jsonx"
)

// defaultAdminPrefix is the legacy admin address. It is a public constant, so
// the rules may name it. A custom address is a secret of the site (the sealed
// admin answers a wrong guess like any unknown path) and is never printed
// here; public pages do not link to it.
const defaultAdminPrefix = "/admin"

// speculationExcluded lists the URLPattern pathnames public prefetch skips
// besides the admin address: routes that are not pages, anything that looks
// like an action, and URLs with a query string. "*" also matches across slashes.
var speculationExcluded = []string{
	defaultAdminPrefix + "/*", "/p/*", "/f/*", "/api/*", "/plugin-ui/*", "/forms/*", "/auth/*", "/preview/*",
	"/media/*", "/theme/*", "/fonts/*", "/.well-known/*", "/-/*",
	"/health", "/ready", "/sitemap", "/sitemap/*", "/sitemap.xsl",
	"/*logout*",
	"/*.xml", "/*.pdf", "/*.zip", "/*.txt", "/*.ico", "/feed", "/feed/*",
}

// speculationQuery excludes URLs with a query string. It is the object form
// because the pathname-only string patterns never see the search part; the
// pathname is spelled out since an object inherits it from the document.
var speculationQuery = map[string]any{"pathname": "/*", "search": "(.+)"}

// speculationRules is the inline speculationrules script for public pages:
// same-site page links are prefetched on hover (moderate eagerness). Prefetch
// only, never prerender: public pages run scripts and analytics.
func speculationRules() template.HTML { return speculationBlock() }

// speculationBlock is the rules element, built once: it takes no site input.
var speculationBlock = sync.OnceValue(func() template.HTML {
	excluded := make([]any, 0, len(speculationExcluded)+1)
	for _, p := range speculationExcluded {
		excluded = append(excluded, p)
	}
	excluded = append(excluded, speculationQuery)
	b, err := jsonx.Marshal(map[string]any{"prefetch": []any{map[string]any{
		"where": map[string]any{"and": []any{
			map[string]any{"href_matches": "/*"},
			map[string]any{"not": map[string]any{"href_matches": excluded}},
		}},
		"eagerness": "moderate",
	}}}, jsonx.HTMLSafe) // lands in template.HTML inside a script element
	if err != nil {
		return ""
	}
	return template.HTML(`<script type="speculationrules">` + string(b) + `</script>`) // #nosec G203 -- JSON encoded HTML-safe
})
