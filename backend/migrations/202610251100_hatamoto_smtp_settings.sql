-- +goose Up
-- Site-wide SMTP settings saved from the admin: one row (id is always true).
-- The password is stored sealed (AES-256-GCM) and never in clear. No row means
-- the server's SMTP_* environment applies. Additive only; validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_smtp_settings (
    id              boolean     PRIMARY KEY DEFAULT true CHECK (id),
    provider        text        NOT NULL DEFAULT 'custom',
    ses_region      text        NOT NULL DEFAULT '',
    host            text        NOT NULL,
    port            integer     NOT NULL,
    security        text        NOT NULL,
    username        text        NOT NULL DEFAULT '',
    password_sealed bytea,
    from_address    text        NOT NULL,
    from_name       text        NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_smtp_settings;
