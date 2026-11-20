-- Site identity: one stable random id per install, created here so every
-- install has it after "hatamoto migrate". It lives in the database, so a
-- backup restore keeps it; content export/import does not touch it. Core
-- never sends it anywhere. Down then up creates a new id.
-- Expand only: a new table the previous release never reads.

-- +goose Up
CREATE TABLE IF NOT EXISTS hatamoto_site_identity (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    site_id uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO hatamoto_site_identity (id) VALUES (true) ON CONFLICT (id) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS hatamoto_site_identity;
