-- Scheduled unpublish and shareable draft preview links.
--
-- hatamoto_entity_schedule keeps the optional unpublish date (and the
-- redirect target to leave behind) beside the entity, so the many entity
-- SELECT lists stay unchanged. hatamoto_preview_links holds the SHA-256 of
-- each preview token; the token itself is never stored.
--
-- Expand only: three new tables. The previous release's binary never reads or
-- writes them.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_entity_schedule (
    entity_id       uuid PRIMARY KEY REFERENCES hatamoto_entities (id) ON DELETE CASCADE,
    unpublish_at    timestamptz NOT NULL,
    redirect_target text        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_entity_schedule_due ON hatamoto_entity_schedule (unpublish_at);

-- The redirect a fired unpublish created, so republishing can remove it. Kept
-- apart from the pending schedule: a fired schedule is gone, never shown again.
CREATE TABLE IF NOT EXISTS hatamoto_unpublish_redirects (
    entity_id   uuid PRIMARY KEY REFERENCES hatamoto_entities (id) ON DELETE CASCADE,
    redirect_id uuid NOT NULL
);

CREATE TABLE IF NOT EXISTS hatamoto_preview_links (
    id              uuid PRIMARY KEY,
    entity_id       uuid        NOT NULL REFERENCES hatamoto_entities (id) ON DELETE CASCADE,
    token_hash      bytea       NOT NULL UNIQUE,
    created_by      uuid,
    created_by_name text        NOT NULL DEFAULT '',
    token_tail      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz
);
CREATE INDEX IF NOT EXISTS idx_preview_links_entity ON hatamoto_preview_links (entity_id, expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_preview_links_entity;
DROP TABLE IF EXISTS hatamoto_preview_links;
DROP TABLE IF EXISTS hatamoto_unpublish_redirects;
DROP INDEX IF EXISTS idx_entity_schedule_due;
DROP TABLE IF EXISTS hatamoto_entity_schedule;
