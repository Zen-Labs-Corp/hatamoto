-- +goose Up
-- One-time marker: the startup step that gives an upgraded site a real Header
-- menu has run. Nullable and additive, N-1 binaries ignore the column.
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS menus_seeded_at timestamptz;

-- +goose Down
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS menus_seeded_at;
