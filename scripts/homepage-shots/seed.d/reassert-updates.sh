#!/usr/bin/env bash
# Writes the "rolled back" update state. The server's own scheduled check can overwrite
# the row, so capture.mjs runs this again right before the updates shot (shots.json "pre").
# Needs DATABASE_URL (and psql in PG_BIN or on PATH).
set -Eeuo pipefail
PSQL=psql
[ -n "${PG_BIN:-}" ] && [ -x "$PG_BIN/psql" ] && PSQL=$PG_BIN/psql
"$PSQL" "${DATABASE_URL:?set DATABASE_URL}" -X -v ON_ERROR_STOP=1 -q <<'SQL'
INSERT INTO hatamoto_update_settings (id) VALUES (TRUE) ON CONFLICT DO NOTHING;
UPDATE hatamoto_update_settings SET mode = 'auto', decision = 'rolled_back', target = 'v0.26.1',
  reason = 'smoke failed: GET /contact returned 500', last_result = 'ok', last_check_at = now() - interval '12 minutes';
INSERT INTO hatamoto_updates (event_id, from_version, to_version, phase, error, started_at, finished_at) VALUES
 ('demo-1', 'v0.25.4', 'v0.26.0', 'success', '', now() - interval '6 days', now() - interval '6 days'),
 ('demo-2', 'v0.26.0', 'v0.26.1', 'rolled_back', 'smoke failed: GET /contact returned 500', now() - interval '2 hours', now() - interval '2 hours')
ON CONFLICT DO NOTHING;
SQL
