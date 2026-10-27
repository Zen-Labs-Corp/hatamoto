-- +goose Up
-- The Go admin's first-run wizard: the site's public origin (precedence:
-- PUBLIC_BASE_URL env, then this column, then the request host) and when setup
-- was finished. Installs that already have users are marked complete so the
-- wizard never appears on a running site.
ALTER TABLE hatamoto_site_config
    ADD COLUMN public_base_url text NOT NULL DEFAULT '',
    ADD COLUMN setup_completed_at timestamptz;

UPDATE hatamoto_site_config SET setup_completed_at = now()
WHERE EXISTS (SELECT 1 FROM hatamoto_users);

-- +goose Down
ALTER TABLE hatamoto_site_config
    DROP COLUMN IF EXISTS setup_completed_at,
    DROP COLUMN IF EXISTS public_base_url;
