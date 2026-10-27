-- +goose Up
-- Longer page cache lifetimes (up to 30 days), preload mode and "preload after
-- content changes". Expand-only: page_cache_ttl_seconds keeps its old 24 h
-- CHECK and is still written (capped) so the previous binary stays valid on
-- this schema. Ranges and the mode values are enforced in code
-- (siteconfig.Update) instead of inline CHECKs, which the rollback-safe
-- linter forbids on added columns.
-- page_cache_ttl_secs: 0 = fall back to page_cache_ttl_seconds.
-- page_preload_mode: 'missing' (skip cached pages) or 'all' (refresh).
ALTER TABLE hatamoto_site_config
  ADD COLUMN IF NOT EXISTS page_cache_ttl_secs integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS page_preload_mode text NOT NULL DEFAULT 'missing',
  ADD COLUMN IF NOT EXISTS page_preload_after_changes boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE hatamoto_site_config
  DROP COLUMN IF EXISTS page_cache_ttl_secs,
  DROP COLUMN IF EXISTS page_preload_mode,
  DROP COLUMN IF EXISTS page_preload_after_changes;
