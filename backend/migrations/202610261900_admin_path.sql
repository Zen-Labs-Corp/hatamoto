-- +goose Up
-- The per-install admin address. NULL means pending: the first boot of a fresh
-- install with no owner yet stores a random value. An install that already
-- exists keeps the address it has always had, '/admin', with no acknowledgement
-- recorded (admin_path_ack_* are set when an owner knowingly picks a weak one).
ALTER TABLE hatamoto_site_config
    ADD COLUMN admin_path text,
    ADD COLUMN admin_path_ack_by text,
    ADD COLUMN admin_path_ack_at timestamptz;

-- hatamoto:backfill idempotent
UPDATE hatamoto_site_config SET admin_path = '/admin' WHERE admin_path IS NULL;

-- +goose Down
ALTER TABLE hatamoto_site_config
    DROP COLUMN IF EXISTS admin_path_ack_at,
    DROP COLUMN IF EXISTS admin_path_ack_by,
    DROP COLUMN IF EXISTS admin_path;
