-- Scheduled unpublish and shareable draft preview links.
--
-- hatamoto_entity_schedule keeps the optional unpublish date (and the
-- redirect target to leave behind) beside the entity, so the many entity
-- SELECT lists stay unchanged. hatamoto_preview_links holds the SHA-256 of
-- each preview token; the token itself is never stored.
--
-- Expand only: two new tables. The previous release's binary never reads or
-- writes them.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_entity_schedule (
    entity_id       uuid PRIMARY KEY REFERENCES hatamoto_entities (id) ON DELETE CASCADE,
    unpublish_at    timestamptz NOT NULL,
    redirect_target text        NOT NULL DEFAULT '',
    redirect_id     uuid
);
CREATE INDEX IF NOT EXISTS idx_entity_schedule_due ON hatamoto_entity_schedule (unpublish_at);

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
DROP INDEX IF EXISTS idx_entity_schedule_due;
DROP TABLE IF EXISTS hatamoto_entity_schedule;
