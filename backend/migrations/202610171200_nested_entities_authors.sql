-- +goose Up
-- v1-21-import-foundations, part 1: nested entity URLs, authors, import keys.

-- 1. Slug uniqueness follows the schema's URL pattern. A pattern with {path} or
--    {parent} puts the parent chain in the URL, so slugs only have to be unique
--    among siblings: /services/seo/local and /services/pt/local may both end in
--    "local". Every other pattern puts the slug alone (or under a fixed
--    prefix) in the URL, so two rows with one slug would shadow each other and
--    the old per-schema rule stays. A schema counts as nested when its pattern
--    or any override pattern has {path} or {parent}.
--
--    Nestedness lives on the schema, a unique index cannot look at another
--    table, so entities carry a copy (slug_nested): a BEFORE trigger fills it
--    on insert or when the row changes schema, and a trigger on the schema
--    refreshes it when the settings change. A pattern change that would make
--    slugs collide fails on the unique index; the service turns that into a
--    clear error. Variant rows still skip both indexes.
-- +goose StatementBegin
CREATE FUNCTION public.entity_schema_nested(settings jsonb) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    AS $$
    SELECT COALESCE(settings->>'url_pattern' LIKE '%{path}%' OR settings->>'url_pattern' LIKE '%{parent}%', false)
        OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements(CASE WHEN jsonb_typeof(settings->'overrides') = 'array' THEN settings->'overrides' ELSE '[]'::jsonb END) o
            WHERE o->>'url_pattern' LIKE '%{path}%' OR o->>'url_pattern' LIKE '%{parent}%')
$$;
-- +goose StatementEnd

ALTER TABLE public.hatamoto_entities ADD COLUMN slug_nested boolean DEFAULT false NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION public.entity_set_slug_nested() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    SELECT COALESCE(public.entity_schema_nested(s.settings), false) INTO NEW.slug_nested
    FROM public.hatamoto_entity_schemas s WHERE s.id = NEW.schema_id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER entities_slug_nested BEFORE INSERT OR UPDATE OF schema_id ON public.hatamoto_entities
    FOR EACH ROW EXECUTE FUNCTION public.entity_set_slug_nested();

-- Refreshing the copies is not an edit of the entities: keep updated_at (the
-- entities_updated_at trigger honours hatamoto.import) and restore the setting.
-- +goose StatementBegin
CREATE FUNCTION public.entity_schema_refresh_nested() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    nested boolean := public.entity_schema_nested(NEW.settings);
    prev text := current_setting('hatamoto.import', true);
BEGIN
    IF nested IS DISTINCT FROM public.entity_schema_nested(OLD.settings) THEN
        PERFORM set_config('hatamoto.import', 'on', true);
        UPDATE public.hatamoto_entities SET slug_nested = nested WHERE schema_id = NEW.id AND slug_nested IS DISTINCT FROM nested;
        PERFORM set_config('hatamoto.import', COALESCE(prev, ''), true);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER entity_schemas_refresh_nested AFTER UPDATE OF settings ON public.hatamoto_entity_schemas
    FOR EACH ROW EXECUTE FUNCTION public.entity_schema_refresh_nested();

DROP INDEX IF EXISTS public.uq_entities_schema_slug;
CREATE UNIQUE INDEX uq_entities_schema_slug
    ON public.hatamoto_entities (schema_id, slug)
    WHERE variant_key IS NULL AND NOT slug_nested;
CREATE UNIQUE INDEX uq_entities_schema_parent_slug
    ON public.hatamoto_entities (schema_id, COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid), slug)
    WHERE variant_key IS NULL AND slug_nested;

-- Public lookups ask for a slug first ("slug = $1 AND schema IN (...)", and
-- "parent_id IS NULL AND slug = $1" for the first step of a {path}); neither
-- unique index above leads with the slug. Without this one both are a
-- sequential scan (measured on 50k entities: 5.3 ms per lookup without it,
-- 0.17 ms with it; TestNestedEntitiesMigration_SlugLookupIndex checks the plan).
CREATE INDEX idx_entities_slug ON public.hatamoto_entities (slug, schema_id) WHERE variant_key IS NULL;

-- 2. Authors are content, not accounts: a byline with an optional bio and avatar.
CREATE TABLE public.hatamoto_authors (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug character varying(128) NOT NULL,
    name character varying(255) NOT NULL,
    bio text DEFAULT ''::text NOT NULL,
    avatar_media_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT hatamoto_authors_pkey PRIMARY KEY (id),
    CONSTRAINT hatamoto_authors_slug_key UNIQUE (slug),
    CONSTRAINT hatamoto_authors_avatar_fkey FOREIGN KEY (avatar_media_id) REFERENCES public.hatamoto_media(id) ON DELETE SET NULL
);

ALTER TABLE public.hatamoto_entities ADD COLUMN author_id uuid;
ALTER TABLE public.hatamoto_entities
    ADD CONSTRAINT hatamoto_entities_author_fkey FOREIGN KEY (author_id) REFERENCES public.hatamoto_authors(id) ON DELETE SET NULL;
CREATE INDEX idx_entities_author ON public.hatamoto_entities USING btree (author_id) WHERE (author_id IS NOT NULL);

-- 3. Existing bylines (attributes.author, else attributes.author_name) become
--    author rows in the next migration (202610171205), which is written in Go
--    so it can use the very Slugify the rest of the code uses.

-- 4. Import keys: one entity per (key), so a re-run updates instead of duplicating.
CREATE UNIQUE INDEX uq_entities_import_key
    ON public.hatamoto_entities ((metadata->>'import_key'))
    WHERE metadata ? 'import_key';

-- 5. An import sets updated_at itself: it runs under SET LOCAL hatamoto.import
--    = 'on', which the trigger honours by leaving updated_at alone.
DROP TRIGGER IF EXISTS entities_updated_at ON public.hatamoto_entities;
CREATE TRIGGER entities_updated_at BEFORE UPDATE ON public.hatamoto_entities
    FOR EACH ROW WHEN (current_setting('hatamoto.import', true) IS DISTINCT FROM 'on')
    EXECUTE FUNCTION public.update_updated_at();

-- 6. Flag the rows of schemas that are already nested (after the trigger above,
--    so updated_at stays). They moved from the flat index to the sibling one.
SELECT set_config('hatamoto.import', 'on', true);
UPDATE public.hatamoto_entities e SET slug_nested = true
FROM public.hatamoto_entity_schemas s
WHERE s.id = e.schema_id AND public.entity_schema_nested(s.settings);
SELECT set_config('hatamoto.import', '', true);

-- +goose Down
-- Structure only: author rows are dropped (the plain-text bylines in
-- attributes remain), and the per-schema slug rule returns (fails if siblings
-- under different parents now share a slug).
DROP TRIGGER IF EXISTS entities_updated_at ON public.hatamoto_entities;
CREATE TRIGGER entities_updated_at BEFORE UPDATE ON public.hatamoto_entities
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();
DROP INDEX IF EXISTS public.uq_entities_import_key;
DROP INDEX IF EXISTS public.idx_entities_author;
ALTER TABLE public.hatamoto_entities DROP CONSTRAINT IF EXISTS hatamoto_entities_author_fkey;
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS author_id;
DROP TABLE IF EXISTS public.hatamoto_authors;
DROP INDEX IF EXISTS public.uq_entities_schema_parent_slug;
DROP INDEX IF EXISTS public.uq_entities_schema_slug;
DROP INDEX IF EXISTS public.idx_entities_slug;
DROP TRIGGER IF EXISTS entity_schemas_refresh_nested ON public.hatamoto_entity_schemas;
DROP TRIGGER IF EXISTS entities_slug_nested ON public.hatamoto_entities;
DROP FUNCTION IF EXISTS public.entity_schema_refresh_nested();
DROP FUNCTION IF EXISTS public.entity_set_slug_nested();
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS slug_nested;
DROP FUNCTION IF EXISTS public.entity_schema_nested(jsonb);
CREATE UNIQUE INDEX uq_entities_schema_slug
    ON public.hatamoto_entities (schema_id, slug)
    WHERE variant_key IS NULL;
