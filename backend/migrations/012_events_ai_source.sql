-- +goose Up
-- AI assistant that referred the visit (chatgpt, claude, ...). Set only on
-- server-published ai_referred_visit events; NULL everywhere else.
-- ADD COLUMN on the partitioned parent propagates to every partition.
ALTER TABLE public.hatamoto_events ADD COLUMN IF NOT EXISTS ai_source text;

-- Same style as idx_hatamoto_events_lead_time (001): created ON ONLY the
-- parent via CREATE INDEX on a partitioned table, which cascades to partitions
-- (existing and future). Partial so the index stays tiny.
CREATE INDEX IF NOT EXISTS idx_hatamoto_events_ai_source_time
    ON public.hatamoto_events USING btree (ai_source, occurred_at DESC)
    WHERE ai_source IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS public.idx_hatamoto_events_ai_source_time;
ALTER TABLE public.hatamoto_events DROP COLUMN IF EXISTS ai_source;
