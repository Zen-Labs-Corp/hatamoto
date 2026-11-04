-- +goose Up
-- Photo credit per media item, set by a plugin run next to the caption.
-- Expand-only: the previous binary ignores the column.
ALTER TABLE hatamoto_media
  ADD COLUMN IF NOT EXISTS credit text;

-- +goose Down
ALTER TABLE hatamoto_media
  DROP COLUMN IF EXISTS credit;
