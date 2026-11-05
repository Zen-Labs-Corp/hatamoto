-- +goose Up
-- Admin branding: name, logo, accent colour and a "Managed by" line for the
-- sign-in page and the admin shell. Empty means unset. Expand-only: the
-- previous binary ignores the columns.
ALTER TABLE hatamoto_site_config
  ADD COLUMN IF NOT EXISTS admin_brand_name text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS admin_logo_url text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS admin_accent text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS admin_managed_by_text text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS admin_managed_by_url text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE hatamoto_site_config
  DROP COLUMN IF EXISTS admin_brand_name,
  DROP COLUMN IF EXISTS admin_logo_url,
  DROP COLUMN IF EXISTS admin_accent,
  DROP COLUMN IF EXISTS admin_managed_by_text,
  DROP COLUMN IF EXISTS admin_managed_by_url;
