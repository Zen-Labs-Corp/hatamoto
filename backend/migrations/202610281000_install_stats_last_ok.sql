-- +goose Up
-- last_ok_at is the time of the last successful report; last_sent_at stays the claim stamp.
ALTER TABLE hatamoto_install_stats ADD COLUMN IF NOT EXISTS last_ok_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE hatamoto_install_stats DROP COLUMN IF EXISTS last_ok_at;
