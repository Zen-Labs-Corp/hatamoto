-- +goose Up
-- Per-account login lockout state. `failed_login_attempts` counts consecutive
-- failures since the last success; `locked_until` (when in the future) blocks
-- authentication. Both are reset on a successful login. Additive and safe to
-- run against an already-migrated database.
ALTER TABLE public.hatamoto_users
    ADD COLUMN IF NOT EXISTS failed_login_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS locked_until timestamp with time zone;

-- +goose Down
ALTER TABLE public.hatamoto_users
    DROP COLUMN IF EXISTS failed_login_attempts,
    DROP COLUMN IF EXISTS locked_until;
