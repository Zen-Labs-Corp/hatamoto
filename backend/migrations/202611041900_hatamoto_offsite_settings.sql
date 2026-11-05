-- +goose Up
-- Off-site backup target and schedule saved from the admin: one row (id is
-- always true). The secret key is stored sealed (AES-256-GCM), never in clear.
-- next_due_at is the next scheduled run, claimed atomically so only one replica
-- runs a slot; the last_* columns hold the result of the latest upload. No row
-- means off-site backups are not configured. Additive only; validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_offsite_settings (
    id            boolean     PRIMARY KEY DEFAULT true CHECK (id),
    enabled       boolean     NOT NULL DEFAULT false,
    service       text        NOT NULL DEFAULT 'aws',
    endpoint      text        NOT NULL DEFAULT '',
    region        text        NOT NULL DEFAULT '',
    bucket        text        NOT NULL DEFAULT '',
    prefix        text        NOT NULL DEFAULT '',
    access_key    text        NOT NULL DEFAULT '',
    secret_sealed bytea,
    schedule      text        NOT NULL DEFAULT 'off',
    hour          integer     NOT NULL DEFAULT 3,
    weekday       integer     NOT NULL DEFAULT 0,
    keep          integer     NOT NULL DEFAULT 7,
    next_due_at   timestamptz,
    last_at       timestamptz,
    last_ok       boolean     NOT NULL DEFAULT false,
    last_size     bigint      NOT NULL DEFAULT 0,
    last_sha256   text        NOT NULL DEFAULT '',
    last_error    text        NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_offsite_settings;
