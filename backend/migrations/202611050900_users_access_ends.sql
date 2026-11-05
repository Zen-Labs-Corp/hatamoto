-- +goose Up
-- Optional end of access for a user. access_ends_at is the instant from which
-- the user cannot sign in; the two *_for columns record the end value the
-- worker already revoked or reminded for, so a changed date is picked up again
-- without resetting anything. Expand-only: the previous binary ignores them.
ALTER TABLE hatamoto_users
  ADD COLUMN IF NOT EXISTS access_ends_at timestamptz,
  ADD COLUMN IF NOT EXISTS access_ended_for timestamptz,
  ADD COLUMN IF NOT EXISTS access_reminder_for timestamptz;

CREATE INDEX IF NOT EXISTS hatamoto_users_access_ends
  ON hatamoto_users (access_ends_at) WHERE access_ends_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS hatamoto_users_access_ends;
ALTER TABLE hatamoto_users
  DROP COLUMN IF EXISTS access_reminder_for,
  DROP COLUMN IF EXISTS access_ended_for,
  DROP COLUMN IF EXISTS access_ends_at;
