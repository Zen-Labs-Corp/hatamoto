-- Sign-in provider identities: one row per (issuer, subject) linked to a user.
-- Expand only: a new table the previous release never reads. Rollback floor: unchanged.

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_user_identities (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES hatamoto_users(id) ON DELETE CASCADE,
    issuer       text NOT NULL CHECK (issuer <> '' AND length(issuer) <= 512),
    subject      text NOT NULL CHECK (subject <> '' AND length(subject) <= 512),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    CONSTRAINT hatamoto_user_identities_issuer_subject_key UNIQUE (issuer, subject)
);
CREATE INDEX IF NOT EXISTS hatamoto_user_identities_user_idx ON hatamoto_user_identities (user_id);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_user_identities;
