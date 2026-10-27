-- +goose Up
-- v1-21-import-foundations, part 2: hierarchical terms and custom term kinds.
ALTER TABLE public.hatamoto_terms
    ADD COLUMN parent_id uuid,
    ADD COLUMN description text DEFAULT ''::text NOT NULL;
ALTER TABLE public.hatamoto_terms
    ADD CONSTRAINT hatamoto_terms_parent_fkey FOREIGN KEY (parent_id) REFERENCES public.hatamoto_terms(id) ON DELETE SET NULL;
CREATE INDEX idx_hatamoto_terms_parent ON public.hatamoto_terms USING btree (parent_id) WHERE (parent_id IS NOT NULL);

-- Kinds are free: any lower-case word (a custom taxonomy such as "genre").
-- The old fixed list becomes a format check; tag and category still satisfy it.
ALTER TABLE public.hatamoto_terms DROP CONSTRAINT hatamoto_terms_kind_check;
ALTER TABLE public.hatamoto_terms
    ADD CONSTRAINT hatamoto_terms_kind_check CHECK (((kind)::text ~ '^[a-z][a-z0-9_-]{0,15}$'));

-- +goose Down
-- Terms of a custom kind cannot satisfy the old list: they are deleted.
DELETE FROM public.hatamoto_terms WHERE kind NOT IN ('tag', 'category');
ALTER TABLE public.hatamoto_terms DROP CONSTRAINT hatamoto_terms_kind_check;
ALTER TABLE public.hatamoto_terms
    ADD CONSTRAINT hatamoto_terms_kind_check CHECK (((kind)::text = ANY (ARRAY[('tag'::character varying)::text, ('category'::character varying)::text])));
DROP INDEX IF EXISTS public.idx_hatamoto_terms_parent;
ALTER TABLE public.hatamoto_terms DROP CONSTRAINT IF EXISTS hatamoto_terms_parent_fkey;
ALTER TABLE public.hatamoto_terms DROP COLUMN IF EXISTS parent_id, DROP COLUMN IF EXISTS description;
