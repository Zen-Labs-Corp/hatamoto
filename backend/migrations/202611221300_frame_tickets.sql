-- Admin frame tickets (draft preview, plugin page, changeset preview): one
-- row per minted, unspent ticket, so a ticket minted on one server opens on
-- another. Rows live 60 seconds; the HMAC tag is never stored. Expand only:
-- a new table the previous release never reads. Rollback floor: unchanged.

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_frame_tickets (
    nonce      text        PRIMARY KEY,
    purpose    text        NOT NULL,
    session_id uuid        NOT NULL,
    target     text        NOT NULL,
    expires_at timestamptz NOT NULL,
    payload    bytea,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hatamoto_frame_tickets_expires_idx ON hatamoto_frame_tickets (expires_at);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_frame_tickets;
