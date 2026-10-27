// Package cache is the in-process cache for rendered HTML, sitemaps and
// site data. Entries live in memory with a TTL and are lost on restart.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// legacyKeyNamespace is a fixed segment embedded in every key. It has no
// meaning any more; it only keeps the key layout (and its patterns) stable.
const legacyKeyNamespace = "2e0b8f2a-0903-4817-a21f-ed8a69a1e6f3"

// ErrCacheMiss is returned by Get when the key is absent or expired.
var ErrCacheMiss = errors.New("cache miss")

// TTL constants for different cache types.
const (
	// TTLPublicPage is the TTL for public page data (5 minutes).
	TTLPublicPage = 5 * time.Minute

	// TTLMenuData is the TTL for menu data (1 hour).
	TTLMenuData = 1 * time.Hour

	// TTLGeoSchema is the TTL for GEO schema data (1 hour).
	TTLGeoSchema = 1 * time.Hour

	// TTLSiteData is the TTL for site metadata (1 hour).
	TTLSiteData = 1 * time.Hour

	// TTLSitemap is the TTL for sitemap XML data (1 hour).
	TTLSitemap = 1 * time.Hour

	// TTLDefault is the default TTL for cached items.
	TTLDefault = 5 * time.Minute
)

const (
	// defaultMaxEntries bounds memory: one entry per rendered page plus the
	// sitemaps stays far below this on any realistic site.
	defaultMaxEntries = 10000
	// sweepEvery is how many writes pass between sweeps of expired entries,
	// so a write is O(1) amortised instead of walking the whole map.
	sweepEvery = 256
)

type entry struct {
	value   []byte
	expires time.Time
	seq     uint64 // write order, oldest first for cap eviction
}

// Cache is a concurrency-safe in-memory TTL cache.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	logger  *slog.Logger

	maxEntries int
	sets       int    // writes since the last sweep
	gen        uint64 // bumped by every delete; see SetIfGeneration

	// Accounting for "render:" entries only (see RenderStats).
	seq            uint64 // last write sequence number
	renderBytes    int64
	renderCount    int
	renderMaxBytes int64  // 0 = no limit
	evictions      uint64 // cap evictions, monotonic
}

// New creates an empty cache.
func New(logger *slog.Logger) *Cache {
	return &Cache{entries: make(map[string]entry), logger: logger, maxEntries: defaultMaxEntries}
}

// Get retrieves a value; expired entries count as misses.
func (c *Cache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return nil, ErrCacheMiss
	}
	return e.value, nil
}

// GetJSON retrieves and unmarshals a JSON value from the cache.
func (c *Cache) GetJSON(ctx context.Context, key string, dest any) error {
	data, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// Set stores a value with the given TTL. Expired entries are swept every
// sweepEvery writes, and when the cache is full arbitrary entries are evicted
// to make room (it is a cache; an evicted entry is just a miss).
func (c *Cache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLocked(key, value, ttl)
	return nil
}

// Generation returns a counter that every Delete and DeletePattern advances.
// Read it before computing a value from the database and hand it to
// SetIfGeneration.
func (c *Cache) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gen
}

// SetIfGeneration is Set, skipped (false) when a delete ran since gen was read.
// A render that started before a purge may hold data from before the change;
// storing it would keep the stale page for the whole TTL.
func (c *Cache) SetIfGeneration(_ context.Context, key string, value []byte, ttl time.Duration, gen uint64) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen != gen {
		return false, nil
	}
	return c.setLocked(key, value, ttl), nil
}

// removeLocked is the single delete path so the render accounting cannot
// drift from the map.
func (c *Cache) removeLocked(key string) {
	e, ok := c.entries[key]
	if !ok {
		return
	}
	if isRenderKey(key) {
		c.renderBytes -= int64(len(e.value))
		c.renderCount--
	}
	delete(c.entries, key)
}

// evictOldestRenderLocked removes the oldest "render:" entries (lowest seq)
// until renderBytes+incoming fits in target or none are left. One sort per
// batch keeps a burst of writes from each paying for a scan.
func (c *Cache) evictOldestRenderLocked(incoming, target int64) {
	type kv struct {
		key string
		seq uint64
	}
	all := make([]kv, 0, c.renderCount)
	for k, e := range c.entries {
		if isRenderKey(k) {
			all = append(all, kv{k, e.seq})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seq < all[j].seq })
	for _, o := range all {
		if c.renderBytes+incoming <= target {
			break
		}
		c.removeLocked(o.key)
	}
	c.evictions++
}

// setLocked stores the value and reports whether it was stored (a render
// value larger than the cap is refused).
func (c *Cache) setLocked(key string, value []byte, ttl time.Duration) bool {
	now := time.Now()
	c.sets++
	if c.sets >= sweepEvery {
		c.sets = 0
		for k, e := range c.entries {
			if now.After(e.expires) {
				c.removeLocked(k)
			}
		}
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		// Drop expired entries first, then arbitrary ones down to 90% so the
		// next writes do not each pay for a full scan.
		for k, e := range c.entries {
			if now.After(e.expires) {
				c.removeLocked(k)
			}
		}
		for k := range c.entries {
			if len(c.entries) < c.maxEntries*9/10 {
				break
			}
			c.removeLocked(k)
		}
	}
	render := isRenderKey(key)
	if render {
		// Overwrite: take the old value out of the accounting first.
		c.removeLocked(key)
		if max := c.renderMaxBytes; max > 0 {
			size := int64(len(value))
			if size > max {
				c.evictions++
				return false
			}
			if c.renderBytes+size > max {
				c.evictOldestRenderLocked(size, max*9/10)
			}
		}
		c.renderBytes += int64(len(value))
		c.renderCount++
	}
	c.seq++
	c.entries[key] = entry{value: value, expires: now.Add(ttl), seq: c.seq}
	return true
}

// RenderStats returns the number of cached "render:" entries and the sum of
// their value sizes. Rendered pages and feeds share the prefix (feeds.go uses
// RenderedPageKey too), so "pages" includes feeds; sitemaps and JSON keys are
// not counted.
func (c *Cache) RenderStats() (pages int, bytes int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.renderCount, c.renderBytes
}

// SetRenderMaxBytes caps the bytes held by "render:" entries; 0 means no
// limit. A lower cap takes effect on the next render write.
func (c *Cache) SetRenderMaxBytes(n int64) {
	if n < 0 {
		n = 0
	}
	c.mu.Lock()
	c.renderMaxBytes = n
	c.mu.Unlock()
}

// RenderMaxBytes returns the current cap (0 = no limit).
func (c *Cache) RenderMaxBytes() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.renderMaxBytes
}

// Evictions counts cap evictions (batches, and values refused for being
// larger than the cap). It never decreases.
func (c *Cache) Evictions() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.evictions
}

// SetJSON marshals and stores a JSON value in the cache.
func (c *Cache) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal cache value: %w", err)
	}
	return c.Set(ctx, key, data, ttl)
}

// Delete removes values from the cache.
func (c *Cache) Delete(_ context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for _, k := range keys {
		c.removeLocked(k)
	}
	return nil
}

// DeletePattern removes every key matching a glob pattern where "*" matches
// any run of characters.
func (c *Cache) DeletePattern(_ context.Context, pattern string) error {
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	re, err := regexp.Compile("^" + strings.Join(parts, ".*") + "$")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for k := range c.entries {
		if re.MatchString(k) {
			c.removeLocked(k)
		}
	}
	return nil
}

// Key generation helpers for different cache types.

// PublicPageKey generates a cache key for a public page.
func PublicPageKey(slug string) string {
	return fmt.Sprintf("public:page:%s:%s", legacyKeyNamespace, slug)
}

// PublicArchiveKey generates a cache key for a public archive.
func PublicArchiveKey(schema string, page, perPage int) string {
	return fmt.Sprintf("public:archive:%s:%s:%d:%d", legacyKeyNamespace, schema, page, perPage)
}

// PublicHomepageKey generates a cache key for the homepage.
func PublicHomepageKey() string {
	return fmt.Sprintf("public:homepage:%s", legacyKeyNamespace)
}

// MenuKey generates a cache key for a menu.
func MenuKey(location string) string {
	return fmt.Sprintf("menu:%s:%s", legacyKeyNamespace, location)
}

// MenuByIDKey generates a cache key for a menu by ID.
func MenuByIDKey(menuID uuid.UUID) string {
	return fmt.Sprintf("menu:%s:id:%s", legacyKeyNamespace, menuID.String())
}

// SiteDataKey generates a cache key for site metadata.
func SiteDataKey() string {
	return fmt.Sprintf("site:%s", legacyKeyNamespace)
}

// GeoSchemaKey generates a cache key for a GEO schema type.
func GeoSchemaKey(schemaType string) string {
	return fmt.Sprintf("geo:schema:%s", schemaType)
}

// GeoImplementationKey generates a cache key for a GEO implementation.
func GeoImplementationKey(implID uuid.UUID) string {
	return fmt.Sprintf("geo:impl:%s:%s", legacyKeyNamespace, implID.String())
}

// EntityJSONLDKey generates a cache key for entity JSON-LD.
func EntityJSONLDKey(entityID uuid.UUID) string {
	return fmt.Sprintf("jsonld:%s:%s", legacyKeyNamespace, entityID.String())
}

// SitemapKey generates a cache key for a sitemap sub-page.
// The sub parameter identifies the sub-sitemap (e.g. "index", "blog").
func SitemapKey(sub string) string {
	return fmt.Sprintf("sitemap:%s:%s", legacyKeyNamespace, sub)
}

// SitemapPattern returns a pattern to match all sitemap keys.
func SitemapPattern() string {
	return fmt.Sprintf("sitemap:%s:*", legacyKeyNamespace)
}

// Invalidation patterns for cache clearing.

// TenantPattern returns a pattern to match every cache key. Retained as
// a name for historical clarity; post-Path-B it has no tenant dimension.
func TenantPattern() string {
	return fmt.Sprintf("*:%s:*", legacyKeyNamespace)
}

// PublicPattern returns a pattern to match all public page keys.
func PublicPattern() string {
	return fmt.Sprintf("public:*:%s:*", legacyKeyNamespace)
}

// MenuPattern returns a pattern to match all menu keys.
func MenuPattern() string {
	return fmt.Sprintf("menu:%s:*", legacyKeyNamespace)
}

// GeoPattern returns a pattern to match all GEO keys.
func GeoPattern() string {
	return fmt.Sprintf("geo:*:%s:*", legacyKeyNamespace)
}

// EntityPattern returns a pattern to match all keys for a specific entity.
func EntityPattern(entityID uuid.UUID) string {
	return fmt.Sprintf("*:%s:*%s*", legacyKeyNamespace, entityID.String())
}

// CacheInvalidator provides methods for invalidating cache entries.
type CacheInvalidator struct {
	cache  *Cache
	logger *slog.Logger
}

// NewCacheInvalidator creates a new cache invalidator.
func NewCacheInvalidator(cache *Cache, logger *slog.Logger) *CacheInvalidator {
	return &CacheInvalidator{
		cache:  cache,
		logger: logger,
	}
}

// InvalidateEntity invalidates all cache entries related to an entity.
func (ci *CacheInvalidator) InvalidateEntity(ctx context.Context, entityID uuid.UUID, slug string) error {
	keys := []string{
		PublicPageKey(slug),
		PublicHomepageKey(),
		EntityJSONLDKey(entityID),
	}

	if err := ci.cache.Delete(ctx, keys...); err != nil {
		ci.logger.Warn("failed to invalidate entity cache",
			slog.String("entity_id", entityID.String()),
			slog.String("error", err.Error()))
		return err
	}

	// Also invalidate archives since entity content may have changed
	if err := ci.cache.DeletePattern(ctx, fmt.Sprintf("public:archive:%s:*", legacyKeyNamespace)); err != nil {
		ci.logger.Warn("failed to invalidate archive cache",
			slog.String("error", err.Error()))
	}

	// Invalidate all sitemap cache entries since entity publish state affects sitemaps
	if err := ci.cache.DeletePattern(ctx, SitemapPattern()); err != nil {
		ci.logger.Warn("failed to invalidate sitemap cache",
			slog.String("error", err.Error()))
	}

	// Rendered pages embed navigation and sibling data: drop them all.
	if err := ci.InvalidateRendered(ctx); err != nil {
		ci.logger.Warn("failed to invalidate rendered pages",
			slog.String("error", err.Error()))
	}

	ci.logger.Debug("invalidated entity cache",
		slog.String("entity_id", entityID.String()),
		slog.String("slug", slug))

	return nil
}

// InvalidateMenu invalidates all menu-related cache entries.
func (ci *CacheInvalidator) InvalidateMenu(ctx context.Context) error {
	// Also invalidate site data since navigation is part of it
	keys := []string{
		SiteDataKey(),
	}
	if err := ci.cache.Delete(ctx, keys...); err != nil {
		ci.logger.Warn("failed to invalidate site data cache",
			slog.String("error", err.Error()))
	}

	if err := ci.cache.DeletePattern(ctx, MenuPattern()); err != nil {
		ci.logger.Warn("failed to invalidate menu cache",
			slog.String("error", err.Error()))
		return err
	}

	if err := ci.InvalidateRendered(ctx); err != nil {
		ci.logger.Warn("failed to invalidate rendered pages",
			slog.String("error", err.Error()))
	}

	ci.logger.Debug("invalidated menu cache")
	return nil
}

// InvalidateGeoSchema invalidates all GEO schema cache entries.
func (ci *CacheInvalidator) InvalidateGeoSchema(ctx context.Context) error {
	if err := ci.cache.DeletePattern(ctx, GeoPattern()); err != nil {
		ci.logger.Warn("failed to invalidate GEO cache",
			slog.String("error", err.Error()))
		return err
	}

	ci.logger.Debug("invalidated GEO cache")
	return nil
}

// InvalidateTenant invalidates every cache entry. The name is retained
// from the multi-tenant era for clarity at call sites — post-Path-B it
// is functionally equivalent to "clear the whole cache".
func (ci *CacheInvalidator) InvalidateTenant(ctx context.Context) error {
	patterns := []string{
		PublicPattern(),
		MenuPattern(),
		GeoPattern(),
		SitemapPattern(),
		fmt.Sprintf("site:%s", legacyKeyNamespace),
		fmt.Sprintf("jsonld:%s:*", legacyKeyNamespace),
	}

	for _, pattern := range patterns {
		if err := ci.cache.DeletePattern(ctx, pattern); err != nil {
			ci.logger.Warn("failed to invalidate cache pattern",
				slog.String("pattern", pattern),
				slog.String("error", err.Error()))
		}
	}

	if err := ci.InvalidateRendered(ctx); err != nil {
		ci.logger.Warn("failed to invalidate rendered pages",
			slog.String("error", err.Error()))
	}

	ci.logger.Debug("invalidated entire cache")
	return nil
}

// GetOrSet retrieves a value from cache, or computes and stores it if not found.
// This is a convenience method for the cache-aside pattern.
func GetOrSet[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, compute func() (T, error)) (T, error) {
	var result T

	// Try to get from cache
	if err := c.GetJSON(ctx, key, &result); err == nil {
		return result, nil
	}

	// Compute the value
	result, err := compute()
	if err != nil {
		return result, err
	}

	// Store in cache (best effort)
	if err := c.SetJSON(ctx, key, result, ttl); err != nil {
		c.logger.Debug("failed to cache result", slog.String("key", key), slog.String("error", err.Error()))
	}

	return result, nil
}
