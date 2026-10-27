-- +goose Up
-- Remove the curated related-blocks feature from core. Manually-curated
-- sidebar/footer/CTA cards were marketing-flavoured and overlapped with the
-- automated internal-linking system (linking/v2). The table is dropped from
-- the baseline for fresh installs; this migration removes it from databases
-- that were already migrated. CASCADE clears the FKs/indexes with it.
DROP TABLE IF EXISTS public.hatamoto_related_blocks CASCADE;

-- +goose Down
-- Recreate the table so a rollback restores the pre-drop schema. Historical
-- row data is not restored.
CREATE TABLE IF NOT EXISTS public.hatamoto_related_blocks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    source_entity_id uuid NOT NULL,
    target_entity_id uuid NOT NULL,
    target_locale_code character varying(8),
    placement character varying(30) NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT hatamoto_related_blocks_pkey PRIMARY KEY (id),
    CONSTRAINT hatamoto_related_blocks_unique UNIQUE (source_entity_id, target_entity_id, placement),
    CONSTRAINT hatamoto_related_blocks_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.hatamoto_users(id),
    CONSTRAINT hatamoto_related_blocks_source_entity_id_fkey FOREIGN KEY (source_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE,
    CONSTRAINT hatamoto_related_blocks_target_entity_id_fkey FOREIGN KEY (target_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_related_blocks_source ON public.hatamoto_related_blocks USING btree (source_entity_id);
CREATE INDEX IF NOT EXISTS idx_related_blocks_target ON public.hatamoto_related_blocks USING btree (target_entity_id);
