-- +goose Up
-- Cookie consent: one settings row (id is always true) holding a jsonb document,
-- and a per-day counter of the choices visitors made. The counter stores no IP,
-- user agent or visitor id, only day, choice and a count.
-- Additive only; validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_privacy_settings (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    settings   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hatamoto_consent_daily (
    day    date   NOT NULL,
    choice text   NOT NULL CHECK (choice IN ('all', 'none', 'custom')),
    n      bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (day, choice)
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_consent_daily;
DROP TABLE IF EXISTS hatamoto_privacy_settings;
