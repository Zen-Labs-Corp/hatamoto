-- +goose Up
-- One row per release switch the supervisor made (success, rollback, manual
-- rollback, failure). The supervisor has no database access: it appends to
-- $HATAMOTO_DATA/releases/events.jsonl and the server imports unseen lines at
-- boot, keyed by event_id so importing twice changes nothing. from_version and
-- to_version avoid the SQL keywords from/to; "bootstrap" names a binary that
-- is not a staged release.
CREATE TABLE IF NOT EXISTS hatamoto_updates (
    id           BIGSERIAL PRIMARY KEY,
    event_id     TEXT NOT NULL UNIQUE,
    from_version TEXT NOT NULL DEFAULT '',
    to_version   TEXT NOT NULL DEFAULT '',
    phase        TEXT NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_updates;
