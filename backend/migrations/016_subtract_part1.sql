-- +goose Up
-- v1-03 subtract, part 1. Drops the storage behind features removed from core:
--   * Search Console connection (returns as a plugin in v1-19)
--   * analytics firehose: raw events (and their monthly partitions), anonymous
--     visitors and the lead intent-signal weights
-- and adds the only analytics store that stays: pageviews and AI referrals
-- rolled up per day. hatamoto_internal_link_rules is kept; the linking v2
-- suggester and equity tiers still read it.
DROP TABLE IF EXISTS public.hatamoto_search_console;

CREATE TABLE IF NOT EXISTS public.hatamoto_analytics_daily (
    day   date   NOT NULL,
    -- 'pageview' (key = '') or 'ai_referral' (key = assistant, e.g. chatgpt).
    kind  text   NOT NULL,
    key   text   NOT NULL DEFAULT '',
    count bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (day, kind, key)
);

-- Carry the history the rollup keeps across before the raw events go (same
-- idea as 010's bot-visit backfill). AI referrals are per assistant per day
-- (ai_source is set only on ai_referred_visit events); pageviews are per day.
-- Guarded so a database without the table (or with it empty) migrates fine.
-- +goose StatementBegin
DO $$
BEGIN
    IF to_regclass('public.hatamoto_events') IS NOT NULL THEN
        INSERT INTO public.hatamoto_analytics_daily (day, kind, key, count)
        SELECT (occurred_at AT TIME ZONE 'UTC')::date, 'ai_referral', ai_source, count(*)
          FROM public.hatamoto_events
         WHERE ai_source IS NOT NULL
         GROUP BY 1, 3
        ON CONFLICT (day, kind, key) DO NOTHING;

        INSERT INTO public.hatamoto_analytics_daily (day, kind, key, count)
        SELECT (occurred_at AT TIME ZONE 'UTC')::date, 'pageview', '', count(*)
          FROM public.hatamoto_events
         WHERE event_type = 'page_view'
         GROUP BY 1
        ON CONFLICT (day, kind, key) DO NOTHING;
    END IF;
END
$$;
-- +goose StatementEnd

-- DROP on the partitioned parent removes every hatamoto_events_YYYY_MM and
-- hatamoto_events_default partition with it.
DROP TABLE IF EXISTS public.hatamoto_events;
DROP TABLE IF EXISTS public.hatamoto_visitors;
DROP TABLE IF EXISTS public.hatamoto_intent_signals;

-- +goose Down
-- Irreversible: the dropped tables held data that cannot be recovered. Only
-- the new rollup table is removed.
DROP TABLE IF EXISTS public.hatamoto_analytics_daily;
