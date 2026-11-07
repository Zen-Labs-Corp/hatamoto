-- +goose Up
-- An invite carries the end of access its user will get when it is accepted.
-- Expand-only: the previous binary ignores the column.
ALTER TABLE hatamoto_user_tokens
  ADD COLUMN IF NOT EXISTS access_ends_at timestamptz;

-- +goose Down
ALTER TABLE hatamoto_user_tokens
  DROP COLUMN IF EXISTS access_ends_at;
