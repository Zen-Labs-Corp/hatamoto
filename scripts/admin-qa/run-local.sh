#!/usr/bin/env bash
# Boot a seeded local instance and run QA scripts against it, then tear down.
# Usage: scripts/admin-qa/run-local.sh [script.mjs ...]   (default: consistency.mjs consistency-families.mjs)
# Env: DATABASE_URL + QA_USE_DATABASE_URL=1 (use an existing local database, e.g. a CI
# service; DATABASE_URL alone is refused because seeding adds a known-password owner;
# else a throwaway Postgres cluster is created under /tmp), PG_BIN (dir with initdb,
# pg_ctl, createdb; default: see qa-env.sh), API_PORT, CHROME, PW, QA_OUT, QA_STRICT, QA_JOBS (sessions at once, default 4)
# (default 1), QA_SEED_ANALYTICS=1 (seed analytics rows so charts are populated), QA_VERSION (build with this release version, see below). Runs the `consistency.mjs` and `crawl.mjs` `--self-test`s first. Exits with the first failing
# script's code.
set -Eeuo pipefail
ROOT=$(git rev-parse --show-toplevel)
QA=$ROOT/scripts/admin-qa
# shellcheck source=qa-env.sh
. "$QA/qa-env.sh"
export QA_STRICT=${QA_STRICT:-1}
[ $# -gt 0 ] || set -- consistency.mjs consistency-families.mjs
W=$(mktemp -d /tmp/hm-admin-qa.XXXXXX)
SRV=""; PGSTARTED=""

cleanup() {
  [ -n "$SRV" ] && kill "$SRV" 2>/dev/null && wait "$SRV" 2>/dev/null || true
  [ -n "$PGSTARTED" ] && "$PG_BIN/pg_ctl" -D "$W/pg" stop -m immediate >/dev/null 2>&1 || true
  rm -rf "$W"
}
trap cleanup EXIT
die() { echo "run-local: $*" >&2; exit 2; }

free_port() { node -e 'const s=require("net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})'; }

CHROME=${CHROME:-/usr/bin/google-chrome}
[ -x "$CHROME" ] || command -v "$CHROME" >/dev/null || die "chrome not found (set CHROME)"
export CHROME
command -v node >/dev/null || die "node not found"

if [ -z "${PW:-}" ] && [ ! -d "$QA/node_modules/playwright-core" ]; then
  (cd "$QA" && npm ci --no-audit --no-fund >/dev/null) || die "npm ci failed in scripts/admin-qa"
fi

qa_guard_database_url || die "database not allowed"
if [ -z "${DATABASE_URL:-}" ]; then
  PG_BIN=$(qa_find_pg_bin)
  [ -x "${PG_BIN:-/nonexistent}/initdb" ] || die "no postgres binaries (set PG_BIN or DATABASE_URL)"
  "$PG_BIN/initdb" -D "$W/pg" -U hatamoto --auth=trust >/dev/null
  # free_port is only a hint (another session or an outgoing connection can take it
  # before Postgres binds): on "Address already in use" try another port.
  started=""
  for _ in 1 2 3 4 5 6; do
    PGPORT=$(free_port)
    if "$PG_BIN/pg_ctl" -D "$W/pg" -o "-p $PGPORT -k $W -c listen_addresses=127.0.0.1" -l "$W/pg.log" -w start >/dev/null; then started=1; break; fi
    grep -q 'already in use' "$W/pg.log" 2>/dev/null || break
    : >"$W/pg.log"
  done
  [ -n "$started" ] || { cat "$W/pg.log" >&2; die "postgres did not start"; }
  PGSTARTED=1
  "$PG_BIN/createdb" -h 127.0.0.1 -p "$PGPORT" -U hatamoto hatamoto_qa
  export DATABASE_URL="postgres://hatamoto@127.0.0.1:$PGPORT/hatamoto_qa?sslmode=disable" QA_USE_DATABASE_URL=1
fi

# The self-test needs no server: fail fast if a rule stopped firing.
echo "== node consistency.mjs --self-test, consistency-families.mjs --self-test, crawl.mjs --self-test"
node "$QA/consistency.mjs" --self-test
node "$QA/consistency-families.mjs" --self-test
node "$QA/crawl.mjs" --self-test

# QA_VERSION=v1.4.1 stamps a release version so the Updates screens leave the dev-build
# state; the update URL then defaults to a closed local port so no check leaves the machine.
LDF=""
if [ -n "${QA_VERSION:-}" ]; then
  LDF="-X hatamoto.dev/core/app.Version=$QA_VERSION"
  export HATAMOTO_UPDATE_URL=${HATAMOTO_UPDATE_URL:-https://127.0.0.1:9}
fi
(cd "$ROOT/backend" && go build ${QA_TAGS:+-tags "$QA_TAGS"} -ldflags "$LDF" -o "$W/hm" ./cmd/hatamoto) || die "go build failed"

# PR-0 admin-component-contracts: the styleguide page (UI-D14 checks every component variant there).
export HATAMOTO_DEV=1
# The QA harness pins the legacy address; real installs get a random one.
export HATAMOTO_ADMIN_PATH=/admin
export API_HOST=127.0.0.1 API_PORT=${API_PORT:-$(free_port)} EMAIL_DISABLED=true
export HATAMOTO_DATA="$W/data"
# No HATAMOTO_ADMIN_DIR: it re-parses every admin template per request (~1s each),
# and the binary built above already embeds this checkout's admin files.
export BASE="http://127.0.0.1:$API_PORT" HATAMOTO_BIN="$W/hm"
# frame-tickets.mjs and tabs.mjs need a plugin with an admin page; the seeded instance has none.
# The dev override loads this fixture (a UI-only plugin, empty wasm module). A caller's
# HATAMOTO_PLUGIN_DIR wins; the fixture is set for the server process only.
PLUGIN_DIR=${HATAMOTO_PLUGIN_DIR:-}
case " $* " in *" frame-tickets.mjs "*|*" tabs.mjs "*) PLUGIN_DIR=${PLUGIN_DIR:-$QA/fixtures/frame-plugin} ;; esac
mkdir -p "$W/cwd"
(cd "$W/cwd" && ${PLUGIN_DIR:+export HATAMOTO_PLUGIN_DIR="$PLUGIN_DIR"} && exec "$W/hm" serve >"$W/hm.log" 2>&1) &
SRV=$!

for _ in $(seq 1 60); do
  curl -fs -o /dev/null "$BASE/health" && break
  kill -0 "$SRV" 2>/dev/null || { cat "$W/hm.log" >&2; die "server exited"; }
  sleep 0.5
done
curl -fs -o /dev/null "$BASE/health" || { cat "$W/hm.log" >&2; die "server did not come up on $BASE"; }

bash "$QA/seed.sh"
bash "$QA/backups-seed.sh"
# QA_SEED_ANALYTICS=1: 100 days of analytics, so the charts draw data.
[ "${QA_SEED_ANALYTICS:-}" != 1 ] || bash "$QA/analytics-seed.sh"

rc=0
for s in "$@"; do
  echo "== node $s"
  node "$QA/$s" || { rc=$?; break; }
done
exit "$rc"
