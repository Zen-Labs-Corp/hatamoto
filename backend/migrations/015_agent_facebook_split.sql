-- +goose Up
-- The registry used to merge Meta's two crawlers under agent_id 'facebookbot'.
-- It now has facebookexternalhit (link previews, social) and facebookbot
-- (model-training crawler). Rows stored so far are overwhelmingly link-preview
-- fetches (010's legacy 'FacebookBot' rows are all facebookexternalhit), so
-- move them to facebookexternalhit. Only rows carrying the OLD purpose
-- ('social') move: the new binary writes facebookbot with purpose 'training',
-- so genuine FacebookBot traffic recorded before or after this runs is kept,
-- whatever the deploy/migrate order. hatamoto_agent_first_crawl is untouched:
-- it only records AI purposes, so it never held a social facebookbot row.

-- Rollup: delete the old rows and fold them into any existing
-- facebookexternalhit row on the same (day, path, status) key. One pass over
-- the facebookbot rows via the (day, agent_id, path, status) unique index
-- scan; re-running finds nothing to move.
-- +goose StatementBegin
WITH moved AS (
    DELETE FROM public.hatamoto_agent_daily WHERE agent_id = 'facebookbot' AND purpose = 'social' RETURNING *
)
INSERT INTO public.hatamoto_agent_daily AS d
    (day, agent_id, path, status, operator, purpose, entity_id,
     hits, verified_hits, spoofed_hits, unverifiable_hits, bytes_total)
SELECT day, 'facebookexternalhit', path, status, 'Meta', 'social', entity_id,
       hits, verified_hits, spoofed_hits, unverifiable_hits, bytes_total
FROM moved
ON CONFLICT ON CONSTRAINT hatamoto_agent_daily_key DO UPDATE SET
    hits              = d.hits + EXCLUDED.hits,
    verified_hits     = d.verified_hits + EXCLUDED.verified_hits,
    spoofed_hits      = d.spoofed_hits + EXCLUDED.spoofed_hits,
    unverifiable_hits = d.unverifiable_hits + EXCLUDED.unverifiable_hits,
    bytes_total       = d.bytes_total + EXCLUDED.bytes_total,
    entity_id         = COALESCE(d.entity_id, EXCLUDED.entity_id);
-- +goose StatementEnd

UPDATE public.hatamoto_agent_hits
   SET agent_id = 'facebookexternalhit', purpose = 'social'
 WHERE agent_id = 'facebookbot' AND purpose = 'social';

-- +goose Down
-- Best-effort no-op: the merged rows cannot be told apart from rows that were
-- facebookexternalhit all along, and the old merged registry id is gone.
SELECT 1;
