-- Review decisions: per-item risk class and owner decision on staged
-- changeset items, and the owner's decision plus the pre-image (for Undo) on
-- run proposals.
--
-- Expand only: every new column is nullable with no default, nothing is
-- renamed, dropped or rewritten. The previous release's binary never reads
-- or writes these columns.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
ALTER TABLE hatamoto_changeset_items
    ADD COLUMN IF NOT EXISTS risk        text,     -- low | high | critical; NULL = not classified yet
    ADD COLUMN IF NOT EXISTS risk_reason text,     -- link_host | link_query | embed | hidden_char | redirect | slug | delete | menu_link
    ADD COLUMN IF NOT EXISTS risk_ref    integer,  -- seq of the staged page a grouped redirect points to
    ADD COLUMN IF NOT EXISTS decision    text;     -- NULL | approved | dropped
ALTER TABLE hatamoto_run_proposals
    ADD COLUMN IF NOT EXISTS decision   text,
    ADD COLUMN IF NOT EXISTS before     jsonb,     -- pre-image written when an approval is applied, used by Undo
    ADD COLUMN IF NOT EXISTS decided_at timestamptz;

-- +goose Down
ALTER TABLE hatamoto_run_proposals
    DROP COLUMN IF EXISTS decided_at,
    DROP COLUMN IF EXISTS before,
    DROP COLUMN IF EXISTS decision;
ALTER TABLE hatamoto_changeset_items
    DROP COLUMN IF EXISTS decision,
    DROP COLUMN IF EXISTS risk_ref,
    DROP COLUMN IF EXISTS risk_reason,
    DROP COLUMN IF EXISTS risk;
