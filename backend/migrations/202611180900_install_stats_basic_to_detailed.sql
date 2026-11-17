-- Install stats are on (Detailed) or off now: the Basic level is gone. Rows
-- that chose or inherited Basic become Detailed. Off and unset rows are left
-- alone.
--
-- Data only: no column or constraint changes. The previous release already
-- knows the value 'detailed', so it is unaffected; if it re-writes a row to
-- Basic after a rollback, this release reads that as Detailed.

-- +goose Up
UPDATE hatamoto_install_stats
SET level = 'detailed', health_window_start = COALESCE(health_window_start, now())
WHERE level = 'basic' AND consent = 'yes';

-- +goose Down
-- No-op: the former Basic rows cannot be told apart from the others.
SELECT 1;
