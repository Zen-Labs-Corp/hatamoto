-- Transactional changesets: who owns a content object, and the tables a
-- changeset engine needs (runs, changesets, items, a per-run buffer and the
-- source-key mapping that makes a re-run update instead of duplicate).
--
-- Expand only: one column with a default on each content table (the default
-- is the backfill: every existing row is owned by the site owner), one
-- nullable column on media for staged files, and new tables. Nothing is
-- added to an existing table that the previous release's binary could
-- violate: its inserts omit "origin" and get 'owner'. Ownership is enforced
-- in code, so there is no CHECK or foreign key on the old tables.

-- +goose Up
ALTER TABLE hatamoto_entities ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'owner';
ALTER TABLE hatamoto_terms    ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'owner';
ALTER TABLE hatamoto_menus    ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'owner';
ALTER TABLE hatamoto_redirects ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'owner';
ALTER TABLE hatamoto_media    ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'owner';
-- A staged file belongs to a run until a published changeset attaches it.
ALTER TABLE hatamoto_media    ADD COLUMN IF NOT EXISTS run_id uuid;

-- Only plugin-owned rows are ever looked up by origin.
CREATE INDEX IF NOT EXISTS idx_entities_origin  ON hatamoto_entities  (origin) WHERE origin <> 'owner';
CREATE INDEX IF NOT EXISTS idx_terms_origin     ON hatamoto_terms     (origin) WHERE origin <> 'owner';
CREATE INDEX IF NOT EXISTS idx_menus_origin     ON hatamoto_menus     (origin) WHERE origin <> 'owner';
CREATE INDEX IF NOT EXISTS idx_redirects_origin ON hatamoto_redirects (origin) WHERE origin <> 'owner';
CREATE INDEX IF NOT EXISTS idx_media_origin     ON hatamoto_media     (origin) WHERE origin <> 'owner';
CREATE INDEX IF NOT EXISTS idx_media_run_id     ON hatamoto_media     (run_id)  WHERE run_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS hatamoto_runs (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plugin     text NOT NULL,
    state      text NOT NULL DEFAULT 'running',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hatamoto_changesets (
    id           bigserial PRIMARY KEY,
    run_id       uuid NOT NULL REFERENCES hatamoto_runs (id) ON DELETE CASCADE,
    plugin       text NOT NULL,
    state        text NOT NULL DEFAULT 'staged', -- staged|published|discarded|reverted|expired
    expires_at   timestamptz NOT NULL,
    published_at timestamptz,
    reverted_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_changesets_run_id ON hatamoto_changesets (run_id);
CREATE INDEX IF NOT EXISTS idx_changesets_staged_expiry ON hatamoto_changesets (expires_at) WHERE state = 'staged';

CREATE TABLE IF NOT EXISTS hatamoto_changeset_items (
    id            bigserial PRIMARY KEY,
    changeset_id  bigint NOT NULL REFERENCES hatamoto_changesets (id) ON DELETE CASCADE,
    seq           int NOT NULL,
    kind          text NOT NULL,
    op            text NOT NULL,                 -- upsert|delete
    source_key    text NOT NULL DEFAULT '',
    target_id     text,
    base_token    text,
    payload       jsonb NOT NULL DEFAULT '{}'::jsonb,
    before        jsonb,                         -- pre-image, written at publish, used by revert
    after_token   text,                          -- token of the live object when the publish ended; revert skips an object that moved since
    before_token  text,                          -- token of the live object just before the publish; lets revert hand the restored token to the changeset built on this one
    blob_keys     text[] NOT NULL DEFAULT '{}',
    result        text,                          -- applied|skipped_conflict|skipped_other|reverted
    result_detail text
);
CREATE INDEX IF NOT EXISTS idx_changeset_items_changeset ON hatamoto_changeset_items (changeset_id, seq);

CREATE TABLE IF NOT EXISTS hatamoto_run_buffer (
    run_id uuid NOT NULL REFERENCES hatamoto_runs (id) ON DELETE CASCADE,
    key    text NOT NULL,
    value  bytea NOT NULL,
    PRIMARY KEY (run_id, key)
);

CREATE TABLE IF NOT EXISTS hatamoto_changeset_source_keys (
    origin     text NOT NULL,
    kind       text NOT NULL,
    source_key text NOT NULL,
    target_id  text NOT NULL,
    PRIMARY KEY (origin, kind, source_key)
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_changeset_source_keys;
DROP TABLE IF EXISTS hatamoto_run_buffer;
DROP TABLE IF EXISTS hatamoto_changeset_items;
DROP TABLE IF EXISTS hatamoto_changesets;
DROP TABLE IF EXISTS hatamoto_runs;
DROP INDEX IF EXISTS idx_media_run_id;
DROP INDEX IF EXISTS idx_media_origin;
DROP INDEX IF EXISTS idx_redirects_origin;
DROP INDEX IF EXISTS idx_menus_origin;
DROP INDEX IF EXISTS idx_terms_origin;
DROP INDEX IF EXISTS idx_entities_origin;
ALTER TABLE hatamoto_media DROP COLUMN IF EXISTS run_id;
ALTER TABLE hatamoto_media DROP COLUMN IF EXISTS origin;
ALTER TABLE hatamoto_redirects DROP COLUMN IF EXISTS origin;
ALTER TABLE hatamoto_menus DROP COLUMN IF EXISTS origin;
ALTER TABLE hatamoto_terms DROP COLUMN IF EXISTS origin;
ALTER TABLE hatamoto_entities DROP COLUMN IF EXISTS origin;
