-- +goose Up
-- Backup encryption saved from the admin: one row (id is always true). Either an
-- age public key (recipient) or a passphrase sealed with the server secret
-- (AES-256-GCM), never both and never a private key. No row, or both empty,
-- means downloads and off-site copies are not encrypted. Additive only;
-- validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_backup_encryption (
    id                boolean     PRIMARY KEY DEFAULT true CHECK (id),
    recipient         text        NOT NULL DEFAULT '',
    passphrase_sealed bytea,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_backup_encryption;
