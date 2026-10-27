-- +goose Up
-- v1-22-wordpress-importer: runs and the source-id to Hatamoto-id map that make
-- re-runs idempotent (a re-sync updates what it imported, never duplicates).

CREATE TABLE hatamoto_import_runs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Source family and the site it read, for example 'wordpress' and 'https://example.com'.
    source      text NOT NULL,
    source_ref  text NOT NULL,
    status      text NOT NULL DEFAULT 'running'
                CHECK (status IN ('running', 'completed', 'failed', 'scanned')),
    phase       text NOT NULL DEFAULT '',
    -- Options of the run (no secrets); --resume reads them back.
    options     jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Counters, scan report and audit result.
    report      jsonb NOT NULL DEFAULT '{}'::jsonb,
    error       text NOT NULL DEFAULT '',
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);

CREATE INDEX idx_import_runs_started ON hatamoto_import_runs (started_at DESC);

-- One row per imported thing. kind is post, page, cpt, term, author, media, menu or
-- redirect; source_id is the id in the source ("123") or a stable key (a media
-- content hash is kept in hash).
CREATE TABLE hatamoto_import_map (
    source     text NOT NULL,
    kind       text NOT NULL,
    source_id  text NOT NULL,
    target_id  uuid NOT NULL,
    hash       text NOT NULL DEFAULT '',
    run_id     uuid REFERENCES hatamoto_import_runs (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source, kind, source_id)
);

CREATE INDEX idx_import_map_target ON hatamoto_import_map (target_id);
CREATE INDEX idx_import_map_hash ON hatamoto_import_map (source, kind, hash) WHERE hash <> '';

-- +goose Down
DROP TABLE hatamoto_import_map;
DROP TABLE hatamoto_import_runs;
