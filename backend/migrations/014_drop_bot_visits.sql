-- +goose Up
-- The legacy bot-visit aggregates were copied into hatamoto_agent_daily by
-- migration 010 and the table has not been written since Phase 1. The
-- /bot-visits* read routes and the bottrack package are gone, so drop it.
-- Legacy databases still carry the table until this runs, which is why
-- adopt/baseline.catalog.tsv (a 001_baseline snapshot) keeps describing it.
DROP TABLE IF EXISTS public.hatamoto_bot_visits;

-- +goose Down
-- Recreates the empty table and its indexes (shape from 001_baseline); the
-- dropped rows are not restored (their aggregates live on in hatamoto_agent_daily).
CREATE TABLE IF NOT EXISTS public.hatamoto_bot_visits (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    bot_name text NOT NULL,
    bot_category text NOT NULL,
    path text NOT NULL,
    hit_count integer DEFAULT 1 NOT NULL,
    visit_date date DEFAULT CURRENT_DATE NOT NULL,
    last_user_agent text,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT hatamoto_bot_visits_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX IF NOT EXISTS hatamoto_bot_visits_upsert
    ON public.hatamoto_bot_visits USING btree (bot_name, path, visit_date);
