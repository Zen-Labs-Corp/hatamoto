-- +goose Up
-- Capabilities the owner granted to each plugin (see docs/PLUGIN_API.md). The
-- plugin's own schema and role are created at runtime by the application's
-- database role, not here.
CREATE TABLE hatamoto_plugin_grants (
    plugin      text PRIMARY KEY,
    permissions text[] NOT NULL DEFAULT '{}',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE hatamoto_plugin_grants;
