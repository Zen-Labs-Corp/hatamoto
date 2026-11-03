-- Plugin runs: the state a run needs (phase, cursor, confirmed hosts, upload,
-- progress, outcome), one active run per plugin, the proposals a run makes
-- for the owner to sign off, and uploads handed to a run.
--
-- Expand only: new columns all have defaults, new tables, and one partial
-- unique index. The index is enforced in the database so that two replicas
-- cannot start the same plugin twice. Rows the previous release left in
-- 'running' (nothing drove them) are closed first so the index can be built.
-- The previous release's binary never starts a run, so the index cannot
-- reject its writes.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
ALTER TABLE hatamoto_runs
    ADD COLUMN IF NOT EXISTS name         text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS phase        text    NOT NULL DEFAULT 'fetch',
    ADD COLUMN IF NOT EXISTS cursor       text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS params       jsonb   NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS hosts        text[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS upload_id    uuid,
    ADD COLUMN IF NOT EXISTS progress     jsonb   NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS summary      jsonb,
    ADD COLUMN IF NOT EXISTS reason       text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS changeset_id bigint,
    ADD COLUMN IF NOT EXISTS steps        integer NOT NULL DEFAULT 0;

-- Running totals of a changeset's items and payload bytes, so the staging
-- quota is an O(1) read under the changeset's row lock. A changeset the
-- previous release left staged is counted once here.
ALTER TABLE hatamoto_changesets
    ADD COLUMN IF NOT EXISTS item_count    integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS payload_bytes bigint  NOT NULL DEFAULT 0;
UPDATE hatamoto_changesets c
   SET item_count = t.n, payload_bytes = t.b
  FROM (SELECT changeset_id, count(*) AS n, COALESCE(sum(octet_length(payload::text)), 0) AS b
          FROM hatamoto_changeset_items GROUP BY changeset_id) t
 WHERE t.changeset_id = c.id AND c.state = 'staged';

-- state: running | finished | failed
UPDATE hatamoto_runs SET state = 'finished' WHERE state = 'running';
CREATE UNIQUE INDEX IF NOT EXISTS hatamoto_runs_one_active ON hatamoto_runs (plugin) WHERE state = 'running';

CREATE TABLE IF NOT EXISTS hatamoto_run_proposals (
    id         bigserial PRIMARY KEY,
    run_id     uuid NOT NULL REFERENCES hatamoto_runs (id) ON DELETE CASCADE,
    kind       text NOT NULL,
    payload    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_run_proposals_run_id ON hatamoto_run_proposals (run_id);

CREATE TABLE IF NOT EXISTS hatamoto_run_uploads (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plugin     text NOT NULL,
    filename   text NOT NULL,
    size       bigint NOT NULL,
    blob_key   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_run_uploads;
DROP TABLE IF EXISTS hatamoto_run_proposals;
DROP INDEX IF EXISTS hatamoto_runs_one_active;
ALTER TABLE hatamoto_changesets
    DROP COLUMN IF EXISTS payload_bytes,
    DROP COLUMN IF EXISTS item_count;
ALTER TABLE hatamoto_runs
    DROP COLUMN IF EXISTS steps,
    DROP COLUMN IF EXISTS changeset_id,
    DROP COLUMN IF EXISTS reason,
    DROP COLUMN IF EXISTS summary,
    DROP COLUMN IF EXISTS progress,
    DROP COLUMN IF EXISTS upload_id,
    DROP COLUMN IF EXISTS hosts,
    DROP COLUMN IF EXISTS params,
    DROP COLUMN IF EXISTS cursor,
    DROP COLUMN IF EXISTS phase,
    DROP COLUMN IF EXISTS name;
