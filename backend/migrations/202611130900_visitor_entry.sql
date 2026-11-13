-- Visitor entry: the site-wide "send visitors of / to their locale" setting
-- (enabled flag, extra entry paths, optional legacy cookie name) as one JSONB
-- document, like the `locales` column before it. Per-locale countries live
-- inside the existing `locales` JSON and need no column.
--
-- Expand only: one column with a default, so existing rows read '{}' (off) and
-- the previous release, which never reads or writes it, is unaffected.

-- +goose Up
ALTER TABLE hatamoto_site_config
    ADD COLUMN IF NOT EXISTS visitor_entry jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS visitor_entry;
