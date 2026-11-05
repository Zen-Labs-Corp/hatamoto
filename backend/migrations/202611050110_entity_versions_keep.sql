-- +goose Up
-- How many earlier versions of each item the daily prune keeps (owner setting,
-- 10 to 500). Expand-only: the previous binary ignores the column.
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS entity_versions_keep int NOT NULL DEFAULT 50;

-- +goose Down
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS entity_versions_keep;
