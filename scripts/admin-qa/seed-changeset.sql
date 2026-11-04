-- Seed for runs-screens.mjs and review-flow.mjs: plugin runs and one staged changeset
-- for the frameplug fixture. Run with psql against the QA database:
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -v running=0 -f seed-changeset.sql
-- running=1 also adds a run in the build phase (a plugin may have only one running run,
-- so only the run screens ask for it). Risk and decision columns stay NULL: the review
-- page classifies lazily; item_count and payload_bytes are set to match the items.
\if :running
INSERT INTO hatamoto_runs (plugin, name, state, phase, hosts, progress, steps)
VALUES ('frameplug', 'import', 'running', 'build', '{example.com}', '{"done":37,"total":60,"message":"Building pages"}', 12);
\endif

WITH run AS (
  INSERT INTO hatamoto_runs (plugin, name, state, phase, hosts, progress, steps)
  VALUES ('frameplug', 'import', 'finished', 'build', '{example.com}', '{"done":60,"total":60}', 40)
  RETURNING id
), cs AS (
  INSERT INTO hatamoto_changesets (run_id, plugin, state, expires_at)
  SELECT id, 'frameplug', 'staged', now() + interval '71 hours' FROM run
  RETURNING id, run_id
), items AS (
  INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, source_key, payload)
  SELECT cs.id, v.seq, v.kind, 'upsert', v.key, v.payload::jsonb
  FROM cs, (VALUES
    (1, 'entity',   'qa-low',       '{"schema":"post","name":"QA Import Plain","slug":"qa-import-plain","attributes":{"content":"Plain text, nothing unusual."}}'),
    (2, 'entity',   'qa-iframe',    '{"schema":"post","name":"QA Import Embed","slug":"qa-import-embed","attributes":{"content":"Watch: <iframe src=\"https://example.com/video\"></iframe>"}}'),
    (3, 'entity',   'qa-link',      '{"schema":"post","name":"QA Import Link","slug":"qa-import-link","attributes":{"content":"<a href=\"https://example.com/offer\">Our offer</a>"}}'),
    (4, 'entity',   'qa-zwsp',      E'{"schema":"post","name":"QA Import Hidden","slug":"qa-import-hidden","attributes":{"content":"Zero\\u200bwidth"}}'),
    (5, 'redirect', 'qa-redirect-1', '{"source_path":"/qa-old-plain","target":"/qa-import-plain"}'),
    (6, 'redirect', 'qa-redirect-2', '{"source_path":"/qa-old-embed","target":"/qa-import-embed"}'),
    (7, 'redirect', 'qa-redirect-3', '{"source_path":"/qa-old-link","target":"/qa-import-link"}')
  ) AS v(seq, kind, key, payload)
  RETURNING 1
)
INSERT INTO hatamoto_run_proposals (run_id, kind, payload)
SELECT cs.run_id, 'site_settings', p.payload::jsonb
FROM cs, (VALUES
  ('{"tagline":"Imported by the QA run"}'),
  ('{"title":"QA Imported Site"}'),
  ('{"tagline":"Second tagline","language":"fr"}')
) AS p(payload);

-- The header's counters follow the items, as the engine keeps them when it stages.
UPDATE hatamoto_changesets c SET item_count = t.n, payload_bytes = t.bytes
FROM (SELECT changeset_id, count(*) AS n, sum(octet_length(payload::text)) AS bytes
      FROM hatamoto_changeset_items GROUP BY changeset_id) t
WHERE c.id = t.changeset_id AND c.plugin = 'frameplug' AND c.item_count = 0;
