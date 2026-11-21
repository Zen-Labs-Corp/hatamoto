-- +goose Up
-- Static export: the hosting settings (one row, id is always true; no row means
-- the site is served and nothing runs) and one row per export run. The S3 secret
-- key is stored sealed (AES-256-GCM), never in clear. Validation lives in Go.
-- At most one run is 'running' (partial unique index), so two replicas or a CLI
-- and the server cannot export at the same time. Expand only: new tables the
-- previous release never reads.
CREATE TABLE IF NOT EXISTS hatamoto_static_hosting_settings (
    id            boolean     PRIMARY KEY DEFAULT true CHECK (id),
    mode          text        NOT NULL DEFAULT 'served',
    target        text        NOT NULL DEFAULT 'dir',
    dir_path      text        NOT NULL DEFAULT '',
    service       text        NOT NULL DEFAULT 'aws',
    endpoint      text        NOT NULL DEFAULT '',
    region        text        NOT NULL DEFAULT '',
    bucket        text        NOT NULL DEFAULT '',
    prefix        text        NOT NULL DEFAULT '',
    access_key    text        NOT NULL DEFAULT '',
    secret_sealed bytea,
    live_origin   text        NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hatamoto_static_export_runs (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    trigger      text        NOT NULL,
    target       text        NOT NULL, -- target key (dir path or endpoint/bucket/prefix), never a secret
    status       text        NOT NULL DEFAULT 'running',
    pages        integer     NOT NULL DEFAULT 0,
    assets       integer     NOT NULL DEFAULT 0,
    media        integer     NOT NULL DEFAULT 0,
    redirects    integer     NOT NULL DEFAULT 0,
    bytes        bigint      NOT NULL DEFAULT 0,
    changed      integer     NOT NULL DEFAULT 0,
    unchanged    integer     NOT NULL DEFAULT 0,
    removed      integer     NOT NULL DEFAULT 0,
    warnings     integer     NOT NULL DEFAULT 0,
    duration_ms  bigint      NOT NULL DEFAULT 0,
    errors       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    started_at   timestamptz NOT NULL DEFAULT now(),
    heartbeat_at timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);
CREATE INDEX IF NOT EXISTS hatamoto_static_export_runs_started_idx ON hatamoto_static_export_runs (started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS hatamoto_static_export_runs_one_running ON hatamoto_static_export_runs ((true)) WHERE status = 'running';

-- +goose Down
DROP TABLE IF EXISTS hatamoto_static_export_runs;
DROP TABLE IF EXISTS hatamoto_static_hosting_settings;
