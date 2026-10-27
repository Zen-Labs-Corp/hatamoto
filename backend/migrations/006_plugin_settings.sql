-- +goose Up
-- Per-plugin settings, edited through /api/v1/plugins/{name}/settings and
-- validated against the plugin manifest's JSON Schema. Plugin-owned tables
-- are created by the plugins' own migrations, not here.
CREATE TABLE hatamoto_plugin_settings (
    plugin     text PRIMARY KEY,
    settings   jsonb NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE hatamoto_plugin_settings;
