-- Comment notifications: the reply opt-in on a comment, per-moderator throttle and
-- unsubscribe state, the queue of held comments a moderator was not mailed about
-- yet, and the queue of approved replies to mail. Expand only: a new column with a
-- default and new tables the previous release never reads. Comments stay disabled.
-- Rollback floor: unchanged.

-- +goose Up
ALTER TABLE hatamoto_comments ADD COLUMN IF NOT EXISTS notify_replies boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS hatamoto_comment_moderator_notify (
    user_id      uuid PRIMARY KEY REFERENCES hatamoto_users(id) ON DELETE CASCADE,
    muted        boolean NOT NULL DEFAULT false,
    last_sent_at timestamptz
);

CREATE TABLE IF NOT EXISTS hatamoto_comment_notify_queue (
    user_id    uuid NOT NULL REFERENCES hatamoto_users(id) ON DELETE CASCADE,
    comment_id uuid NOT NULL REFERENCES hatamoto_comments(id) ON DELETE CASCADE,
    queued_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, comment_id)
);
CREATE INDEX IF NOT EXISTS hatamoto_comment_notify_queue_comment_idx ON hatamoto_comment_notify_queue (comment_id);

CREATE TABLE IF NOT EXISTS hatamoto_comment_reply_queue (
    comment_id uuid PRIMARY KEY REFERENCES hatamoto_comments(id) ON DELETE CASCADE,
    queued_at  timestamptz NOT NULL DEFAULT now(),
    sent_at    timestamptz
);
CREATE INDEX IF NOT EXISTS hatamoto_comment_reply_queue_due_idx ON hatamoto_comment_reply_queue (queued_at) WHERE sent_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS hatamoto_comment_reply_queue;
DROP TABLE IF EXISTS hatamoto_comment_notify_queue;
DROP TABLE IF EXISTS hatamoto_comment_moderator_notify;
ALTER TABLE hatamoto_comments DROP COLUMN IF EXISTS notify_replies;
