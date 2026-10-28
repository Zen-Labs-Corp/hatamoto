-- +goose Up
-- One row of opt-in install-stats state. A missing row means consent is still
-- unset. install_id exists only while consent is 'yes' (the CHECK makes any
-- other combination impossible), so turning stats off cannot leave an ID
-- behind. last_sent_at is stamped when a send is claimed, so replicas sharing
-- one database send at most once per interval.
CREATE TABLE IF NOT EXISTS hatamoto_install_stats (
    id            BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    consent       TEXT NOT NULL DEFAULT 'unset' CHECK (consent IN ('unset', 'yes', 'no')),
    install_id    TEXT,
    last_sent_at  TIMESTAMPTZ,
    last_status   TEXT NOT NULL DEFAULT '',
    CHECK (consent = 'yes' OR install_id IS NULL)
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_install_stats;
