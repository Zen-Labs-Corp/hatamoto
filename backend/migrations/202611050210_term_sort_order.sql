-- Manual order for terms: a stored position among siblings (same kind and
-- parent). Rows keep 0, so terms stay alphabetical until an owner reorders a
-- sibling set.
--
-- Expand only: one column with a default. The previous release's binary
-- ignores it.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
ALTER TABLE hatamoto_terms ADD COLUMN IF NOT EXISTS sort_order integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_terms_sort_order ON hatamoto_terms (kind, parent_id, sort_order);

-- +goose Down
DROP INDEX IF EXISTS idx_terms_sort_order;
ALTER TABLE hatamoto_terms DROP COLUMN IF EXISTS sort_order;
