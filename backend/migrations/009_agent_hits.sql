-- +goose Up
-- Agent analytics: one row per AI crawler/assistant request (short retention,
-- carries the client IP) plus a long-lived per-day rollup (no IP, ever).

CREATE TABLE IF NOT EXISTS public.hatamoto_agent_hits (
    id           uuid NOT NULL DEFAULT gen_random_uuid(),
    occurred_at  timestamp with time zone NOT NULL,
    agent_id     text,
    operator     text,
    purpose      text,
    verification text NOT NULL DEFAULT 'pending',
    ip           inet,
    ip_truncated boolean NOT NULL DEFAULT false,
    method       text,
    path         text,
    entity_id    uuid,
    status       smallint,
    bytes        bigint,
    duration_ms  integer,
    source       text NOT NULL,
    CONSTRAINT hatamoto_agent_hits_pkey PRIMARY KEY (id, occurred_at),
    CONSTRAINT hatamoto_agent_hits_source_check CHECK (source IN ('middleware', 'backend', 'log')),
    CONSTRAINT hatamoto_agent_hits_verification_check CHECK (verification IN ('pending', 'verified', 'spoofed', 'unverifiable'))
) PARTITION BY RANGE (occurred_at);

-- Partitions are created here (not left to the worker) so inserts never depend
-- on ENABLE_WORKER; the default partition catches anything outside them.
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_hits_2026_10 PARTITION OF public.hatamoto_agent_hits
    FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00');
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_hits_2026_11 PARTITION OF public.hatamoto_agent_hits
    FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_hits_2026_12 PARTITION OF public.hatamoto_agent_hits
    FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_hits_2027_01 PARTITION OF public.hatamoto_agent_hits
    FOR VALUES FROM ('2027-01-01 00:00:00+00') TO ('2027-02-01 00:00:00+00');
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_hits_default PARTITION OF public.hatamoto_agent_hits DEFAULT;

CREATE INDEX IF NOT EXISTS idx_agent_hits_agent_time
    ON public.hatamoto_agent_hits (agent_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_hits_entity_time
    ON public.hatamoto_agent_hits (entity_id, occurred_at DESC) WHERE entity_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_hits_pending
    ON public.hatamoto_agent_hits (verification, occurred_at) WHERE verification = 'pending';
CREATE INDEX IF NOT EXISTS idx_agent_hits_ip_untruncated
    ON public.hatamoto_agent_hits (occurred_at) WHERE NOT ip_truncated;
CREATE INDEX IF NOT EXISTS idx_agent_hits_errors
    ON public.hatamoto_agent_hits (status, occurred_at DESC) WHERE status >= 400;

-- Rollup: no IP column by design. Hits with unknown status roll up under 0.
CREATE TABLE IF NOT EXISTS public.hatamoto_agent_daily (
    day               date NOT NULL,
    agent_id          text NOT NULL,
    path              text NOT NULL,
    status            integer NOT NULL DEFAULT 0,
    operator          text NOT NULL DEFAULT '',
    purpose           text NOT NULL DEFAULT '',
    entity_id         uuid,
    hits              bigint NOT NULL DEFAULT 0,
    verified_hits     bigint NOT NULL DEFAULT 0,
    spoofed_hits      bigint NOT NULL DEFAULT 0,
    unverifiable_hits bigint NOT NULL DEFAULT 0,
    bytes_total       bigint NOT NULL DEFAULT 0,
    CONSTRAINT hatamoto_agent_daily_key UNIQUE (day, agent_id, path, status)
);

CREATE INDEX IF NOT EXISTS idx_agent_daily_entity_day
    ON public.hatamoto_agent_daily (entity_id, day DESC) WHERE entity_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_daily_day
    ON public.hatamoto_agent_daily (day DESC);

-- +goose Down
DROP TABLE IF EXISTS public.hatamoto_agent_daily;
DROP TABLE IF EXISTS public.hatamoto_agent_hits;
