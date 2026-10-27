-- +goose Up
-- Persisted operator IP range lists for agent verification. Ranges survive a
-- restart so pending hits can be verified before the first refresh completes.
-- No client data here: only published operator prefixes.

CREATE TABLE IF NOT EXISTS public.hatamoto_agent_ip_ranges (
    key         text PRIMARY KEY,
    prefixes    text[] NOT NULL,
    fetched_at  timestamp with time zone NOT NULL,
    source_urls text[]
);

-- +goose Down
DROP TABLE IF EXISTS public.hatamoto_agent_ip_ranges;
