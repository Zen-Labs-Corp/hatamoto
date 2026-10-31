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

-- +goose Down
DROP TABLE IF EXISTS public.hatamoto_agent_search_first_crawl;
