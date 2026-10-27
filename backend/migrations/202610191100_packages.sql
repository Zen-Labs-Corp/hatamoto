-- +goose Up
-- v1-18-packages-and-themes: installed .hmp packages (plugins and themes), the
-- owner's package policy, and per-theme settings. Core content tables are not
-- touched, and removing a package never changes them.

CREATE TABLE hatamoto_packages (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind          text NOT NULL CHECK (kind IN ('plugin', 'theme')),
    name          text NOT NULL,
    version       text NOT NULL,
    -- plugin.json or theme.json as shipped.
    manifest      jsonb NOT NULL,
    -- The .hmp archive itself.
    data          bytea NOT NULL,
    -- Lower-case hex SHA-256 of data.
    sha256        text NOT NULL,
    -- ed25519 signature over the package digest and the id of the signing key;
    -- both NULL for an unsigned package (owner opt-in, or a dev push).
    signature     bytea,
    signer_key_id text,
    -- registry, upload, url or dev.
    source        text NOT NULL CHECK (source IN ('registry', 'upload', 'url', 'dev')),
    -- Plugins: loaded at boot and live. Themes ignore it.
    enabled       boolean NOT NULL DEFAULT true,
    installed_at  timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, name),
    CHECK ((signature IS NULL) = (signer_key_id IS NULL))
);

-- One row: the policy and which theme is active (NULL = the built-in default).
CREATE TABLE hatamoto_package_config (
    singleton      boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    allow_unsigned boolean NOT NULL DEFAULT false,
    active_theme   text,
    updated_at     timestamptz NOT NULL DEFAULT now()
);
INSERT INTO hatamoto_package_config (singleton) VALUES (true);

-- Values of a theme's settings_schema, kept across theme updates.
CREATE TABLE hatamoto_theme_settings (
    theme      text PRIMARY KEY,
    settings   jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE hatamoto_theme_settings;
DROP TABLE hatamoto_package_config;
DROP TABLE hatamoto_packages;
