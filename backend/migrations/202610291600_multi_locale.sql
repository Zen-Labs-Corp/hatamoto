-- Several public locales per install. The site settings hold an ordered locale
-- list (BCP47 tag + URL segment), a prefix-all toggle and an x-default locale;
-- the default locale stays the existing `language` column. An entity can join a
-- translation group. An empty list is a single-locale install: nothing changes.
--
-- entity_locale_retag() is replaced: while the locale list is non-empty a
-- language change no longer re-tags entities (each locale keeps its content).
-- The previous release's binary never writes `locales`, so the new body behaves
-- exactly like the old one for it.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
ALTER TABLE hatamoto_site_config
    ADD COLUMN IF NOT EXISTS locales jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS prefix_all boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS x_default_locale text NOT NULL DEFAULT '';

ALTER TABLE hatamoto_entities ADD COLUMN IF NOT EXISTS translation_group uuid;
CREATE INDEX IF NOT EXISTS idx_entities_translation_group
    ON hatamoto_entities (translation_group) WHERE translation_group IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.entity_locale_retag() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    prev text := current_setting('hatamoto.import', true);
BEGIN
    IF jsonb_array_length(NEW.locales) > 0 THEN
        RETURN NULL;
    END IF;
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.entity_locale_retag() RETURNS trigger
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
DROP INDEX IF EXISTS public.idx_entities_translation_group;
ALTER TABLE hatamoto_entities DROP COLUMN IF EXISTS translation_group;
ALTER TABLE hatamoto_site_config
    DROP COLUMN IF EXISTS x_default_locale,
    DROP COLUMN IF EXISTS prefix_all,
    DROP COLUMN IF EXISTS locales;
