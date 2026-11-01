-- +goose Up
-- How the sitemap child files of a multi-locale site are named: "group" is one
-- file per locale and group (sitemap-xx-post.xml), "legacy" is one file per
-- locale (sitemap-xx.xml). Additive, N-1 binaries ignore the column.
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS sitemap_naming text NOT NULL DEFAULT 'group'
  CHECK (sitemap_naming IN ('group','legacy'));

-- +goose Down
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS sitemap_naming;
