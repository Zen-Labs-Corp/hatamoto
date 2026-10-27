#!/usr/bin/env bash
# Seeds a fictional demo ("Northwind Studio") for the homepage shots: sources seed.d/*.sh
# in order against a running instance. Only demo names and example.com addresses.
# Usage: QA_USE_DATABASE_URL=1 DATABASE_URL=... BASE=http://127.0.0.1:PORT HATAMOTO_BIN=... \
#          GTM_HMP=path/to/gtm.hmp scripts/homepage-shots/seed.sh
# Meant for a fresh database (run.sh makes one); it is not idempotent.
set -Eeuo pipefail
HS=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BIN=${HATAMOTO_BIN:-hatamoto}
BASE=${BASE:-http://127.0.0.1:8090}
PASS=${QA_PASSWORD:-demo-password-123}
OWNER=${QA_EMAIL:-mika.lindqvist@example.com}
: "${DATABASE_URL:?set DATABASE_URL to the demo database}"
. "$HS/../admin-qa/qa-env.sh"
qa_guard_database_url || exit 2
trap 'echo "seed: failed at ${BASH_SOURCE[0]##*/}:$LINENO (exit $?)" >&2' ERR
PSQL=psql
if [ -n "${PG_BIN:-}" ] && [ -x "$PG_BIN/psql" ]; then PSQL=$PG_BIN/psql; fi
export BIN BASE PASS OWNER PSQL HS

ASSETS=$(mktemp -d); jar=$(mktemp); trap 'rm -rf "$ASSETS" "$jar"' EXIT
node "$HS/seed.d/gen-assets.mjs" "$ASSETS"

# The admin serves documents only to what a browser navigation sends, and keeps its token in the page, not in a cookie.
# Every curl in this file and in the sourced seed.d/*.sh sends the browser's navigation headers (public pages ignore them).
curl() { command curl -H 'Sec-Fetch-Site: same-origin' -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document' "$@"; }
# csrf: the token the next POST carries, read once per sign-in state (the sign-in screens are rate limited, so not per POST).
T=""
csrf() { printf '%s' "$T"; }
# token PATH: GET a navigated admin page with the jar (it keeps hm_pre, then the session) and print its csrf meta token.
token() { curl -fsL -b "$jar" -c "$jar" "$BASE$1" | sed -n 's/.*<meta name="csrf-token" content="\([^"]*\)".*/\1/p' | head -1; }
# signin EMAIL: a fresh browser signs in; the token comes from the sign-in page before, from the admin after (the session's).
signin() { : >"$jar"; T=$(token /admin/login); form /admin/login "email=$1" "password=$PASS" >/dev/null; T=$(token /admin); }
# req PATH curl-args...: one admin request; prints "PATH status" and fails on status >= 400
# (or a transport error), so a broken seed step stops the run.
req() { local p=$1 code; shift; code=$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -b "$jar" -H "Origin: $BASE" "$@" "$BASE$p") || return 1
  echo "$p $code"; [ "$code" -lt 400 ] || { echo "seed: $p returned $code" >&2; return 1; }; }
# post PATH curl-args...: a multipart admin POST (token in a header).
post() { local p=$1; shift; req "$p" -H "X-CSRF-Token: $(csrf)" "$@"; }
# form PATH k=v...: a urlencoded admin POST.
form() { local p=$1; shift; local a=(); for kv in "$@"; do a+=(--data-urlencode "$kv"); done; req "$p" --data-urlencode "_csrf=$(csrf)" "${a[@]}"; }
sql() { "$PSQL" "$DATABASE_URL" -v ON_ERROR_STOP=1 -qtAX -c "$1"; }
# mk SCHEMA SLUG NAME JSON: a published entity owned by the owner.
mk() { "$BIN" entity create --schema "$1" --slug "$2" --name "$3" --attributes "$4" --publish --as "$OWNER" >/dev/null && echo "created $1 $2" || { echo "seed: could not create $1 $2" >&2; return 1; }; }
# upload FILE: uploads to the media library, prints the /media/... URL.
upload() {
  post /admin/media/upload -F "file=@$1;type=image/png" >/dev/null
  sql "select public_url from hatamoto_media where original_filename='$(basename "$1")' order by created_at desc limit 1"
}

# SEED_ONLY=20 runs only seed.d/20-*.sh against an already seeded instance (signs in first).
if [ -n "${SEED_ONLY:-}" ]; then
  signin "$OWNER"
fi
for f in "$HS"/seed.d/${SEED_ONLY:-[0-9]}*.sh; do
  echo "== $(basename "$f")"
  # shellcheck disable=SC1090
  . "$f" # errexit applies here only because this is not part of an || list
done
echo "seed: done"
