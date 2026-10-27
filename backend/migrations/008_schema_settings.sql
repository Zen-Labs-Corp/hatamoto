-- +goose Up
-- Per-schema routing settings (url_pattern, archive_path, sitemap_group,
-- aliases, overrides, sitemap, section_pages). Empty object = built-in defaults.
ALTER TABLE hatamoto_entity_schemas
    ADD COLUMN IF NOT EXISTS settings jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE hatamoto_entity_schemas DROP COLUMN IF EXISTS settings;
