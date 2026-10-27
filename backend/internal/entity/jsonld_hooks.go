// Package entity — plugin extension point for JSON-LD generation.
//
// Core ships this registry EMPTY: no default builders are registered here
// for any schema slug. Plugins that add a content type (e.g. case-study,
// team-member) register their own JSON-LD builder via RegisterJSONLDBuilder
// from their Wire().
package entity

import "sync"

// JSONLDBuilder renders JSON-LD fields for an entity of a given schema
// slug directly into jsonLD, mirroring the shape of the in-package
// buildXJSONLD helpers (e.g. buildArticleJSONLD).
type JSONLDBuilder func(jsonLD map[string]any, entity *Entity, attrs map[string]any, baseURL string)

var (
	jsonldBuildersMu sync.RWMutex
	jsonldBuilders   = map[string]JSONLDBuilder{}
)

// RegisterJSONLDBuilder installs a JSON-LD builder for the given schema
// slug. Intended to be called from a plugin's Wire().
func RegisterJSONLDBuilder(schemaSlug string, fn JSONLDBuilder) {
	jsonldBuildersMu.Lock()
	defer jsonldBuildersMu.Unlock()
	jsonldBuilders[schemaSlug] = fn
}

// LookupJSONLDBuilder returns the registered JSON-LD builder for the
// given schema slug, or ok=false when none is registered.
func LookupJSONLDBuilder(schemaSlug string) (JSONLDBuilder, bool) {
	jsonldBuildersMu.RLock()
	defer jsonldBuildersMu.RUnlock()
	fn, ok := jsonldBuilders[schemaSlug]
	return fn, ok
}

// JSONLDEnricher post-processes a generated JSON-LD document in place. It is
// the host's single seam for the jsonld.enrich filter; GenerateJSONLD has no
// context, so the host binding supplies its own (see pluginhost.BindHostHooks).
type JSONLDEnricher func(ent *Entity, baseURL string, doc map[string]any)

var (
	jsonldEnricherMu sync.RWMutex
	jsonldEnricher   JSONLDEnricher
)

// SetJSONLDEnricher installs (or, with nil, clears) the enricher. Package
// level because GenerateJSONLD is a plain function with four call sites.
func SetJSONLDEnricher(f JSONLDEnricher) {
	jsonldEnricherMu.Lock()
	defer jsonldEnricherMu.Unlock()
	jsonldEnricher = f
}

func currentJSONLDEnricher() JSONLDEnricher {
	jsonldEnricherMu.RLock()
	defer jsonldEnricherMu.RUnlock()
	return jsonldEnricher
}
