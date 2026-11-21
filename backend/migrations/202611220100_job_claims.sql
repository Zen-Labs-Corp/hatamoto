-- +goose Up
-- One row per job claimed across servers: no other server may start the job
-- before claimed_until. Operational state, not content. Expand-only.
CREATE TABLE IF NOT EXISTS hatamoto_job_claims (
    job_id        text        PRIMARY KEY,
    claimed_until timestamptz NOT NULL,
    claimed_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
