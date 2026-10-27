// Package entity — plugin extension point for public entity URL
// construction, used by JSON-LD generation (BuildEntityURLForJSONLD).
//
// Modeled on jsonld_hooks.go's registration pattern. Core ships this
// registry EMPTY: no default builders are registered here for any schema
// slug. Plugins that add a content type register their own
// URL builder via RegisterEntityURLBuilder from their Wire().
package entity

import "sync"

// EntityURLBuilder returns the canonical public URL for an entity given
// the request's baseURL and the entity's slug. baseURL is per-request
// (Server.publicBaseURL(r): PUBLIC_BASE_URL, else the request origin) and per-tenant, so it must be supplied to the
// builder rather than assumed — every other branch of
// BuildEntityURLForJSONLD returns an absolute baseURL+path URL, and a
// relative path here would produce an invalid relative @id/url in
// JSON-LD.
type EntityURLBuilder func(baseURL, slug string) string

var (
	entityURLBuildersMu sync.RWMutex
	entityURLBuilders   = map[string]EntityURLBuilder{}
)

// RegisterEntityURLBuilder installs a URL builder for the given schema
// slug. Intended to be called from a plugin's Wire().
func RegisterEntityURLBuilder(schemaSlug string, fn EntityURLBuilder) {
	entityURLBuildersMu.Lock()
	defer entityURLBuildersMu.Unlock()
	entityURLBuilders[schemaSlug] = fn
}

// LookupEntityURLBuilder returns the registered URL builder for the
// given schema slug, or ok=false when none is registered.
func LookupEntityURLBuilder(schemaSlug string) (EntityURLBuilder, bool) {
	entityURLBuildersMu.RLock()
	defer entityURLBuildersMu.RUnlock()
	fn, ok := entityURLBuilders[schemaSlug]
	return fn, ok
}
