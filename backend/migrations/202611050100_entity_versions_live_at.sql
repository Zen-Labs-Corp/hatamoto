-- +goose Up
-- live_at marks a version that was live (its entity was published) up to the
-- moment it was snapshotted. Pruning keeps such versions for 30 days.
-- Expand-only: the previous binary ignores the column.
ALTER TABLE hatamoto_entity_versions ADD COLUMN IF NOT EXISTS live_at timestamptz;

-- Errs toward keeping: recent versions of currently published entities count as live.
UPDATE hatamoto_entity_versions v SET live_at = v.created_at
  FROM hatamoto_entities e
 WHERE e.id = v.entity_id AND e.status = 'published'
   AND v.created_at > now() - interval '30 days';

CREATE INDEX IF NOT EXISTS hatamoto_entity_versions_live_idx
  ON hatamoto_entity_versions (live_at) WHERE live_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS hatamoto_entity_versions_live_idx;
ALTER TABLE hatamoto_entity_versions DROP COLUMN IF EXISTS live_at;
