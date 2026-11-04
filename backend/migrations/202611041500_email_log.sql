-- Email log: one row per mail the server hands to SMTP or drops, so an owner
-- can see what was sent, filter it and resend it, plus the two retention
-- settings that bound how long bodies and rows are kept.
--
-- Expand only: a new table and two columns with a default. The previous
-- release's binary never reads or writes any of this.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_email_log (
    id          bigserial PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now(),
    recipients  text[]      NOT NULL,
    subject     text        NOT NULL,
    kind        text        NOT NULL,
    plugin      text        NOT NULL DEFAULT '',
    status      text        NOT NULL CHECK (status IN ('sent', 'failed', 'suppressed')),
    error       text        NOT NULL DEFAULT '',
    message_id  text        NOT NULL DEFAULT '',
    body_text   text,
    body_html   text,
    resent_from bigint REFERENCES hatamoto_email_log (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_email_log_created ON hatamoto_email_log (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_email_log_status ON hatamoto_email_log (status, created_at DESC);

ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS email_log_body_days int NOT NULL DEFAULT 30;
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS email_log_days int NOT NULL DEFAULT 90;

-- +goose Down
ALTER TABLE hatamoto_site_config
    DROP COLUMN IF EXISTS email_log_days,
    DROP COLUMN IF EXISTS email_log_body_days;
DROP INDEX IF EXISTS idx_email_log_status;
DROP INDEX IF EXISTS idx_email_log_created;
DROP TABLE IF EXISTS hatamoto_email_log;
