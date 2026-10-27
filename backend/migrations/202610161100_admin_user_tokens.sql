-- +goose Up
-- One-time tokens for the Go admin: password reset by email and user invites.
-- Only the sha256 of the opaque token is stored (same scheme as
-- hatamoto_sessions); the raw token exists in the email and the URL only.
CREATE TABLE hatamoto_user_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        text NOT NULL CHECK (kind IN ('password_reset', 'invite')),
    token_hash  bytea NOT NULL UNIQUE,
    user_id     uuid REFERENCES hatamoto_users(id) ON DELETE CASCADE,
    email       text,
    role        text CHECK (role IN ('owner', 'editor')),
    invited_by  uuid REFERENCES hatamoto_users(id) ON DELETE SET NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chk_user_tokens_shape CHECK (
        (kind = 'password_reset' AND user_id IS NOT NULL)
        OR (kind = 'invite' AND email IS NOT NULL AND role IS NOT NULL)
    )
);

CREATE INDEX hatamoto_user_tokens_user ON hatamoto_user_tokens (user_id, kind) WHERE used_at IS NULL;
CREATE INDEX hatamoto_user_tokens_expiry ON hatamoto_user_tokens (expires_at);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_user_tokens;
