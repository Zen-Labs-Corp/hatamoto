-- +goose Up
-- Funnel steps before a submission: forms seen / started, once per visit per day.
ALTER TABLE public.hatamoto_traffic_daily
    ADD COLUMN IF NOT EXISTS form_seen    bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS form_started bigint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE public.hatamoto_traffic_daily
    DROP COLUMN IF EXISTS form_seen,
    DROP COLUMN IF EXISTS form_started;
