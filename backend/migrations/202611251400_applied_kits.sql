-- +goose Up
-- Which site kits were applied to this site, at which version, by whom.
-- Additive only: a new table, nothing existing changes.
CREATE TABLE hatamoto_applied_kits (
    name       text PRIMARY KEY,
    version    text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    applied_by text NOT NULL,
    summary    jsonb NOT NULL DEFAULT '{}'
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_applied_kits;
