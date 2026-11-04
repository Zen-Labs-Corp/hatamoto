-- +goose Up
-- Core tag manager: one settings row (id is always true) holding a jsonb
-- document with the enabled vendor tags and their ids. Additive only;
-- validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_tracking_settings (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    settings   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_tracking_settings;
