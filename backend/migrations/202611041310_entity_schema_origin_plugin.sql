-- +goose Up
-- The plugin that installed an entity type (a plugin package's schemas/ files).
-- NULL for built-in and owner-created types. Expand-only: the previous binary
-- ignores the column.
ALTER TABLE hatamoto_entity_schemas
  ADD COLUMN IF NOT EXISTS origin_plugin text;

-- +goose Down
ALTER TABLE hatamoto_entity_schemas
  DROP COLUMN IF EXISTS origin_plugin;
