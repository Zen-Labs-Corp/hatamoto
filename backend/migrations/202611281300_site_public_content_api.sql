-- +goose Up
-- The public content API switch (Settings > Integrations > Public content API).
-- Off by default. Expand-only: the previous binary ignores the column.
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS public_content_api boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS public_content_api;
