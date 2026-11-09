#!/usr/bin/env bash
# Writes the "rolled back" update state. The server's own scheduled check can overwrite
# the row, so capture.mjs runs this again right before the updates shot (shots.json "pre").
# Needs DATABASE_URL (and psql in PG_BIN or on PATH). The versions default to the homepage
# set (QA_VERSION v0.26.0); run.sh sets them from QA_VERSION for the README set.
set -Eeuo pipefail
CUR=${QA_VERSION:-v0.26.0} PREV=${SHOTS_PREV_VERSION:-v0.25.4} NEXT=${SHOTS_NEXT_VERSION:-v0.26.1}
for v in "$CUR" "$PREV" "$NEXT"; do [[ $v =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "reassert-updates: bad version $v" >&2; exit 2; }; done
PSQL=psql
[ -n "${PG_BIN:-}" ] && [ -x "$PG_BIN/psql" ] && PSQL=$PG_BIN/psql
"$PSQL" "${DATABASE_URL:?set DATABASE_URL}" -X -v ON_ERROR_STOP=1 -q <<SQL
INSERT INTO hatamoto_update_settings (id) VALUES (TRUE) ON CONFLICT DO NOTHING;
UPDATE hatamoto_update_settings SET mode = 'auto', decision = 'rolled_back', target = '$NEXT',
  reason = 'smoke failed: GET /contact returned 500', last_result = 'ok', last_check_at = now() - interval '12 minutes';
INSERT INTO hatamoto_updates (event_id, from_version, to_version, phase, error, started_at, finished_at) VALUES
 ('demo-1', '$PREV', '$CUR', 'success', '', now() - interval '6 days', now() - interval '6 days'),
 ('demo-2', '$CUR', '$NEXT', 'rolled_back', 'smoke failed: GET /contact returned 500', now() - interval '2 hours', now() - interval '2 hours')
ON CONFLICT DO NOTHING;
SQL
