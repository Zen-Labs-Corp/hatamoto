package render

import (
	"html/template"
	"sync/atomic"

	"hatamoto.dev/core/internal/jsonx"
)

var adminPrefixFn atomic.Pointer[func() string]

// SetAdminPrefix installs the hook that reports the configured admin address
// (a path such as "/admin"), so the public prefetch rules exclude it. nil
// restores the default.
func SetAdminPrefix(f func() string) {
	if f == nil {
		adminPrefixFn.Store(nil)
		return
	}
	adminPrefixFn.Store(&f)
}

func adminPrefix() string {
	if f := adminPrefixFn.Load(); f != nil {
		if p := (*f)(); p != "" && p != "/" {
			return p
		}
	}
	return "/admin"
}

// speculationExcluded lists the URLPattern pathnames public prefetch skips
// besides the admin address: routes that are not pages, anything that looks
// like an action, and URLs with a query string. "*" also matches across slashes.
var speculationExcluded = []string{
	"/p/*", "/api/*", "/plugin-ui/*", "/forms/*", "/auth/*", "/preview/*",
	"/media/*", "/theme/*", "/fonts/*", "/.well-known/*", "/-/*",
	"/health*", "/ready*", "/sitemap*",
	"/*logout*", "/*\\?*",
	"/*.xml", "/*.pdf", "/*.zip", "/*.txt", "/*.ico", "/feed*",
}

// speculationRules is the inline speculationrules script for public pages:
// same-site page links are prefetched on hover (moderate eagerness). Prefetch
// only, never prerender: public pages run scripts and analytics.
func speculationRules(prefix string) template.HTML {
	excluded := append([]string{prefix + "/*"}, speculationExcluded...)
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
}
