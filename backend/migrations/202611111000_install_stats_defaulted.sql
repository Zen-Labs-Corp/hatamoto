-- +goose Up
-- default_notice_pending marks a row the default wrote (consent yes, level
-- detailed) for an install that was never asked, until the owner acknowledges
-- or changes it. Expand only: no CHECK and no backfill. consent stays yes, so
-- an older binary reads the row as a normal opt-in.
ALTER TABLE hatamoto_install_stats
    ADD COLUMN IF NOT EXISTS default_notice_pending BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE hatamoto_install_stats
    DROP COLUMN IF EXISTS default_notice_pending;
