-- +goose Up
-- Agent analytics admin: first AI crawl per entity (kept past the ~90-day hit
-- retention so publish -> first-crawl latency survives) and the robots.txt
-- policy. The policy table is intentionally NOT seeded: no row means "allow",
-- so robots.txt output is unchanged until an admin saves a deny.

CREATE TABLE IF NOT EXISTS public.hatamoto_agent_first_crawl (
    entity_id    uuid PRIMARY KEY REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE,
    agent_id     text NOT NULL,
    first_at     timestamp with time zone NOT NULL,
    published_at timestamp with time zone
);

-- Single row: when first-crawl tracking began. Entities published before this
-- have no meaningful publish -> first-crawl latency (their first crawl may be
-- years old), so the API only reports latency for published_at >= started_at.
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_tracking (
    id         boolean PRIMARY KEY DEFAULT true CHECK (id),
    started_at timestamp with time zone NOT NULL DEFAULT now()
);
INSERT INTO public.hatamoto_agent_tracking (id) VALUES (true) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS public.hatamoto_agent_robots_policy (
    purpose    text PRIMARY KEY,
    action     text NOT NULL,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT hatamoto_agent_robots_policy_action_check CHECK (action = 'deny')
);

-- +goose Down
DROP TABLE IF EXISTS public.hatamoto_agent_tracking;
DROP TABLE IF EXISTS public.hatamoto_agent_robots_policy;
DROP TABLE IF EXISTS public.hatamoto_agent_first_crawl;
