-- +goose Up
-- OIDC sign-in settings saved in Settings > Sign-in: one row (id is always true).
-- The client secret is stored sealed (AES-256-GCM), never in clear. No row = off.
-- Additive only; validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_signin_oidc (
    id                   boolean     PRIMARY KEY DEFAULT true CHECK (id),
    enabled              boolean     NOT NULL DEFAULT false,
    label                text        NOT NULL DEFAULT '',
    issuer               text        NOT NULL DEFAULT '',
    client_id            text        NOT NULL DEFAULT '',
    client_secret_sealed bytea,
    allowed_domains      text[]      NOT NULL DEFAULT '{}',
    default_role         text        NOT NULL DEFAULT 'editor',
    link_existing        boolean     NOT NULL DEFAULT false,
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_signin_oidc;
