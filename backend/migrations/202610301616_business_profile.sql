-- Business details: a site-wide profile and one profile per public locale, kept
-- as two JSONB documents on the site settings row. The profile holds only the
-- new fields; the existing phone, business_hours and postal_address columns stay
-- the single source for those values, so nothing is copied or backfilled.
--
-- This is the first per-locale settings since the locale columns were dropped
-- (see docs/adr/0003-public-locales.md); the legacy columns stay authoritative.
-- The previous release's binary never reads or writes the new columns and keeps
-- using the legacy ones, so rolling back loses nothing it showed before.
-- Down is different: it drops both columns and deletes every profile and locale
-- card entered. Roll back by running the previous binary on the migrated
-- database, not by running Down.

-- +goose Up
ALTER TABLE hatamoto_site_config
    ADD COLUMN IF NOT EXISTS business_profile jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS locale_profiles jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE hatamoto_site_config
    DROP COLUMN IF EXISTS locale_profiles,
    DROP COLUMN IF EXISTS business_profile;
