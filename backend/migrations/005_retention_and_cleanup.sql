-- +goose Up
-- Retention/cleanup schema changes. All additive-or-drop; 001_baseline.sql is
-- never edited. See .pi/plans/2026-09-29-db-retention/ for the analysis behind
-- each drop (verified no live Go references).

-- 1. Drop confirmed-redundant duplicate indexes.
--    - idx_sessions_expires_at duplicates idx_sessions_expires (identical btree(expires_at)).
--    - idx_sessions_user_id is a partial subset of the full idx_sessions_user.
--    - idx_entity_versions_entity is covered by the composite idx_entity_versions_created
--      (leading column entity_id).
--    NOTE: the idx_entities_url / idx_entities_attributes GIN pair is deliberately
--    NOT dropped here (different opclasses; verify idx_scan on a live DB first).
DROP INDEX IF EXISTS public.idx_sessions_expires_at;
DROP INDEX IF EXISTS public.idx_sessions_user_id;
DROP INDEX IF EXISTS public.idx_entity_versions_entity;

-- 2. Add missing foreign-key indexes (unindexed FK columns → slow cascade checks
--    when the referenced row is deleted). Partial where the column is nullable.
CREATE INDEX IF NOT EXISTS idx_media_uploaded_by
    ON public.hatamoto_media USING btree (uploaded_by) WHERE (uploaded_by IS NOT NULL);
CREATE INDEX IF NOT EXISTS idx_404_log_redirect
    ON public.hatamoto_404_log USING btree (redirect_id) WHERE (redirect_id IS NOT NULL);
CREATE INDEX IF NOT EXISTS idx_entity_versions_changed_by
    ON public.hatamoto_entity_versions USING btree (changed_by) WHERE (changed_by IS NOT NULL);

-- 3. Enforce entity uniqueness per (schema_id, slug, locale_code) for base rows.
--    Uses COALESCE(locale_code,'') because core entities have locale_code = NULL
--    and NULLs count as DISTINCT in a plain UNIQUE, which would let duplicate
--    (schema_id, slug) rows through. A functional unique index closes that.
--    Variant rows: variant_key IS NULL means a base row (core owns slug
--    uniqueness); a non-NULL variant_key marks a plugin-owned variant that may
--    share its slug with sibling rows (the owning plugin enforces its own
--    uniqueness), so the index is partial and skips variants.
--    PRECHECK on non-fresh DBs (this fails if duplicate base rows already exist):
--      SELECT schema_id, slug, COALESCE(locale_code,''), COUNT(*)
--      FROM hatamoto_entities WHERE variant_key IS NULL GROUP BY 1,2,3 HAVING COUNT(*) > 1;
--    (On a DB without the column, set variant_key for variant rows first, e.g. with
--    `adopt-legacy-db.sh --pre-sql`; see backend/migrations/ADOPTING_LEGACY_DB.md.)
--    (GetBySlug currently assumes global slug uniqueness; a scoped lookup variant
--    is a follow-up before slugs may legitimately be reused across schemas/locales.)
ALTER TABLE public.hatamoto_entities ADD COLUMN IF NOT EXISTS variant_key text;
CREATE UNIQUE INDEX IF NOT EXISTS uq_entities_schema_slug_locale
    ON public.hatamoto_entities (schema_id, slug, COALESCE(locale_code, ''))
    WHERE variant_key IS NULL;

-- 4. Drop dead tables (zero live Go references; api_key_usage's LogUsage/
--    GetUsageStats methods are removed in the same PR).
DROP TABLE IF EXISTS public.hatamoto_api_key_usage;
DROP TABLE IF EXISTS public.hatamoto_secrets;

-- NOTE: the unused pgvector extension is NOT dropped here. DROP EXTENSION
-- requires ownership/superuser, but goose runs as the non-superuser app role
-- (per CLAUDE.md), which would fail the whole transactional migration. Drop it
-- manually as the extension owner: `DROP EXTENSION vector;` (no column or Go
-- code uses it). Tracked as a follow-up.

-- +goose Down
-- Reverse the schema changes. Historical row data is NOT restored (tables come
-- back empty), matching the baseline-cutover convention. Best-effort structure.
CREATE TABLE IF NOT EXISTS public.hatamoto_secrets (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    key character varying(255) NOT NULL,
    encrypted_value bytea NOT NULL,
    nonce bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT hatamoto_secrets_pkey PRIMARY KEY (id),
    CONSTRAINT hatamoto_secrets_key_key UNIQUE (key)
);

CREATE TABLE IF NOT EXISTS public.hatamoto_api_key_usage (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    api_key_id uuid NOT NULL,
    endpoint character varying(255) NOT NULL,
    method character varying(10) NOT NULL,
    status_code integer,
    response_time_ms integer,
    ip_address character varying(45),
    user_agent text,
    created_at timestamp with time zone DEFAULT now(),
    CONSTRAINT hatamoto_api_key_usage_pkey PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_created
    ON public.hatamoto_api_key_usage USING btree (api_key_id, created_at DESC);
ALTER TABLE ONLY public.hatamoto_api_key_usage
    ADD CONSTRAINT fk_api_key_usage_key FOREIGN KEY (api_key_id)
    REFERENCES public.hatamoto_api_keys(id) ON DELETE CASCADE;

DROP INDEX IF EXISTS public.uq_entities_schema_slug_locale;
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS variant_key;

DROP INDEX IF EXISTS public.idx_entity_versions_changed_by;
DROP INDEX IF EXISTS public.idx_404_log_redirect;
DROP INDEX IF EXISTS public.idx_media_uploaded_by;

CREATE INDEX IF NOT EXISTS idx_entity_versions_entity
    ON public.hatamoto_entity_versions USING btree (entity_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id
    ON public.hatamoto_sessions USING btree (user_id) WHERE (user_id IS NOT NULL);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at
    ON public.hatamoto_sessions USING btree (expires_at);
