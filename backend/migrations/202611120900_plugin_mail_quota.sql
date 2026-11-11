-- Per-plugin mail quota: how many recipients per hour a plugin may email
-- through mail.send. Set by the owner, read by the host and returned to the
-- plugin by permissions(); a plugin can never write it.
--
-- Expand only: one nullable column with no default, so existing rows and the
-- upserts that write only the permissions are unaffected. NULL means the
-- default of 360 recipients per hour. The previous release never reads it.
-- The 1..3600 range is enforced in Go (store and admin handler), not here.

-- +goose Up
ALTER TABLE hatamoto_plugin_grants
    ADD COLUMN IF NOT EXISTS mail_per_hour integer;
COMMENT ON COLUMN hatamoto_plugin_grants.mail_per_hour IS
    'Owner-set mail.send recipients per hour; NULL = default 360. Never plugin-writable.';

-- +goose Down
ALTER TABLE hatamoto_plugin_grants DROP COLUMN IF EXISTS mail_per_hour;
