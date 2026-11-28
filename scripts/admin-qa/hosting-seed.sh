#!/usr/bin/env bash
# Seed Settings > Hosting: static mode with a directory target and three export runs, the newest failed
# (the failure banner and the Failed badge). No S3 secret is stored and no 'running' row is written (the
# partial unique index would block every export the QA server starts). The settings are written straight
# into the database, so the running server's island snapshot keeps the mode it booted with; the Hosting
# page reads the settings fresh, so the screens are right. Idempotent: the settings row is upserted and
# the runs are replaced.
# QA_HOSTING_LIVE_ORIGIN sets the saved live origin: unset keeps https://live.example.com, empty means same host.
# QA_HOSTING_TARGET=sftp seeds the SFTP target instead (example.com host, trusted host key, .htaccess on; the
# stored credential is a placeholder blob that cannot be opened, so the page shows the "can no longer be read"
# state and still treats the target as saved, which the setup card needs) and its runs carry the SFTP run key. Unset keeps the
# directory seed.
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
LIVE=${QA_HOSTING_LIVE_ORIGIN-https://live.example.com}
TARGET=${QA_HOSTING_TARGET:-dir}
case "$TARGET" in
dir) RUN_KEY='dir:/tmp/hm-qa-static' ;;
sftp) RUN_KEY='sftp:site@ssh.example.com:22/public_html' ;;
*) echo "hosting-seed: QA_HOSTING_TARGET must be dir or sftp" >&2; exit 1 ;;
esac
"$PSQL" "$DATABASE_URL" -v ON_ERROR_STOP=1 -v live="$LIVE" -v target="$TARGET" -v runkey="$RUN_KEY" -q <<'SQL'
BEGIN;
INSERT INTO hatamoto_static_hosting_settings (id, mode, target, dir_path, live_origin,
  sftp_host, sftp_user, sftp_dir, sftp_host_key, sftp_htaccess, sftp_secret_sealed)
VALUES (true, 'static', :'target', '/tmp/hm-qa-static', :'live',
  CASE WHEN :'target' = 'sftp' THEN 'ssh.example.com' ELSE '' END,
  CASE WHEN :'target' = 'sftp' THEN 'site' ELSE '' END,
  'public_html',
  CASE WHEN :'target' = 'sftp' THEN 'SHA256:qa-example' ELSE '' END,
  true,
  CASE WHEN :'target' = 'sftp' THEN '\x00'::bytea ELSE NULL END)
ON CONFLICT (id) DO UPDATE SET mode = EXCLUDED.mode, target = EXCLUDED.target, dir_path = EXCLUDED.dir_path,
  live_origin = EXCLUDED.live_origin, sftp_host = EXCLUDED.sftp_host, sftp_user = EXCLUDED.sftp_user,
  sftp_dir = EXCLUDED.sftp_dir, sftp_host_key = EXCLUDED.sftp_host_key, sftp_htaccess = EXCLUDED.sftp_htaccess,
  sftp_secret_sealed = EXCLUDED.sftp_secret_sealed;
DELETE FROM hatamoto_static_export_runs;
INSERT INTO hatamoto_static_export_runs (trigger, target, status, pages, bytes, changed, duration_ms, errors, started_at, heartbeat_at, finished_at) VALUES
  ('manual', :'runkey', 'ok', 214, 39845888, 214, 9000, '[]', now() - interval '2 days', now() - interval '2 days', now() - interval '2 days'),
  ('change', :'runkey', 'ok', 214, 39845888, 3, 4000, '[]', now() - interval '1 hour', now() - interval '1 hour', now() - interval '1 hour'),
  ('change', :'runkey', 'failed', 120, 0, 0, 2000, '["render 500 on /blog/x"]', now() - interval '2 minutes', now() - interval '2 minutes', now() - interval '2 minutes');
COMMIT;
SQL
echo "hosting-seed: static mode, $TARGET target (${LIVE:-same host}), 3 export runs (newest failed)"
