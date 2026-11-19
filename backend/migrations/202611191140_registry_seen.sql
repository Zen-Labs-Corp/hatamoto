-- Newest registry index time accepted per URL, so an older signed copy that is
-- still in date is refused once a newer one has been seen.
--
-- Expand only: one new table the previous release never reads or writes.

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_registry_seen (
    url       text PRIMARY KEY,
    issued_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_registry_seen;
