#!/usr/bin/env bash
# Attack the sealed admin from a public page in headless Chromium, against a fresh local install.
# Server runs on one database:
#   1. admin pinned at /admin: seed, grant the attack plugin site.scripts, AT-01 to AT-15 and AT-22
#   1b. same data, ENABLE_COOKIE_AUTH=true: AT-03b (cookie-path control)
#   2. same data, admin pinned at a random address: AT-16 to AT-21 and the scanner check S-1
#   3. a hatamoto_qa build with HATAMOTO_QA_SANDBOX_ALL=1, pinned at /admin: Layer B (B-1 to B-3)
# Every line is PASS, HOLE (the attack worked) or INCONCLUSIVE (the positive control failed).
# Exit 0 only when every line is PASS.
#
# Env: PW (path to playwright-core, required), CHROME (default /usr/bin/google-chrome),
# DATABASE_URL + QA_USE_DATABASE_URL=1 (an existing local database; otherwise a throwaway
# Postgres cluster is started), PG_BIN, HM_BIN and HM_QA_BIN (prebuilt binaries; otherwise built),
# ATTACK_OUT (directory for the summary and server logs; default: a temp dir removed at exit).
set -Eeuo pipefail

ROOT=$(git rev-parse --show-toplevel)
HERE=$ROOT/test/admin-attack
QA=$ROOT/scripts/admin-qa
# shellcheck source=../../scripts/admin-qa/qa-env.sh
. "$QA/qa-env.sh"

die() { echo "admin-attack: $*" >&2; exit 2; }
[ -n "${PW:-}" ] || die "set PW to the playwright-core directory (npm ci in scripts/admin-qa)"
export PW
CHROME=${CHROME:-/usr/bin/google-chrome}
[ -x "$CHROME" ] || command -v "$CHROME" >/dev/null || die "chrome not found (set CHROME)"
export CHROME
command -v node >/dev/null || die "node not found"

WORK=$(mktemp -d /tmp/hm-admin-attack.XXXXXX)
OUT=${ATTACK_OUT:-$WORK/out}
mkdir -p "$OUT"
SRV=""
PGSTARTED=""
PG_BIN=${PG_BIN:-}

stop_server() {
  if [ -n "$SRV" ]; then
    kill "$SRV" 2>/dev/null || true
    wait "$SRV" 2>/dev/null || true
    SRV=""
  fi
}
cleanup() {
  stop_server
  if [ -n "$PGSTARTED" ]; then "$PG_BIN/pg_ctl" -D "$WORK/pg" stop -m immediate >/dev/null 2>&1 || true; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

free_port() { node -e 'const s=require("net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})'; }

# ---- database ----
qa_guard_database_url || die "database not allowed"
if [ -z "${DATABASE_URL:-}" ]; then
  PG_BIN=$(qa_find_pg_bin)
  [ -x "${PG_BIN:-/nonexistent}/initdb" ] || die "no postgres binaries (set PG_BIN or DATABASE_URL)"
  "$PG_BIN/initdb" -D "$WORK/pg" -U hatamoto --auth=trust >/dev/null
  # The port is only a hint: another process can take it before Postgres binds, so retry.
  started=""
  for _ in 1 2 3 4 5 6; do
    PGPORT=$(free_port)
    if "$PG_BIN/pg_ctl" -D "$WORK/pg" -o "-p $PGPORT -k $WORK -c listen_addresses=127.0.0.1" -l "$WORK/pg.log" -w start >/dev/null; then started=1; break; fi
    grep -q 'already in use' "$WORK/pg.log" 2>/dev/null || break
    : >"$WORK/pg.log"
  done
  [ -n "$started" ] || { cat "$WORK/pg.log" >&2; die "postgres did not start"; }
  PGSTARTED=1
  "$PG_BIN/createdb" -h 127.0.0.1 -p "$PGPORT" -U hatamoto hatamoto_attack
  export DATABASE_URL="postgres://hatamoto@127.0.0.1:$PGPORT/hatamoto_attack?sslmode=disable" QA_USE_DATABASE_URL=1
fi

# ---- binaries ----
HM_BIN=${HM_BIN:-$WORK/hm}
HM_QA_BIN=${HM_QA_BIN:-$WORK/hm-qa}
[ -x "$HM_BIN" ] || (cd "$ROOT/backend" && go build -o "$HM_BIN" ./cmd/hatamoto) || die "go build failed"
[ -x "$HM_QA_BIN" ] || (cd "$ROOT/backend" && go build -tags hatamoto_qa -o "$HM_QA_BIN" ./cmd/hatamoto) || die "go build (hatamoto_qa) failed"

# ---- server ----
export API_HOST=127.0.0.1 EMAIL_DISABLED=true HATAMOTO_BIN=$HM_BIN
export HATAMOTO_DATA="$WORK/data" HATAMOTO_NOTFOUND_PER_MIN=0
export HATAMOTO_PLUGIN_DIR="$HERE/fixtures/plugins"
mkdir -p "$WORK/cwd"

# A 3x4 base32 address like the one a fresh install generates.
ADMIN_RANDOM=$(node -e 'const a="abcdefghijklmnopqrstuvwxyz234567",c=require("crypto").randomBytes(12);console.log("/"+[0,4,8].map((i)=>[...c.subarray(i,i+4)].map((b)=>a[b%32]).join("")).join("-"))')

LOG=""
start_server() { # $1 = binary, $2 = admin path, $3 = log name
  LOG=$OUT/admin-attack-$3.log
  # A fresh port per run: a stale server from an earlier run on the old port can never answer the health check.
  API_PORT=$(free_port)
  export API_PORT BASE="http://127.0.0.1:$API_PORT"
  (cd "$WORK/cwd" && HATAMOTO_ADMIN_PATH="$2" exec "$1" serve >"$LOG" 2>&1) &
  SRV=$!
  for _ in $(seq 1 60); do
    kill -0 "$SRV" 2>/dev/null || { cat "$LOG" >&2; die "server exited at start"; }
    # Healthy only if the process we started is still alive after the answer.
    if curl -fs -o /dev/null "$BASE/health" && kill -0 "$SRV" 2>/dev/null; then return 0; fi
    sleep 0.5
  done
  cat "$LOG" >&2
  die "server did not come up on $BASE"
}

failed=0
SUMMARY=$OUT/summary.txt
: >"$SUMMARY"
attack() { # $1 = admin address, then the script; its lines go to the console and the summary
  local a=$1 s=$2
  echo "== $s (admin address $([ "$a" = /admin ] && echo /admin || echo random))"
  local rc
  set +e
  ADMIN_ADDR=$a node "$HERE/$s" 2>&1 | tee -a "$SUMMARY"
  rc=${PIPESTATUS[0]} # the exit code of node, not of tee
  set -e
  if [ "$rc" -ne 0 ]; then
    failed=1
    echo "FAILED $s" | tee -a "$SUMMARY"
    echo "--- server log tail ($LOG)" >&2
    tail -n 25 "$LOG" >&2 || true
  fi
}

echo "== run 1: admin pinned at /admin"
start_server "$HM_BIN" /admin 1
bash "$QA/seed.sh" >"$OUT/seed.log" 2>&1 || { tail -n 20 "$OUT/seed.log" >&2; die "seed failed"; }
ATK=$HERE node --input-type=module -e 'const { grant } = await import(process.env.ATK + "/lib.mjs"); await grant("attackplug", ["site.scripts"]);'
attack /admin attacks-browser.mjs
attack /admin attacks-state.mjs
attack /admin attacks-canvas.mjs
stop_server

echo "== run 1b: same data, admin pinned at /admin, API cookie sessions on (ENABLE_COOKIE_AUTH=true)"
ENABLE_COOKIE_AUTH=true start_server "$HM_BIN" /admin 1b
attack /admin attacks-cookieauth.mjs
stop_server

echo "== run 2: same data, admin pinned at a random address"
start_server "$HM_BIN" "$ADMIN_RANDOM" 2
attack "$ADMIN_RANDOM" attacks-discovery.mjs
attack "$ADMIN_RANDOM" scanner.mjs
stop_server

echo "== run 3: hatamoto_qa build, every public page sandboxed, admin pinned at /admin"
HATAMOTO_QA_SANDBOX_ALL=1 start_server "$HM_QA_BIN" /admin 3
attack /admin layer-b.mjs
stop_server

pass=$(grep -c '^PASS ' "$SUMMARY" || true)
bad=$(grep -Ec '^(HOLE|INCONCLUSIVE|FAILED) ' "$SUMMARY" || true)
echo
echo "admin-attack summary: $pass PASS, $bad not passing"
if [ "$failed" -ne 0 ] || [ "$pass" -eq 0 ]; then
  echo "admin-attack: FAILED (a HOLE means an attack worked: stop and report it, do not edit the test)" >&2
  exit 1
fi
echo "admin-attack: all attacks failed and all controls succeeded"
