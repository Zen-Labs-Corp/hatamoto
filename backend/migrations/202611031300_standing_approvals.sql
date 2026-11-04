-- Standing approvals: the owner's one-time approval for a cron started run to
-- publish by itself inside a declared rule, the marker on a changeset that
-- was published that way, how a run was started, and the day the owner
-- digest was last sent.
--
-- Expand only: new tables, and new columns that are nullable or have a
-- default. The approvals table has no foreign key, so uninstalling a plugin
-- never has to touch it. The partial unique index allows one active approval
-- per plugin and run. The previous release's binary never reads or writes
-- any of this.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_standing_approvals (
    id          bigserial PRIMARY KEY,
    plugin      text        NOT NULL,
    run         text        NOT NULL,
    policy      jsonb       NOT NULL,  -- fields, schemas, max_per_hour as approved
    fingerprint jsonb       NOT NULL,  -- plugin version, manifest hash, schema hashes at approval
    approved_by text        NOT NULL DEFAULT '',
    approved_at timestamptz NOT NULL DEFAULT now(),
    voided_at   timestamptz,
    void_reason text        NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_standing_active
    ON hatamoto_standing_approvals (plugin, run) WHERE voided_at IS NULL;

ALTER TABLE hatamoto_changesets ADD COLUMN IF NOT EXISTS auto_published boolean NOT NULL DEFAULT false;
ALTER TABLE hatamoto_changesets ADD COLUMN IF NOT EXISTS approval_id bigint;
ALTER TABLE hatamoto_runs ADD COLUMN IF NOT EXISTS trigger text NOT NULL DEFAULT 'owner'; -- owner | cron

CREATE TABLE IF NOT EXISTS hatamoto_digest_state (
    name         text PRIMARY KEY,
    sent_through timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_changesets_auto
    ON hatamoto_changesets (published_at) WHERE auto_published;

-- +goose Down
DROP INDEX IF EXISTS idx_changesets_auto;
DROP TABLE IF EXISTS hatamoto_digest_state;
ALTER TABLE hatamoto_runs DROP COLUMN IF EXISTS trigger;
ALTER TABLE hatamoto_changesets
    DROP COLUMN IF EXISTS approval_id,
    DROP COLUMN IF EXISTS auto_published;
DROP INDEX IF EXISTS uq_standing_active;
DROP TABLE IF EXISTS hatamoto_standing_approvals;
