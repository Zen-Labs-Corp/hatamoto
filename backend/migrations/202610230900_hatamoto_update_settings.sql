-- +goose Up
-- One row of update policy and the result of the last check. NULL in a policy
-- column means "use the built-in default"; the HATAMOTO_UPDATES and
-- HATAMOTO_UPDATE_CHANNEL environment variables win over the row. Replicas that
-- share one database share the row. The status columns are written by the
-- updater on every run and read by `hatamoto update status`.
CREATE TABLE IF NOT EXISTS hatamoto_update_settings (
    id               BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    mode             TEXT,
    channel          TEXT,
    window_start     SMALLINT,
    window_end       SMALLINT,
    pin              TEXT,
    defer_days       SMALLINT NOT NULL DEFAULT 0,
    last_check_at    TIMESTAMPTZ,
    last_result      TEXT NOT NULL DEFAULT '',
    decision         TEXT NOT NULL DEFAULT '',
    reason           TEXT NOT NULL DEFAULT '',
    target           TEXT NOT NULL DEFAULT '',
    available_major  TEXT NOT NULL DEFAULT '',
    first_seen       JSONB NOT NULL DEFAULT '{}'
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_update_settings;
