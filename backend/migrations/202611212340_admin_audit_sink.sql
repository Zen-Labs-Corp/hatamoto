-- +goose Up
-- Widens the admin audit log for the audit sink: who acted (kind, key),
-- from where (ip, user agent, request id) and the outcome. Additive only.
ALTER TABLE hatamoto_admin_audit
    ADD COLUMN IF NOT EXISTS actor_kind   text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS actor_key_id uuid,
    ADD COLUMN IF NOT EXISTS ip           text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS user_agent   text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS request_id   text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS result       text NOT NULL DEFAULT 'ok';
CREATE INDEX IF NOT EXISTS idx_hatamoto_admin_audit_action_at ON hatamoto_admin_audit (action, at DESC);
CREATE INDEX IF NOT EXISTS idx_hatamoto_admin_audit_actor_at  ON hatamoto_admin_audit (actor_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_hatamoto_admin_audit_target    ON hatamoto_admin_audit (target);

-- +goose Down
DROP INDEX IF EXISTS idx_hatamoto_admin_audit_target;
DROP INDEX IF EXISTS idx_hatamoto_admin_audit_actor_at;
DROP INDEX IF EXISTS idx_hatamoto_admin_audit_action_at;
ALTER TABLE hatamoto_admin_audit
    DROP COLUMN IF EXISTS result,
    DROP COLUMN IF EXISTS request_id,
    DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS ip,
    DROP COLUMN IF EXISTS actor_key_id,
    DROP COLUMN IF EXISTS actor_kind;
