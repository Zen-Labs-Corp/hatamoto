-- +goose Up
-- v1-18 review fixes: an uninstall that can be resumed, and the server's own
-- ENVIRONMENT, which the CLI reads to know whether dev packages are allowed.

ALTER TABLE hatamoto_packages
    ADD COLUMN state text NOT NULL DEFAULT 'installed' CHECK (state IN ('installed', 'uninstalling'));

ALTER TABLE hatamoto_package_config ADD COLUMN server_environment text;

-- +goose Down
ALTER TABLE hatamoto_package_config DROP COLUMN server_environment;
ALTER TABLE hatamoto_packages DROP COLUMN state;
