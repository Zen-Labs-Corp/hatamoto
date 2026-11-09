-- +goose Up
-- level is the owner's choice (unset, off, basic, detailed). consent keeps its
-- meaning: yes for basic and detailed, no for off, so an older binary that only
-- reads consent keeps working and treats every opted-in install as Basic.
-- The h_* columns hold the Detailed level's health counts since
-- health_window_start; h_sent is what the last report carried; last_sent_version lets a new version report at once.
ALTER TABLE hatamoto_install_stats
    ADD COLUMN IF NOT EXISTS level TEXT NOT NULL DEFAULT 'unset'
        CHECK (level IN ('unset', 'off', 'basic', 'detailed')),
    ADD COLUMN IF NOT EXISTS last_sent_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS nudge_dismissed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS health_window_start TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS h_panics BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS h_http_5xx BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS h_render_errors BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS h_migration_failed BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS h_plugin_errors JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS h_sent JSONB NOT NULL DEFAULT '{}';

UPDATE hatamoto_install_stats
   SET level = CASE consent WHEN 'yes' THEN 'basic' WHEN 'no' THEN 'off' ELSE 'unset' END;

-- +goose Down
ALTER TABLE hatamoto_install_stats
    DROP COLUMN IF EXISTS h_sent,
    DROP COLUMN IF EXISTS h_plugin_errors,
    DROP COLUMN IF EXISTS h_migration_failed,
    DROP COLUMN IF EXISTS h_render_errors,
    DROP COLUMN IF EXISTS h_http_5xx,
    DROP COLUMN IF EXISTS h_panics,
    DROP COLUMN IF EXISTS health_window_start,
    DROP COLUMN IF EXISTS nudge_dismissed,
    DROP COLUMN IF EXISTS last_sent_version,
    DROP COLUMN IF EXISTS level;
