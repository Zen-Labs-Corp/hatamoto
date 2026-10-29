-- +goose Up
-- Per-term SEO values (same keys as entry attributes). Expand-only: additive column with a default.
ALTER TABLE hatamoto_terms ADD COLUMN IF NOT EXISTS seo jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE hatamoto_terms DROP COLUMN IF EXISTS seo;
