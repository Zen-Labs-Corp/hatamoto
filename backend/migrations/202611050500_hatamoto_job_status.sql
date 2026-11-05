-- +goose Up
-- Last run of each scheduled job (one row per job id, overwritten by the next
-- run). Shown in Settings > Jobs. Operational state, not content. Expand-only.
CREATE TABLE IF NOT EXISTS hatamoto_job_status (
    job_id           text        PRIMARY KEY,
    last_started_at  timestamptz NOT NULL,
    last_finished_at timestamptz NOT NULL,
    duration_ms      bigint      NOT NULL DEFAULT 0,
    ok               boolean     NOT NULL,
    error            text        NOT NULL DEFAULT '',
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
