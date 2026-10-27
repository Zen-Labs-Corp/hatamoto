-- Content carries a locale (a BCP 47 tag). One site is still one language: the
-- column defaults to the site language, so a row written without it (an old
-- binary, an old archive) is valid, and the public site serves only the rows in
-- the site language. Slugs are unique per schema and locale.
--
-- The two slug indexes keep their NAMES: the previous binary and the archive
-- import map a 23505 by constraint name. Only the key gains the locale.
--
-- hatamoto:contract unused-since v0.32
-- hatamoto:backfill idempotent

-- +goose Up
-- Runs as its owner: it is a column default, so any role that may insert an
-- entity evaluates it, and such a role need not read hatamoto_site_config.
-- +goose StatementBegin
CREATE FUNCTION public.hatamoto_site_language() RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
    SELECT COALESCE((SELECT language FROM public.hatamoto_site_config LIMIT 1), 'en')
$$;
-- +goose StatementEnd

-- A STABLE default is stored as metadata: no table rewrite, existing rows read
-- the site language at the time of the migration.
ALTER TABLE public.hatamoto_entities
    ADD COLUMN locale text NOT NULL DEFAULT public.hatamoto_site_language();

DROP INDEX IF EXISTS public.uq_entities_schema_slug;
CREATE UNIQUE INDEX uq_entities_schema_slug
    ON public.hatamoto_entities (schema_id, locale, slug)
    WHERE variant_key IS NULL AND NOT slug_nested;
DROP INDEX IF EXISTS public.uq_entities_schema_parent_slug;
CREATE UNIQUE INDEX uq_entities_schema_parent_slug
    ON public.hatamoto_entities (schema_id, locale, COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid), slug)
    WHERE variant_key IS NULL AND slug_nested;

-- Changing the site language re-tags the entities of the old language, so the
-- public site does not go empty. A row whose slug already exists in the new
-- language stays as stored: the settings save never fails. updated_at is kept
-- (the import flag makes entities_updated_at leave it alone).
-- +goose StatementBegin
CREATE FUNCTION public.entity_locale_retag() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    prev text := current_setting('hatamoto.import', true);
BEGIN
    PERFORM set_config('hatamoto.import', 'on', true);
    UPDATE public.hatamoto_entities e SET locale = NEW.language
     WHERE e.locale = OLD.language
       AND (e.variant_key IS NOT NULL OR NOT EXISTS (
            SELECT 1 FROM public.hatamoto_entities o
             WHERE o.locale = NEW.language
               AND o.schema_id = e.schema_id
               AND o.slug = e.slug
               AND o.variant_key IS NULL
               AND o.slug_nested = e.slug_nested
               AND (NOT e.slug_nested OR o.parent_id IS NOT DISTINCT FROM e.parent_id)));
    PERFORM set_config('hatamoto.import', COALESCE(prev, ''), true);
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER site_config_language_retag AFTER UPDATE OF language ON public.hatamoto_site_config
    FOR EACH ROW WHEN (OLD.language IS DISTINCT FROM NEW.language)
    EXECUTE FUNCTION public.entity_locale_retag();

-- +goose Down
-- Structure only: the per-schema (not per-locale) slug rule returns, so this
-- fails if one slug now exists in two locales of a schema.
DROP TRIGGER IF EXISTS site_config_language_retag ON public.hatamoto_site_config;
DROP FUNCTION IF EXISTS public.entity_locale_retag();
DROP INDEX IF EXISTS public.uq_entities_schema_parent_slug;
DROP INDEX IF EXISTS public.uq_entities_schema_slug;
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS locale;
DROP FUNCTION IF EXISTS public.hatamoto_site_language();
CREATE UNIQUE INDEX uq_entities_schema_slug
    ON public.hatamoto_entities (schema_id, slug)
    WHERE variant_key IS NULL AND NOT slug_nested;
CREATE UNIQUE INDEX uq_entities_schema_parent_slug
    ON public.hatamoto_entities (schema_id, COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid), slug)
    WHERE variant_key IS NULL AND slug_nested;
