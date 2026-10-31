-- +goose Up
-- First/last touch of the submitter, classified server-side at submit time.
-- All columns are NULL for submissions without attribution ("Unknown"). Only
-- channel/source/medium/campaign/landing path: no IP, user agent or identifier.
ALTER TABLE public.hatamoto_form_submissions
    ADD COLUMN IF NOT EXISTS first_channel text,
    ADD COLUMN IF NOT EXISTS first_source text,
    ADD COLUMN IF NOT EXISTS first_medium text,
    ADD COLUMN IF NOT EXISTS first_campaign text,
    ADD COLUMN IF NOT EXISTS first_landing text,
    ADD COLUMN IF NOT EXISTS last_channel text,
    ADD COLUMN IF NOT EXISTS last_source text,
    ADD COLUMN IF NOT EXISTS last_medium text,
    ADD COLUMN IF NOT EXISTS last_campaign text,
    ADD COLUMN IF NOT EXISTS last_landing text;

-- +goose Down
ALTER TABLE public.hatamoto_form_submissions
    DROP COLUMN IF EXISTS first_channel,
    DROP COLUMN IF EXISTS first_source,
    DROP COLUMN IF EXISTS first_medium,
    DROP COLUMN IF EXISTS first_campaign,
    DROP COLUMN IF EXISTS first_landing,
    DROP COLUMN IF EXISTS last_channel,
    DROP COLUMN IF EXISTS last_source,
    DROP COLUMN IF EXISTS last_medium,
    DROP COLUMN IF EXISTS last_campaign,
    DROP COLUMN IF EXISTS last_landing;
