-- +goose Up
-- Server-side recovery copies of unsaved editor and builder work: one row per
-- (user, entity). Deleted on the owner's publish, on discard, after 30 days
-- (prune worker) and with the user or the entity.
CREATE TABLE hatamoto_admin_recovery_copies (
    user_id        uuid NOT NULL REFERENCES hatamoto_users(id)    ON DELETE CASCADE,
    entity_id      uuid NOT NULL REFERENCES hatamoto_entities(id) ON DELETE CASCADE,
    content        text NOT NULL,
    name           text NOT NULL DEFAULT '',
    excerpt        text NOT NULL DEFAULT '',
    featured_image text NOT NULL DEFAULT '',
    base_version   integer NOT NULL DEFAULT 0,
    saved_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, entity_id)
);
CREATE INDEX idx_admin_recovery_copies_saved_at ON hatamoto_admin_recovery_copies (saved_at);

-- +goose Down
DROP TABLE hatamoto_admin_recovery_copies;
