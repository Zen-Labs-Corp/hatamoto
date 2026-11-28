-- +goose Up
-- The page protection switch (Overview card). NULL = never chosen: the site
-- follows CSP_ENFORCE or the default (enforce). A row that exists now is an
-- install from before enforcement became the default: pin it to report-only so
-- the upgrade does not start blocking scripts. A fresh database has no row yet
-- (first boot inserts it after migrations), so it stays NULL and enforces.
-- Expand-only: the previous binary ignores the column.
-- The pin is a metadata-only column default that is cleared right away: the
-- existing rows keep false without being rewritten, so re-running the
-- migration never writes a row and never pins a row created since.
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS csp_enforce boolean DEFAULT false;
ALTER TABLE hatamoto_site_config ALTER COLUMN csp_enforce SET DEFAULT NULL;

-- +goose Down
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS csp_enforce;
