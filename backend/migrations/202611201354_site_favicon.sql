-- Site favicon: the address of the image the public site uses as its browser
-- icon (Settings > General). Empty means unset: the theme's own icons are
-- served. Expand only: the previous release never reads or writes it.

-- +goose Up
ALTER TABLE hatamoto_site_config
    ADD COLUMN IF NOT EXISTS favicon_url text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS favicon_url;
