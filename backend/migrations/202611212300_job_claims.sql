-- Scheduled job claims: one row per job id. A server runs a claimed job only
-- when it wins this row (the lease ran out, or it holds the row already), so a
-- job runs once per interval when several servers share the database.
-- Operational state, not content. Expand only: a new table the previous
-- release never reads. Rollback floor: unchanged.

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_job_claims (
    job_id        text        PRIMARY KEY,
    holder        text        NOT NULL,
    claimed_at    timestamptz NOT NULL DEFAULT now(),
    claimed_until timestamptz NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_job_claims;
