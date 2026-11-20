-- Comment identity: one row per confirmation mail sent. The token itself is
-- never stored, only its SHA-256. Rows double as the send counter for the
-- per-address, per-ip_hash and per-net_hash limits (ip_hash / net_hash are the
-- comment's HMACs, never a raw IP). Expand only: a new table the previous
-- release never reads. Comments stay disabled. Rollback floor: unchanged.

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_comment_confirmations (
    token_hash   bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    -- comment_id is declared before commenter_id on purpose: the insert's
    -- foreign-key locks then follow the comment -> commenter lock order.
    comment_id   uuid REFERENCES hatamoto_comments(id) ON DELETE SET NULL,
    commenter_id uuid NOT NULL REFERENCES hatamoto_commenters(id) ON DELETE CASCADE,
    ip_hash      text NOT NULL DEFAULT '',
    net_hash     text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz
);
CREATE INDEX IF NOT EXISTS hatamoto_comment_confirmations_commenter_idx ON hatamoto_comment_confirmations (commenter_id, created_at);
CREATE INDEX IF NOT EXISTS hatamoto_comment_confirmations_ip_idx ON hatamoto_comment_confirmations (ip_hash, created_at) WHERE ip_hash <> '';
CREATE INDEX IF NOT EXISTS hatamoto_comment_confirmations_net_idx ON hatamoto_comment_confirmations (net_hash, created_at) WHERE net_hash <> '';
CREATE INDEX IF NOT EXISTS hatamoto_comment_confirmations_created_idx ON hatamoto_comment_confirmations (created_at);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_comment_confirmations;
