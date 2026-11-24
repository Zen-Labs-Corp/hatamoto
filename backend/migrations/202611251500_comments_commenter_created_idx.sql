-- Comments: a commenter's comments newest first, and commenters by creation time.
-- Serves the plugin read of one commenter's comments (ORDER BY created_at DESC,
-- seq DESC, no in-memory sort) and the incremental commenter scan that resolves
-- plugin commenter keys. Expand only: two new indexes the previous release never
-- uses. Rollback floor: unchanged.

-- +goose Up
CREATE INDEX IF NOT EXISTS hatamoto_comments_commenter_created_idx
    ON hatamoto_comments (commenter_id, created_at DESC, seq DESC);
CREATE INDEX IF NOT EXISTS hatamoto_commenters_created_idx
    ON hatamoto_commenters (created_at);

-- +goose Down
DROP INDEX IF EXISTS hatamoto_commenters_created_idx;
DROP INDEX IF EXISTS hatamoto_comments_commenter_created_idx;
