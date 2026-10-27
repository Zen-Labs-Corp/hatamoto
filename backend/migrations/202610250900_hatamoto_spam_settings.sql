-- +goose Up
-- Site-wide spam protection settings: one row (id is always true). The captcha
-- secret is stored sealed (AES-256-GCM) and never in clear. A missing row means
-- the built-in defaults. Additive only; validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_spam_settings (
    id              boolean     PRIMARY KEY DEFAULT true,
    provider        text        NOT NULL DEFAULT 'none',
    site_key        text        NOT NULL DEFAULT '',
    secret_sealed   bytea,
    min_seconds     integer     NOT NULL DEFAULT 3,
    max_links       integer     NOT NULL DEFAULT 2,
    blocked_words   text        NOT NULL DEFAULT '',
    blocked_scripts text        NOT NULL DEFAULT '',
    action          text        NOT NULL DEFAULT 'spam',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_spam_settings;
