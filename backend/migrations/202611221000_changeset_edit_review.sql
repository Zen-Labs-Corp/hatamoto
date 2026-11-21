-- Human edits as changesets: the kind of a changeset (run | edit), who asked
-- for an edit, the ordered review stages snapshotted when it was staged, and
-- the per stage decisions.
--
-- Expand only: new columns with a default and a new table. The decisions
-- table references only a table created earlier. The previous release's
-- binary never reads or writes any of this, and every existing row keeps
-- working as a plain run changeset (kind 'run', no stages).
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
ALTER TABLE hatamoto_changesets ADD COLUMN IF NOT EXISTS kind         text  NOT NULL DEFAULT 'run'; -- run | edit
ALTER TABLE hatamoto_changesets ADD COLUMN IF NOT EXISTS requested_by text  NOT NULL DEFAULT '';
ALTER TABLE hatamoto_changesets ADD COLUMN IF NOT EXISTS stages       jsonb NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS hatamoto_changeset_stage_decisions (
    id           bigserial   PRIMARY KEY,
    changeset_id bigint      NOT NULL REFERENCES hatamoto_changesets(id) ON DELETE CASCADE,
    stage        int         NOT NULL CHECK (stage >= 0),
    reviewer     text        NOT NULL,
    decision     text        NOT NULL CHECK (decision IN ('approved','rejected')),
    comment      text        NOT NULL DEFAULT '',
    decided_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (changeset_id, stage)
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_changeset_stage_decisions;
ALTER TABLE hatamoto_changesets DROP COLUMN IF EXISTS stages, DROP COLUMN IF EXISTS requested_by, DROP COLUMN IF EXISTS kind;
