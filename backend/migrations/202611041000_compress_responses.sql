-- +goose Up
-- Response compression switch (zstd, gzip) for the app's own text responses.
-- On by default, also for existing installs. Expand-only: the previous binary
-- ignores the column.
ALTER TABLE hatamoto_site_config
  ADD COLUMN IF NOT EXISTS compress_responses boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE hatamoto_site_config
  DROP COLUMN IF EXISTS compress_responses;
