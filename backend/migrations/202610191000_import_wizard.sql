-- +goose Up
-- v1-22b-import-wizard: the admin wizard runs imports in the background and can
-- resume them after a restart.

-- A scan is a run of its own; a stopped run is 'canceled', not 'failed'.
ALTER TABLE hatamoto_import_runs DROP CONSTRAINT IF EXISTS hatamoto_import_runs_status_check;
ALTER TABLE hatamoto_import_runs ADD CONSTRAINT hatamoto_import_runs_status_check
    CHECK (status IN ('scanning', 'scanned', 'running', 'completed', 'failed', 'canceled'));

-- The source credential of a run that may still be resumed, sealed with the
-- server's key (AES-256-GCM, bound to the run id). Deleted when the run
-- completes; never part of the run's options or report.
CREATE TABLE hatamoto_import_secrets (
    run_id     uuid PRIMARY KEY REFERENCES hatamoto_import_runs (id) ON DELETE CASCADE,
    sealed     bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Who did what in the admin, for actions that change the whole site. Detail
-- never holds a secret.
CREATE TABLE hatamoto_admin_audit (
    id       bigserial PRIMARY KEY,
    at       timestamptz NOT NULL DEFAULT now(),
    actor_id uuid,
    actor    text NOT NULL DEFAULT '',
    action   text NOT NULL,
    target   text NOT NULL DEFAULT '',
    detail   jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_hatamoto_admin_audit_at ON hatamoto_admin_audit (at DESC);

-- +goose Down
DROP TABLE hatamoto_admin_audit;
DROP TABLE hatamoto_import_secrets;
ALTER TABLE hatamoto_import_runs DROP CONSTRAINT IF EXISTS hatamoto_import_runs_status_check;
UPDATE hatamoto_import_runs SET status = 'failed' WHERE status IN ('scanning', 'canceled');
ALTER TABLE hatamoto_import_runs ADD CONSTRAINT hatamoto_import_runs_status_check
    CHECK (status IN ('running', 'completed', 'failed', 'scanned'));
