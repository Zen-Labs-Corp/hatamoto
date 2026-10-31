-- +goose Up
-- First verified search engine crawl per page. A sibling of
-- hatamoto_agent_first_crawl, which stays AI only (one row per entity): this
-- one is keyed by entity and agent so each search engine has its own first
-- crawl, which makes publish -> first Googlebot crawl measurable.
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_search_first_crawl (
    entity_id    uuid NOT NULL REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE,
    agent_id     text NOT NULL,
    first_at     timestamp with time zone NOT NULL,
    published_at timestamp with time zone,
    PRIMARY KEY (entity_id, agent_id)
);
CREATE INDEX IF NOT EXISTS idx_agent_search_first_crawl_agent_time
    ON public.hatamoto_agent_search_first_crawl (agent_id, first_at);

-- Single row: when search first-crawl recording began. Search hits had no page
-- before this release, so a page published earlier has no meaningful publish ->
-- first-crawl latency; the Google view only measures pages published since.
-- Not hatamoto_agent_tracking, which dates the AI first crawl.
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_search_tracking (
    id         boolean PRIMARY KEY DEFAULT true CHECK (id),
    started_at timestamp with time zone NOT NULL DEFAULT now()
);
INSERT INTO public.hatamoto_agent_search_tracking (id) VALUES (true) ON CONFLICT DO NOTHING;

-- Page attribution backfill. It looks up the paths that still lack a page by
-- path: these partial indexes hold only those rows (so they stay small once
-- attributed) and serve both the path listing and the per-path updates.
CREATE INDEX IF NOT EXISTS idx_agent_daily_unattributed
    ON public.hatamoto_agent_daily (path) WHERE entity_id IS NULL AND status < 400;
CREATE INDEX IF NOT EXISTS idx_agent_hits_unattributed
    ON public.hatamoto_agent_hits (path, occurred_at) WHERE entity_id IS NULL AND COALESCE(status, 0) < 400;

-- Paths the backfill found no page for, so a restart does not resolve them
-- again until checked_at ages out.
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_attribution_misses (
    path       text PRIMARY KEY,
    checked_at timestamp with time zone NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS public.hatamoto_agent_attribution_misses;
DROP INDEX IF EXISTS public.idx_agent_hits_unattributed;
DROP INDEX IF EXISTS public.idx_agent_daily_unattributed;
DROP TABLE IF EXISTS public.hatamoto_agent_search_tracking;
DROP TABLE IF EXISTS public.hatamoto_agent_search_first_crawl;
