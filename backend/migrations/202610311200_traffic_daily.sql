-- +goose Up
-- Cookieless traffic rollup: one row per (day, dimension, key). No visitor, IP,
-- user agent or referrer URL column exists by design; only aggregate counters.
CREATE TABLE IF NOT EXISTS public.hatamoto_traffic_daily (
    day       date NOT NULL,
    dim       text NOT NULL CHECK (dim IN ('channel','source','campaign','landing','device','country')),
    k1        text NOT NULL DEFAULT '' CHECK (char_length(k1) <= 200),
    k2        text NOT NULL DEFAULT '' CHECK (char_length(k2) <= 100),
    visits    bigint NOT NULL DEFAULT 0,
    pageviews bigint NOT NULL DEFAULT 0,
    engaged   bigint NOT NULL DEFAULT 0,
    multi     bigint NOT NULL DEFAULT 0,
    scrolled  bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (day, dim, k1, k2)
);
CREATE INDEX IF NOT EXISTS idx_traffic_daily_dim_day
    ON public.hatamoto_traffic_daily (dim, day);

-- Single row: owner switch for engagement counting.
CREATE TABLE IF NOT EXISTS public.hatamoto_analytics_settings (
    id                 boolean PRIMARY KEY DEFAULT true CHECK (id),
    engagement_enabled boolean NOT NULL DEFAULT true
);
INSERT INTO public.hatamoto_analytics_settings (id) VALUES (true) ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS public.hatamoto_analytics_settings;
DROP TABLE IF EXISTS public.hatamoto_traffic_daily;
