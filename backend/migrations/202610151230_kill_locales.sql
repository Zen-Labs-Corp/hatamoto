-- +goose Up
-- One site, one language (ROADMAP v1-09). Removes every locale concept:
--   * site config gains `language`, `phone`, `business_hours` and
--     `legacy_wp_redirects`; the default locale's contact details are copied
--     into it once, then the per-locale map is dropped;
--   * every previously active locale code becomes a 301 `/<code>/**` -> `/$1`
--     plus `/<code>` -> `/` (each skipped when a redirect already has that
--     source; `/<code>` also when a page owns it). Existing redirect targets
--     lose their `{locale}` / `/<code>` segment, and an inserted rule that
--     would close a loop is switched off;
--   * rows that collide on a slug once the locale is gone keep the best row
--     (published > scheduled > draft, then the default locale, then neutral);
--     the rest become slug-<code>, archived when they were live;
--   * published default-locale overrides are merged into the entity, and
--     everything moved or dropped is logged in hatamoto_migration_notes
--     (and as NOTICEs) so the owner can see what changed;
--   * schema URL patterns lose the `{locale}` token and keep serving the URLs
--     they served before (post schemas keep `/post/{slug}`; the new default for
--     fresh installs is `/blog/{slug}`);
--   * hatamoto_locales, hatamoto_entity_locale_overrides, entities.locale_code,
--     entities.locale_scope, attributes.enabled_locales and the link index's
--     target_locale_code are dropped.

-- 0. What this migration moved, archived or switched off, for the owner.
CREATE TABLE IF NOT EXISTS public.hatamoto_migration_notes (
    id         uuid        NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    migration  text        NOT NULL,
    kind       text        NOT NULL,
    entity_id  uuid,
    message    text        NOT NULL,
    details    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- 1. Site config: new columns, one-off copy of the default locale's settings.
ALTER TABLE public.hatamoto_site_config
    ADD COLUMN IF NOT EXISTS language text NOT NULL DEFAULT 'en',
    ADD COLUMN IF NOT EXISTS phone text,
    ADD COLUMN IF NOT EXISTS business_hours text,
    ADD COLUMN IF NOT EXISTS legacy_wp_redirects boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
DO $$
DECLARE
    def record;
    entry jsonb;
    addr jsonb;
    lang text;
    m text[];
BEGIN
    IF to_regclass('public.hatamoto_locales') IS NULL THEN
        RETURN;
    END IF;
    SELECT * INTO def FROM public.hatamoto_locales WHERE is_default LIMIT 1;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    -- Canonical BCP 47 (en-AU), the same shape the settings PATCH accepts.
    m := regexp_match(replace(COALESCE(def.language_code, ''), '_', '-'), '^([A-Za-z]{2,3})(?:-([A-Za-z]{2}))?$');
    IF m IS NULL THEN
        lang := 'en';
        RAISE NOTICE 'default locale language % is not a BCP 47 tag, using en', def.language_code;
    ELSE
        lang := lower(m[1]) || COALESCE('-' || upper(m[2]), '');
    END IF;

    SELECT COALESCE(postal_addresses -> def.code, '{}'::jsonb) INTO entry FROM public.hatamoto_site_config;
    addr := jsonb_strip_nulls(jsonb_build_object(
        'street',      NULLIF(entry ->> 'street', ''),
        'city',        NULLIF(entry ->> 'city', ''),
        'state',       NULLIF(entry ->> 'state', ''),
        'postal_code', NULLIF(entry ->> 'postal_code', ''),
        'country',     NULLIF(entry ->> 'country', '')));

    UPDATE public.hatamoto_site_config SET
        language       = lang,
        timezone       = COALESCE(NULLIF(timezone, ''), NULLIF(def.timezone, '')),
        phone          = COALESCE(NULLIF(phone, ''), NULLIF(entry ->> 'phone', '')),
        business_hours = COALESCE(NULLIF(business_hours, ''), NULLIF(entry ->> 'business_hours', '')),
        postal_address = CASE
            WHEN (postal_address IS NULL OR postal_address IN ('null'::jsonb, '{}'::jsonb)) AND addr <> '{}'::jsonb
            THEN addr ELSE postal_address END;
END
$$;
-- +goose StatementEnd

ALTER TABLE public.hatamoto_site_config DROP COLUMN IF EXISTS postal_addresses;

-- 3. Schema URL patterns.
-- +goose StatementBegin
DO $$
DECLARE
    sch record;
    st jsonb;
    ovr jsonb;
    owner_slug text;
    family text[] := ARRAY['post', 'blog', 'article', 'blog_post'];
    claimed text[];
    aliases jsonb;
BEGIN
    -- 3a. Strip the {locale} segment from explicit patterns and overrides.
    FOR sch IN SELECT id, settings FROM public.hatamoto_entity_schemas
               WHERE settings::text LIKE '%{locale}%' LOOP
        st := sch.settings;
        IF st ? 'url_pattern' THEN
            st := jsonb_set(st, '{url_pattern}', to_jsonb(replace(st ->> 'url_pattern', '/{locale}', '')));
        END IF;
        IF jsonb_typeof(st -> 'overrides') = 'array' THEN
            SELECT COALESCE(jsonb_agg(
                       CASE WHEN o ? 'url_pattern'
                            THEN jsonb_set(o, '{url_pattern}', to_jsonb(replace(o ->> 'url_pattern', '/{locale}', '')))
                            ELSE o END), '[]'::jsonb)
              INTO ovr FROM jsonb_array_elements(st -> 'overrides') o;
            st := jsonb_set(st, '{overrides}', ovr);
        END IF;
        UPDATE public.hatamoto_entity_schemas SET settings = st WHERE id = sch.id;
    END LOOP;

    -- 3b. An install that already has post content keeps /post/{slug} (the
    -- built-in default for new installs is /blog/{slug}): pin the pattern on
    -- the schema that owned the shared post defaults.
    SELECT COALESCE(array_agg(DISTINCT x), ARRAY[]::text[]) INTO claimed FROM (
        SELECT s.slug AS x FROM public.hatamoto_entity_schemas s WHERE COALESCE(s.settings ->> 'url_pattern', '') <> ''
        UNION
        SELECT a FROM public.hatamoto_entity_schemas s,
             jsonb_array_elements_text(CASE WHEN jsonb_typeof(s.settings -> 'aliases') = 'array' THEN s.settings -> 'aliases' ELSE '[]'::jsonb END) a
         WHERE COALESCE(s.settings ->> 'url_pattern', '') <> ''
    ) c;

    -- The owner is the first family member in the runtime's order.
    SELECT s.slug INTO owner_slug
      FROM public.hatamoto_entity_schemas s
     WHERE s.slug = ANY (family)
       AND COALESCE(s.settings ->> 'url_pattern', '') = ''
       AND NOT (s.slug = ANY (claimed))
     ORDER BY array_position(family, s.slug)
     LIMIT 1;

    IF owner_slug IS NOT NULL AND EXISTS (
        SELECT 1 FROM public.hatamoto_entities e
          JOIN public.hatamoto_entity_schemas s ON s.id = e.schema_id
         WHERE s.slug = ANY (family) AND NOT (s.slug = ANY (claimed))
    ) THEN
        SELECT COALESCE(jsonb_agg(f), '[]'::jsonb) INTO aliases
          FROM unnest(family) f WHERE f <> owner_slug AND NOT (f = ANY (claimed));
        UPDATE public.hatamoto_entity_schemas SET settings =
            jsonb_build_object('url_pattern', '/post/{slug}', 'archive_path', '/post', 'sitemap_group', 'post')
            || CASE WHEN jsonb_array_length(aliases) > 0 THEN jsonb_build_object('aliases', aliases) ELSE '{}'::jsonb END
            || COALESCE(settings, '{}'::jsonb)
         WHERE slug = owner_slug;
    END IF;
END
$$;
-- +goose StatementEnd

-- 4. Entity rows. Rows of retired locales stop being served (as before); rows
-- that collide on (schema, slug) once the locale is gone keep one winner and
-- the others are renamed to slug-<code> and archived when they were live.
-- Winner: published > scheduled > draft, then the default locale's row (what
-- /<default>/x served), then the locale-neutral row, then other locales.
-- +goose StatementBegin
DO $$
DECLARE
    def_code text;
    r record;
    base text;
    cand text;
    n int;
    new_status text;
BEGIN
    IF to_regclass('public.hatamoto_locales') IS NULL THEN
        RETURN;
    END IF;
    SELECT code INTO def_code FROM public.hatamoto_locales WHERE is_default LIMIT 1;

    FOR r IN
        SELECT e.id, e.slug, e.locale_code
          FROM public.hatamoto_entities e
         WHERE e.locale_code IS NOT NULL AND e.status IN ('published', 'scheduled')
           AND NOT EXISTS (SELECT 1 FROM public.hatamoto_locales l WHERE l.code = e.locale_code AND l.is_active)
    LOOP
        UPDATE public.hatamoto_entities SET status = 'archived' WHERE id = r.id;
        INSERT INTO public.hatamoto_migration_notes (migration, kind, entity_id, message, details)
        VALUES ('202610151230_kill_locales', 'entity_archived_inactive_locale', r.id,
                format('entity %s (%s) belonged to the inactive locale %s and was archived', r.id, r.slug, r.locale_code),
                jsonb_build_object('slug', r.slug, 'locale', r.locale_code));
        RAISE NOTICE 'entity % (%) belonged to the inactive locale % and was archived', r.id, r.slug, r.locale_code;
    END LOOP;

    FOR r IN
        SELECT * FROM (
            SELECT e.id, e.schema_id, e.slug, e.locale_code, e.status,
                   row_number() OVER (
                       PARTITION BY e.schema_id, e.slug
                       ORDER BY CASE e.status WHEN 'published' THEN 0 WHEN 'scheduled' THEN 1 WHEN 'draft' THEN 2 ELSE 3 END,
                                CASE WHEN e.locale_code IS NOT DISTINCT FROM def_code THEN 0
                                     WHEN e.locale_code IS NULL THEN 1 ELSE 2 END,
                                e.locale_code, e.created_at, e.id) AS rn,
                   first_value(e.id) OVER (
                       PARTITION BY e.schema_id, e.slug
                       ORDER BY CASE e.status WHEN 'published' THEN 0 WHEN 'scheduled' THEN 1 WHEN 'draft' THEN 2 ELSE 3 END,
                                CASE WHEN e.locale_code IS NOT DISTINCT FROM def_code THEN 0
                                     WHEN e.locale_code IS NULL THEN 1 ELSE 2 END,
                                e.locale_code, e.created_at, e.id) AS winner_id
              FROM public.hatamoto_entities e
             WHERE e.variant_key IS NULL
        ) ranked
         WHERE rn > 1
         ORDER BY schema_id, slug, rn
    LOOP
        base := r.slug || '-' || COALESCE(r.locale_code, 'neutral');
        cand := base;
        n := 2;
        WHILE EXISTS (SELECT 1 FROM public.hatamoto_entities x
                       WHERE x.schema_id = r.schema_id AND x.slug = cand AND x.variant_key IS NULL AND x.id <> r.id) LOOP
            cand := base || '-' || n;
            n := n + 1;
        END LOOP;
        new_status := CASE WHEN r.status IN ('published', 'scheduled') THEN 'archived' ELSE r.status END;
        UPDATE public.hatamoto_entities SET slug = cand, status = new_status WHERE id = r.id;
        INSERT INTO public.hatamoto_migration_notes (migration, kind, entity_id, message, details)
        VALUES ('202610151230_kill_locales', 'entity_slug_collision', r.id,
                format('entity %s: slug %s -> %s, status %s -> %s (kept: %s)', r.id, r.slug, cand, r.status, new_status, r.winner_id),
                jsonb_build_object('old_slug', r.slug, 'new_slug', cand, 'locale', r.locale_code,
                                   'old_status', r.status, 'new_status', new_status, 'kept_entity_id', r.winner_id));
        RAISE NOTICE 'entity % (locale %): slug % -> %, status % -> % (kept entity %)',
            r.id, COALESCE(r.locale_code, 'neutral'), r.slug, cand, r.status, new_status, r.winner_id;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- 5. Locale overrides. The default locale's published overrides were applied
-- to what the primary site served, so they are merged into the entity's
-- attributes (SEO fields land in meta_title, meta_description, og_image,
-- no_index). Every override that carried content is also kept verbatim in
-- hatamoto_migration_notes before the table is dropped. A canonical_url or a
-- slug_override has no place in the new model: it is kept in the notes only.
-- +goose StatementBegin
DO $$
DECLARE
    def_code text;
    o record;
    seo jsonb;
    merged jsonb;
    n_merged int := 0;
    n_noted int := 0;
BEGIN
    IF to_regclass('public.hatamoto_entity_locale_overrides') IS NULL OR to_regclass('public.hatamoto_locales') IS NULL THEN
        RETURN;
    END IF;
    SELECT code INTO def_code FROM public.hatamoto_locales WHERE is_default LIMIT 1;

    FOR o IN SELECT * FROM public.hatamoto_entity_locale_overrides ORDER BY entity_id, locale_code LOOP
        CONTINUE WHEN o.attribute_overrides = '{}'::jsonb AND COALESCE(o.seo_overrides, '{}'::jsonb) = '{}'::jsonb
                  AND COALESCE(o.metadata_overrides, '{}'::jsonb) = '{}'::jsonb AND o.slug_override IS NULL;

        IF o.locale_code IS NOT DISTINCT FROM def_code AND o.status <> 'draft' THEN
            seo := COALESCE(o.seo_overrides, '{}'::jsonb);
            merged := o.attribute_overrides
                || jsonb_strip_nulls(jsonb_build_object(
                       'meta_title',       NULLIF(seo ->> 'meta_title', ''),
                       'meta_description', NULLIF(seo ->> 'meta_description', ''),
                       'og_image',         NULLIF(seo ->> 'og_image', ''),
                       'no_index',         CASE WHEN seo ->> 'no_index' = 'true' THEN to_jsonb(true) END));
            UPDATE public.hatamoto_entities SET attributes = COALESCE(attributes, '{}'::jsonb) || merged WHERE id = o.entity_id;
            n_merged := n_merged + 1;
            INSERT INTO public.hatamoto_migration_notes (migration, kind, entity_id, message, details)
            VALUES ('202610151230_kill_locales', 'override_merged_default_locale', o.entity_id,
                    format('default-locale override of entity %s merged into its attributes', o.entity_id),
                    jsonb_build_object('locale', o.locale_code, 'attribute_overrides', o.attribute_overrides,
                                       'seo_overrides', o.seo_overrides, 'slug_override', o.slug_override));
        ELSE
            n_noted := n_noted + 1;
            INSERT INTO public.hatamoto_migration_notes (migration, kind, entity_id, message, details)
            VALUES ('202610151230_kill_locales', 'override_dropped', o.entity_id,
                    format('%s override of entity %s was dropped (kept here)', o.locale_code, o.entity_id),
                    jsonb_build_object('locale', o.locale_code, 'status', o.status, 'attribute_overrides', o.attribute_overrides,
                                       'metadata_overrides', o.metadata_overrides, 'seo_overrides', o.seo_overrides,
                                       'slug_override', o.slug_override));
        END IF;
    END LOOP;
    IF n_merged + n_noted > 0 THEN
        RAISE NOTICE 'locale overrides: % default-locale merged into entities, % dropped (kept in hatamoto_migration_notes)', n_merged, n_noted;
    END IF;
END
$$;
-- +goose StatementEnd

-- 6. Redirects. Existing targets lose their locale segment ({locale} and
-- /<code>), then the old `/<code>/...` URLs get their redirects, then loops
-- are looked for: an inserted rule that closes a loop is switched off; a
-- rewritten rule that now points at itself (/seo -> /au/seo became
-- /seo -> /seo) or back into a loop is switched off too, since it can only
-- loop. Owner rules are otherwise never touched or deactivated.
-- +goose StatementBegin
DO $$
DECLARE
    codes text;
    loc record;
    rd record;
    t text;
    rewritten uuid[] := ARRAY[]::uuid[];
    inserted jsonb := '{}'::jsonb;        -- code -> ARRAY of inserted rule ids
    cur text;
    nxt text;
    seen text[];
    hops int;
    in_loop boolean;
    nxt_rule record;
    victim uuid;
    code_hit text;
BEGIN
    IF to_regclass('public.hatamoto_locales') IS NULL THEN
        RETURN;
    END IF;
    SELECT string_agg(code, '|') INTO codes FROM public.hatamoto_locales;

    -- 6a. Rewrite the targets of existing rules.
    FOR rd IN SELECT id, source_path, target_url FROM public.hatamoto_redirects
               WHERE target_url LIKE '%{locale}%'
                  OR (codes IS NOT NULL AND target_url ~ ('^/(' || codes || ')(/|$|\?|#)')) LOOP
        t := replace(rd.target_url, '/{locale}/', '/');
        t := regexp_replace(t, '/\{locale\}($|[?#])', '/\1');
        IF codes IS NOT NULL AND t ~ '^/' THEN
            t := regexp_replace(t, '^/(' || codes || ')(?=/|$|\?|#)', '');
            IF t = '' OR t ~ '^[?#]' THEN t := '/' || t; END IF;
        END IF;
        IF t <> rd.target_url THEN
            UPDATE public.hatamoto_redirects SET target_url = t, updated_at = now() WHERE id = rd.id;
            rewritten := rewritten || rd.id;
            INSERT INTO public.hatamoto_migration_notes (migration, kind, message, details)
            VALUES ('202610151230_kill_locales', 'redirect_target_rewritten',
                    format('redirect %s: target %s -> %s', rd.source_path, rd.target_url, t),
                    jsonb_build_object('redirect_id', rd.id, 'source', rd.source_path, 'old_target', rd.target_url, 'new_target', t));
            RAISE NOTICE 'redirect %: target % -> %', rd.source_path, rd.target_url, t;
        END IF;
    END LOOP;

    -- 6b. Old `/<code>/...` URLs. Each source is skipped when a redirect
    -- already has it; `/<code>` also when a published page lives there.
    FOR loc IN SELECT code FROM public.hatamoto_locales WHERE is_active ORDER BY code LOOP
        IF EXISTS (SELECT 1 FROM public.hatamoto_redirects WHERE source_path = '/' || loc.code) THEN
            RAISE NOTICE 'locale %: a redirect already owns /%, not adding it', loc.code, loc.code;
        ELSIF EXISTS (SELECT 1 FROM public.hatamoto_entities e
                        JOIN public.hatamoto_entity_schemas s ON s.id = e.schema_id
                       WHERE e.slug = loc.code AND e.variant_key IS NULL AND e.status = 'published'
                         AND COALESCE(s.settings ->> 'url_pattern', '/{slug}') IN ('', '/{slug}')
                         AND s.slug <> ALL (ARRAY['post', 'blog', 'article', 'blog_post'])) THEN
            RAISE NOTICE 'locale %: a page lives at /%, not adding its redirect', loc.code, loc.code;
        ELSE
            INSERT INTO public.hatamoto_redirects (source_path, target_url, status_code, source_tag)
            VALUES ('/' || loc.code, '/', 301, 'v1-09-kill-locales');
            inserted := jsonb_set(inserted, ARRAY[loc.code], COALESCE(inserted -> loc.code, '[]'::jsonb) || to_jsonb('/' || loc.code));
        END IF;
        IF EXISTS (SELECT 1 FROM public.hatamoto_redirects WHERE source_path = '/' || loc.code || '/**') THEN
            RAISE NOTICE 'locale %: a redirect already owns /%/**, not adding it', loc.code, loc.code;
        ELSE
            INSERT INTO public.hatamoto_redirects (source_path, target_url, status_code, source_tag)
            VALUES ('/' || loc.code || '/**', '/$1', 301, 'v1-09-kill-locales');
            inserted := jsonb_set(inserted, ARRAY[loc.code], COALESCE(inserted -> loc.code, '[]'::jsonb) || to_jsonb('/' || loc.code || '/**'));
        END IF;
    END LOOP;

    -- 6c. Loops. Follow each rewritten rule through the exact rules and the
    -- inserted prefix rules; a repeated path is a loop.
    FOR rd IN SELECT id, source_path, target_url FROM public.hatamoto_redirects
               WHERE id = ANY (rewritten) AND is_active AND target_url ~ '^/' LOOP
        cur := regexp_replace(rd.target_url, '[?#].*$', '');
        seen := ARRAY[rd.source_path];
        hops := 0;
        in_loop := false;
        victim := NULL;
        WHILE hops < 12 LOOP
            hops := hops + 1;
            IF cur = ANY (seen) THEN in_loop := true; EXIT; END IF;
            seen := seen || cur;
            SELECT id, target_url INTO nxt_rule FROM public.hatamoto_redirects
             WHERE source_path = cur AND is_active LIMIT 1;
            IF FOUND THEN
                IF nxt_rule.target_url !~ '^/' THEN EXIT; END IF;
                nxt := regexp_replace(nxt_rule.target_url, '[?#].*$', '');
                IF EXISTS (SELECT 1 FROM public.hatamoto_redirects WHERE id = nxt_rule.id AND source_tag = 'v1-09-kill-locales') THEN
                    victim := nxt_rule.id;
                END IF;
                cur := nxt;
                CONTINUE;
            END IF;
            code_hit := substring(cur from '^/([^/]+)(?:/|$)');
            SELECT id INTO nxt_rule FROM public.hatamoto_redirects
             WHERE source_tag = 'v1-09-kill-locales' AND is_active AND source_path = '/' || code_hit || '/**';
            IF FOUND THEN
                victim := nxt_rule.id;
                cur := '/' || COALESCE(substring(cur from '^/[^/]+/(.*)$'), '');
                CONTINUE;
            END IF;
            EXIT;
        END LOOP;
        IF in_loop THEN
            -- The inserted rule on the path is the one to switch off; with none
            -- on it, the rewritten rule itself only loops.
            victim := COALESCE(victim, rd.id);
            UPDATE public.hatamoto_redirects SET is_active = false, updated_at = now() WHERE id = victim;
            INSERT INTO public.hatamoto_migration_notes (migration, kind, message, details)
            VALUES ('202610151230_kill_locales', 'redirect_loop_deactivated',
                    format('redirect %s -> %s forms a loop; rule %s was switched off', rd.source_path, rd.target_url, victim),
                    jsonb_build_object('redirect_id', rd.id, 'source', rd.source_path, 'target', rd.target_url, 'deactivated_rule_id', victim));
            RAISE NOTICE 'redirect % -> % forms a loop; rule % was switched off', rd.source_path, rd.target_url, victim;
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- 7. Drop the locale tables, columns and indexes.
UPDATE public.hatamoto_entities SET attributes = attributes - 'enabled_locales' WHERE attributes ? 'enabled_locales';

DROP TABLE IF EXISTS public.hatamoto_entity_locale_overrides CASCADE;
DROP TABLE IF EXISTS public.hatamoto_locales CASCADE;

DROP INDEX IF EXISTS public.idx_entities_enabled_locales;
DROP INDEX IF EXISTS public.idx_entities_locale_code;
DROP INDEX IF EXISTS public.uq_entities_schema_slug_locale;
ALTER TABLE public.hatamoto_entities
    DROP COLUMN IF EXISTS locale_scope,
    DROP COLUMN IF EXISTS locale_code;
CREATE UNIQUE INDEX uq_entities_schema_slug
    ON public.hatamoto_entities (schema_id, slug)
    WHERE variant_key IS NULL;

DROP INDEX IF EXISTS public.idx_entity_link_index_target;
ALTER TABLE public.hatamoto_entity_link_index DROP COLUMN IF EXISTS target_locale_code;
CREATE INDEX idx_entity_link_index_target ON public.hatamoto_entity_link_index USING btree (target_entity_id);

-- +goose Down
-- Best-effort structural rollback: the locale tables and columns come back
-- empty; data is not restored (the redirects inserted by Up stay).
ALTER TABLE public.hatamoto_entity_link_index ADD COLUMN IF NOT EXISTS target_locale_code character varying(8);
DROP INDEX IF EXISTS public.idx_entity_link_index_target;
CREATE INDEX idx_entity_link_index_target ON public.hatamoto_entity_link_index USING btree (target_entity_id, target_locale_code);

DROP INDEX IF EXISTS public.uq_entities_schema_slug;
ALTER TABLE public.hatamoto_entities
    ADD COLUMN IF NOT EXISTS locale_scope character varying(20) DEFAULT 'locale_specific'::character varying,
    ADD COLUMN IF NOT EXISTS locale_code character varying(5);
CREATE UNIQUE INDEX uq_entities_schema_slug_locale
    ON public.hatamoto_entities (schema_id, slug, COALESCE(locale_code, ''))
    WHERE variant_key IS NULL;
CREATE INDEX idx_entities_locale_code ON public.hatamoto_entities USING btree (locale_code);

CREATE TABLE IF NOT EXISTS public.hatamoto_locales (
    code character varying(10) NOT NULL PRIMARY KEY,
    country_code character varying(2) NOT NULL,
    language_code character varying(5) NOT NULL,
    name character varying(100) NOT NULL,
    currency character varying(3),
    timezone character varying(50),
    phone_prefix character varying(10),
    is_active boolean DEFAULT true,
    display_order integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now(),
    is_default boolean DEFAULT false NOT NULL
);
CREATE TABLE IF NOT EXISTS public.hatamoto_entity_locale_overrides (
    id uuid DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    entity_id uuid NOT NULL REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE,
    locale_code character varying(10) NOT NULL REFERENCES public.hatamoto_locales(code),
    attribute_overrides jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_overrides jsonb DEFAULT '{}'::jsonb,
    seo_overrides jsonb DEFAULT '{}'::jsonb,
    slug_override character varying(255),
    status character varying(20) DEFAULT 'inherit'::character varying,
    version integer DEFAULT 1,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    CONSTRAINT uq_entity_locale UNIQUE (entity_id, locale_code)
);

ALTER TABLE public.hatamoto_site_config
    ADD COLUMN IF NOT EXISTS postal_addresses jsonb DEFAULT '{}'::jsonb NOT NULL,
    DROP COLUMN IF EXISTS language,
    DROP COLUMN IF EXISTS phone,
    DROP COLUMN IF EXISTS business_hours,
    DROP COLUMN IF EXISTS legacy_wp_redirects;
