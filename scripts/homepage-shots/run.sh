#!/usr/bin/env bash
# One command: build the backend, boot a throwaway instance, seed a fictional demo,
# capture the 13 homepage shots as framed 2880x1800 WebPs and write a contact sheet.
# Usage: scripts/homepage-shots/run.sh
# Env: OUT_DIR (default scripts/homepage-shots/out), QA_VERSION (default
# v0.26.0, stamped into the build so Settings > Updates leaves the dev-build state),
# CHROME, PG_BIN, GTM_REPO (default ../hatamoto-plugin-gtm), ONLY (comma
# separated slots), HATAMOTO_BIN (prebuilt binary stamped with QA_VERSION, skips the build),
# SHOTS_CACHE (dir that keeps the demo key and gtm.hmp between runs, for iterating; dev only,
# the key is a throwaway demo key, never put a real key there; must be outside the repo),
# SHOTS_SERVE=1 (seed, print the address and wait, no capture).
set -Eeuo pipefail
ROOT=$(git rev-parse --show-toplevel)
HS=$ROOT/scripts/homepage-shots
QA=$ROOT/scripts/admin-qa
# shellcheck source=../admin-qa/qa-env.sh
. "$QA/qa-env.sh"
OUT_DIR=${OUT_DIR:-$ROOT/scripts/homepage-shots/out}
QA_VERSION=${QA_VERSION:-v0.26.0}
W=$(mktemp -d /tmp/hm-homepage-shots.XXXXXX)
SRV=""; PGSTARTED=""
die() { echo "homepage-shots: $*" >&2; exit 2; }
cleanup() {
  [ -n "$SRV" ] && kill "$SRV" 2>/dev/null && wait "$SRV" 2>/dev/null || true
  [ -n "$PGSTARTED" ] && "$PG_BIN/pg_ctl" -D "$W/pg" stop -m immediate >/dev/null 2>&1 || true
  rm -rf "$W"
}
trap cleanup EXIT
free_port() { node -e 'const s=require("net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})'; }

if [ -n "${SHOTS_CACHE:-}" ]; then
  mkdir -p "$SHOTS_CACHE"; SHOTS_CACHE=$(cd "$SHOTS_CACHE" && pwd -P)
  case "$SHOTS_CACHE/" in "$ROOT"/*) die "SHOTS_CACHE must be outside the repo ($ROOT)" ;; esac
fi
export CHROME=${CHROME:-/usr/bin/google-chrome}
[ -x "$CHROME" ] || command -v "$CHROME" >/dev/null || die "chrome not found (set CHROME)"
command -v node >/dev/null || die "node not found"
[ -d "$HS/node_modules/sharp" ] || (cd "$HS" && npm ci --no-audit --no-fund >/dev/null) || die "npm ci failed in scripts/homepage-shots"
# capture.mjs reuses login() from admin-qa, which needs its own playwright-core unless PW is set.
export PW=${PW:-$HS/node_modules/playwright-core}

# Throwaway database: never DATABASE_URL, the seed adds an owner with a known password.
[ -z "${DATABASE_URL:-}" ] || die "unset DATABASE_URL: this script always makes its own throwaway cluster"
PG_BIN=$(qa_find_pg_bin)
[ -x "${PG_BIN:-/nonexistent}/initdb" ] || die "no postgres binaries (set PG_BIN)"
PGPORT=$(free_port)
"$PG_BIN/initdb" -D "$W/pg" -U hatamoto --auth=trust >/dev/null
"$PG_BIN/pg_ctl" -D "$W/pg" -o "-p $PGPORT -k $W -c listen_addresses=127.0.0.1" -l "$W/pg.log" -w start >/dev/null || { cat "$W/pg.log" >&2; die "postgres did not start"; }
PGSTARTED=1
"$PG_BIN/createdb" -h 127.0.0.1 -p "$PGPORT" -U hatamoto hatamoto_shots
export DATABASE_URL="postgres://hatamoto@127.0.0.1:$PGPORT/hatamoto_shots?sslmode=disable" QA_USE_DATABASE_URL=1 PG_BIN

# A stamped release build leaves the dev-build state; the update URL is a closed local port.
export HATAMOTO_UPDATE_URL=${HATAMOTO_UPDATE_URL:-https://127.0.0.1:9}
if [ -n "${HATAMOTO_BIN:-}" ] && [ -x "$HATAMOTO_BIN" ]; then
  cp "$HATAMOTO_BIN" "$W/hm" # a prebuilt binary (it must be stamped with QA_VERSION)
else
  (cd "$ROOT/backend" && go build -ldflags "-X hatamoto.dev/core/app.Version=$QA_VERSION" -o "$W/hm" ./cmd/hatamoto) || die "go build failed"
fi
export HATAMOTO_BIN=$W/hm

# A local signing key for the gtm plugin; the server must trust it before it starts.
if [ -n "${SHOTS_CACHE:-}" ] && [ -f "$SHOTS_CACHE/demo.key" ]; then cp "$SHOTS_CACHE"/demo.key "$SHOTS_CACHE"/demo.pub "$W/"; else
  "$HATAMOTO_BIN" keys generate --name demo --dir "$W" >/dev/null
fi
HATAMOTO_TRUSTED_KEYS=$(sed -n 's/.*"public": *"\([^"]*\)".*/\1/p' "$W/demo.pub")
export HATAMOTO_TRUSTED_KEYS
export GTM_HMP=$W/gtm.hmp
if [ -n "${SHOTS_CACHE:-}" ] && [ -f "$SHOTS_CACHE/gtm.hmp" ]; then cp "$SHOTS_CACHE/gtm.hmp" "$GTM_HMP"; else
  bash "$HS/seed.d/gtm-build.sh" "$GTM_HMP" "$W/demo.key" || die "gtm build failed"
fi
if [ -n "${SHOTS_CACHE:-}" ] && [ ! -f "$SHOTS_CACHE/gtm.hmp" ]; then mkdir -p "$SHOTS_CACHE" && cp "$W/demo.key" "$W/demo.pub" "$GTM_HMP" "$SHOTS_CACHE/"; fi

API_PORT=${API_PORT:-$(free_port)}
# The QA harness pins the legacy address; real installs get a random one.
export HATAMOTO_ADMIN_PATH=/admin
export API_HOST=127.0.0.1 API_PORT EMAIL_DISABLED=true HATAMOTO_DEV=1 PUBLIC_BASE_URL="http://127.0.0.1:$API_PORT"
export HATAMOTO_DATA=$W/data
export BASE="http://127.0.0.1:$API_PORT"
mkdir -p "$W/cwd"
(cd "$W/cwd" && exec "$W/hm" serve >"$W/hm.log" 2>&1) &
SRV=$!
for _ in $(seq 1 60); do
  curl -fs -o /dev/null "$BASE/health" && break
  kill -0 "$SRV" 2>/dev/null || { cat "$W/hm.log" >&2; die "server exited"; }
  sleep 0.5
done
curl -fs -o /dev/null "$BASE/health" || { cat "$W/hm.log" >&2; die "server did not come up on $BASE"; }

export QA_VERSION QA_EMAIL=${QA_EMAIL:-mika.lindqvist@example.com} QA_PASSWORD=${QA_PASSWORD:-demo-password-123}
bash "$HS/seed.sh" || [ "${SHOTS_SERVE:-}" = 1 ] || die "seed failed"
if [ "${SHOTS_SERVE:-}" = 1 ]; then
  echo "serving $BASE (owner $QA_EMAIL, password $QA_PASSWORD); Ctrl-C to stop" >&2
  echo "env: DATABASE_URL=$DATABASE_URL HATAMOTO_BIN=$HATAMOTO_BIN GTM_HMP=$GTM_HMP" >&2
  wait "$SRV"
fi

mkdir -p "$OUT_DIR"
# Only this tool's own outputs: the files named in shots.json, the sheet and failure dumps.
while read -r f; do rm -f "$OUT_DIR/$f" "$OUT_DIR/_fail-$f.png"; done < <(node -e 'for (const s of require(process.argv[1])) console.log(s.file)' "$HS/shots.json")
rm -f "$OUT_DIR"/contact-sheet.png
node "$HS/capture.mjs" --out "$OUT_DIR" ${ONLY:+--only "$ONLY"}
node "$HS/contact-sheet.mjs" "$OUT_DIR"
echo "homepage-shots: done, $(node -e 'console.log(require(process.argv[1]).length)' "$HS/shots.json") shots in $OUT_DIR"
