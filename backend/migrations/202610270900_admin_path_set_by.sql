-- +goose Up
-- Who last chose the admin address and when. Both stay NULL for an address the
-- server generated itself at first start and for rows that predate this column;
-- no backfill, the admin UI words those cases differently.
ALTER TABLE hatamoto_site_config
    ADD COLUMN IF NOT EXISTS admin_path_set_by text,
    ADD COLUMN IF NOT EXISTS admin_path_set_at timestamptz;

-- +goose Down
ALTER TABLE hatamoto_site_config
    DROP COLUMN IF EXISTS admin_path_set_at,
    DROP COLUMN IF EXISTS admin_path_set_by;
