-- +goose Up
-- Two-step sign-in: the owner policy, one authenticator secret per user,
-- passkeys, one-time recovery codes and single-use WebAuthn challenges.
-- Expand-only: the previous binary ignores the column and the tables.
ALTER TABLE hatamoto_site_config
  ADD COLUMN IF NOT EXISTS two_factor_policy text NOT NULL DEFAULT 'optional'
  CHECK (two_factor_policy IN ('optional', 'owners', 'everyone'));

CREATE TABLE IF NOT EXISTS hatamoto_user_totp (
  user_id       uuid PRIMARY KEY REFERENCES hatamoto_users(id) ON DELETE CASCADE,
  secret_sealed bytea NOT NULL,
  confirmed_at  timestamptz,
  last_step     bigint NOT NULL DEFAULT 0,
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hatamoto_user_passkeys (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid NOT NULL REFERENCES hatamoto_users(id) ON DELETE CASCADE,
  credential_id bytea NOT NULL UNIQUE,
  credential    jsonb NOT NULL,
  name          text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_used_at  timestamptz
);
CREATE INDEX IF NOT EXISTS hatamoto_user_passkeys_user_idx ON hatamoto_user_passkeys (user_id);

CREATE TABLE IF NOT EXISTS hatamoto_user_recovery_codes (
  id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id   uuid NOT NULL REFERENCES hatamoto_users(id) ON DELETE CASCADE,
  code_hash bytea NOT NULL,
  used_at   timestamptz
);
CREATE INDEX IF NOT EXISTS hatamoto_user_recovery_codes_user_idx ON hatamoto_user_recovery_codes (user_id);

CREATE TABLE IF NOT EXISTS hatamoto_webauthn_ceremonies (
  id         uuid PRIMARY KEY,
  user_id    uuid,
  purpose    text NOT NULL,
  data       jsonb NOT NULL,
  expires_at timestamptz NOT NULL
);
-- The purge on every begin deletes by expires_at.
CREATE INDEX IF NOT EXISTS hatamoto_webauthn_ceremonies_expires_idx ON hatamoto_webauthn_ceremonies (expires_at);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_webauthn_ceremonies;
DROP TABLE IF EXISTS hatamoto_user_recovery_codes;
DROP TABLE IF EXISTS hatamoto_user_passkeys;
DROP TABLE IF EXISTS hatamoto_user_totp;
ALTER TABLE hatamoto_site_config
  DROP COLUMN IF EXISTS two_factor_policy;
