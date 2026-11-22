-- +goose Up
-- The site's review switch (Settings > Content > Review). Off keeps today's
-- behaviour. Expand-only: the previous binary ignores the column.
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS review_enabled boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS review_enabled;
