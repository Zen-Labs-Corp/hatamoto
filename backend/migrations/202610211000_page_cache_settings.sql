-- +goose Up
-- Admin-controlled page cache: on/off, lifetime, size cap and preload.
-- Sentinels instead of NULL because siteconfig.Update patches with COALESCE
-- and cannot write NULL: ttl 0 = server default (env or built-in), max_mb 0 =
-- no limit, preload interval 0 = same as the lifetime. Defaults preserve the
-- current behaviour (cache on, server TTL, no cap, preload off).
ALTER TABLE hatamoto_site_config
  ADD COLUMN IF NOT EXISTS page_cache_enabled boolean NOT NULL DEFAULT true,
  ADD COLUMN IF NOT EXISTS page_cache_ttl_seconds integer NOT NULL DEFAULT 0
    CHECK (page_cache_ttl_seconds = 0 OR page_cache_ttl_seconds BETWEEN 30 AND 86400),
  ADD COLUMN IF NOT EXISTS page_cache_max_mb integer NOT NULL DEFAULT 0 CHECK (page_cache_max_mb >= 0),
  ADD COLUMN IF NOT EXISTS page_preload_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS page_preload_interval_seconds integer NOT NULL DEFAULT 0 CHECK (page_preload_interval_seconds >= 0);

-- +goose Down
ALTER TABLE hatamoto_site_config
  DROP COLUMN IF EXISTS page_cache_enabled,
  DROP COLUMN IF EXISTS page_cache_ttl_seconds,
  DROP COLUMN IF EXISTS page_cache_max_mb,
  DROP COLUMN IF EXISTS page_preload_enabled,
  DROP COLUMN IF EXISTS page_preload_interval_seconds;
