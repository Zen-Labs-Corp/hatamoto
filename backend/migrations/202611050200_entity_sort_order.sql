-- Manual order for entities: a stored position among siblings (same schema,
-- parent and locale). Rows keep 0, so every existing order is unchanged until
-- an owner reorders a sibling set; ties fall back to created_at, id.
--
-- Expand only: one column with a default. The previous release's binary
-- ignores it.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
ALTER TABLE hatamoto_entities ADD COLUMN IF NOT EXISTS sort_order integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_entities_sort_order
    ON hatamoto_entities (schema_id, parent_id, locale, sort_order) WHERE variant_key IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_entities_sort_order;
ALTER TABLE hatamoto_entities DROP COLUMN IF EXISTS sort_order;
