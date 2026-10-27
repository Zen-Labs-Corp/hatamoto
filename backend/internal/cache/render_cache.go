package cache

import (
	"context"
	"fmt"
	"strings"
)

// renderKeyPrefix namespaces every server-rendered HTML entry so a single
// pattern delete drops them all. It is deliberately outside the
// legacyKeyNamespace layout used by the JSON API keys.
const renderKeyPrefix = "render:"

// RenderedPageKey generates the cache key for a server-rendered HTML page.
// scope is the engine's content hash, so a theme change never serves stale
// markup (feeds pass their base URL); path identifies the page.
func RenderedPageKey(scope, path string) string {
	return fmt.Sprintf("%sv1:%s:%s", renderKeyPrefix, scope, path)
}

// InvalidateRendered drops every cached rendered page. Pages embed navigation
// and sibling data, so any content, menu or site-config change invalidates the
// whole set (coarse on purpose).
func (ci *CacheInvalidator) InvalidateRendered(ctx context.Context) error {
	return ci.cache.DeletePattern(ctx, renderKeyPrefix+"*")
}

// InvalidateSitemaps drops every cached sitemap (index and children).
func (ci *CacheInvalidator) InvalidateSitemaps(ctx context.Context) error {
	return ci.cache.DeletePattern(ctx, SitemapPattern())
}

func isRenderKey(key string) bool { return strings.HasPrefix(key, renderKeyPrefix) }
