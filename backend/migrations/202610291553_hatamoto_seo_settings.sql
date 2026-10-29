-- +goose Up
-- Site-wide SEO settings: one row (id is always true) holding a jsonb document
-- of title/description templates and custom variables. A missing row means the
-- built-in defaults; later settings tabs add keys without a migration.
-- Additive only; validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_seo_settings (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    settings   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_seo_settings;
