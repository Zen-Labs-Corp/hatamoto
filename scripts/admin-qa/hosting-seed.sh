#!/usr/bin/env bash
# Seed Settings > Hosting: static mode with a directory target and three export runs, the newest failed
# (the failure banner and the Failed badge). No S3 secret is stored and no 'running' row is written (the
# partial unique index would block every export the QA server starts). The settings are written straight
# into the database, so the running server's island snapshot keeps the mode it booted with; the Hosting
# page reads the settings fresh, so the screens are right. Idempotent: the settings row is upserted and
# the runs are replaced.
# Throwaway QA databases only. Opt-in: run-local.sh calls it when QA_SEED_HOSTING=1. Needs psql (in PG_BIN
# or on PATH).
# Usage: QA_USE_DATABASE_URL=1 DATABASE_URL=... scripts/admin-qa/hosting-seed.sh
set -Eeuo pipefail
: "${DATABASE_URL:?set DATABASE_URL to the QA database}"
. "$(dirname "${BASH_SOURCE[0]}")/qa-env.sh"
qa_guard_database_url
PSQL=psql
[ -n "${PG_BIN:-}" ] && [ -x "$PG_BIN/psql" ] && PSQL=$PG_BIN/psql
# A stale directory would make the engine refuse the target (it only replaces an earlier export).
rm -rf /tmp/hm-qa-static /tmp/hm-qa-static.prev
"$PSQL" "$DATABASE_URL" -v ON_ERROR_STOP=1 -q <<'SQL'
BEGIN;
INSERT INTO hatamoto_static_hosting_settings (id, mode, target, dir_path, live_origin)
VALUES (true, 'static', 'dir', '/tmp/hm-qa-static', 'https://live.example.com')
ON CONFLICT (id) DO UPDATE SET mode = EXCLUDED.mode, target = EXCLUDED.target, dir_path = EXCLUDED.dir_path,
  live_origin = EXCLUDED.live_origin;
DELETE FROM hatamoto_static_export_runs;
INSERT INTO hatamoto_static_export_runs (trigger, target, status, pages, bytes, changed, duration_ms, errors, started_at, heartbeat_at, finished_at) VALUES
  ('manual', 'dir:/tmp/hm-qa-static', 'ok', 214, 39845888, 214, 9000, '[]', now() - interval '2 days', now() - interval '2 days', now() - interval '2 days'),
  ('change', 'dir:/tmp/hm-qa-static', 'ok', 214, 39845888, 3, 4000, '[]', now() - interval '1 hour', now() - interval '1 hour', now() - interval '1 hour'),
  ('change', 'dir:/tmp/hm-qa-static', 'failed', 120, 0, 0, 2000, '["render 500 on /blog/x"]', now() - interval '2 minutes', now() - interval '2 minutes', now() - interval '2 minutes');
COMMIT;
SQL
echo "hosting-seed: static mode, 3 export runs (newest failed)"
