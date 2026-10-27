-- +goose Up
-- Variant rows. For databases that already applied the earlier form of 005
-- (non-partial unique index, no variant_key column). A NULL variant_key is a
-- base row: core owns slug uniqueness. A non-NULL variant_key marks a
-- plugin-owned variant sharing its slug with siblings; the owning plugin
-- enforces its own uniqueness. Idempotent for DBs where 005 already created
-- the partial form.
ALTER TABLE public.hatamoto_entities ADD COLUMN IF NOT EXISTS variant_key text;

DROP INDEX IF EXISTS public.uq_entities_schema_slug_locale;
CREATE UNIQUE INDEX uq_entities_schema_slug_locale
    ON public.hatamoto_entities (schema_id, slug, COALESCE(locale_code, ''))
    WHERE variant_key IS NULL;

CREATE INDEX IF NOT EXISTS idx_entities_variant_key
    ON public.hatamoto_entities USING btree (variant_key) WHERE (variant_key IS NOT NULL);

-- +goose Down
-- Best-effort, and deliberately keeps the variant_key column: 005 (which owns it)
-- creates it, and dropping it would destroy plugin-populated values. Only the
-- non-partial unique index is restored (fails if variant rows share a slug) and the
-- variant_key index is dropped.
DROP INDEX IF EXISTS public.idx_entities_variant_key;
DROP INDEX IF EXISTS public.uq_entities_schema_slug_locale;
CREATE UNIQUE INDEX uq_entities_schema_slug_locale
    ON public.hatamoto_entities (schema_id, slug, COALESCE(locale_code, ''));
