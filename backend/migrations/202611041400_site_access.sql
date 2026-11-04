-- +goose Up
-- Site access gate: open, coming soon, maintenance or one shared password for
-- the whole public site. Expand-only: the previous binary ignores the columns.
-- site_access_epoch is bumped on every password change so older visitor
-- cookies stop working.
ALTER TABLE hatamoto_site_config
  ADD COLUMN IF NOT EXISTS site_access_mode text NOT NULL DEFAULT 'open',
  ADD COLUMN IF NOT EXISTS site_access_retry_after int NOT NULL DEFAULT 3600,
  ADD COLUMN IF NOT EXISTS site_access_password_hash text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS site_access_epoch int NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS site_access_heading text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS site_access_message text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE hatamoto_site_config
  DROP COLUMN IF EXISTS site_access_mode,
  DROP COLUMN IF EXISTS site_access_retry_after,
  DROP COLUMN IF EXISTS site_access_password_hash,
  DROP COLUMN IF EXISTS site_access_epoch,
  DROP COLUMN IF EXISTS site_access_heading,
  DROP COLUMN IF EXISTS site_access_message;
