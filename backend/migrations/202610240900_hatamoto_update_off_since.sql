-- +goose Up
-- When the admin first saw updates switched off (any source: stored mode, CLI or
-- environment). NULL while updates are on. Written only by the server's admin
-- adapter, used for the "off for 90+ days" banner. Additive and nullable, so
-- the previous release keeps working on the new schema.
ALTER TABLE hatamoto_update_settings ADD COLUMN IF NOT EXISTS off_since TIMESTAMPTZ;

-- +goose Down
ALTER TABLE hatamoto_update_settings DROP COLUMN IF EXISTS off_since;
