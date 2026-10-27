#!/usr/bin/env bash
# test-demo-seed.sh - proves scripts/demo-seed.sh and the demo mode end to end on a
# throwaway PostgreSQL: initdb, boot a demo server (the server migrates on boot),
# seed, curl. Needs PG_BIN (initdb, pg_ctl, createdb) and Go (or HATAMOTO_BIN).
# Without PG_BIN and without initdb on PATH it prints a note and exits 0.
# Env: PG_BIN, HATAMOTO_BIN, PGPORT_T (default 55941), APP_PORT (default 18191).
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
bash -n scripts/demo-seed.sh
PG_BIN=${PG_BIN:-$(dirname "$(command -v initdb 2>/dev/null || echo /nonexistent/x)")}
if [ ! -x "$PG_BIN/initdb" ]; then echo "test-demo-seed: no PostgreSQL (set PG_BIN); only the syntax was checked"; exit 0; fi
export TMPDIR=${TMPDIR:-/tmp}
D=$(mktemp -d "$TMPDIR/demo-seed.XXXXXX")
PORT=${PGPORT_T:-55941}; APP=${APP_PORT:-18191}; BASE=http://127.0.0.1:$APP
pid=""
cleanup() { [ -n "$pid" ] && kill "$pid" 2>/dev/null || true; wait 2>/dev/null || true
  "$PG_BIN/pg_ctl" -D "$D/data" -m immediate stop >/dev/null 2>&1 || true; rm -rf "$D"; }
trap cleanup EXIT
fail() { echo "FAIL $*"; [ -f "$D/app.log" ] && tail -20 "$D/app.log"; exit 1; }
ok() { echo "ok   $*"; }

BIN=${HATAMOTO_BIN:-$D/hatamoto}
[ -x "$BIN" ] || (cd backend && go build -o "$BIN" ./cmd/hatamoto)

echo pw > "$D/pw"
"$PG_BIN/initdb" -D "$D/data" -E UTF8 -A scram-sha-256 --pwfile="$D/pw" -U postgres >/dev/null
"$PG_BIN/pg_ctl" -D "$D/data" -o "-p $PORT -k $D" -l "$D/pg.log" -w start >/dev/null
PGPASSWORD=pw "$PG_BIN/createdb" -h localhost -p "$PORT" -U postgres hm_demo
export DATABASE_URL="postgresql://postgres:pw@localhost:$PORT/hm_demo?sslmode=disable"
export HATAMOTO_DEMO_LOGIN_EMAIL=demo@example.test HATAMOTO_DEMO_LOGIN_PASSWORD=demo-password-123

boot() { # boot ENV=VAL... : start the server on $APP and wait for /health
  [ -n "$pid" ] && { kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }
  env API_PORT=$APP API_HOST=127.0.0.1 HATAMOTO_DATA="$D/appdata" MEDIA_UPLOAD_DIR="$D/appdata/uploads" \
    HATAMOTO_REGISTRY_URL=off HATAMOTO_WORKER=off PUBLIC_BASE_URL=$BASE "$@" "$BIN" serve >"$D/app.log" 2>&1 &
  pid=$!
  for _ in $(seq 1 60); do curl -fs -o /dev/null "$BASE/health" && return 0; sleep 1; done
  fail "server did not start"
}

# 1. a demo server on an empty database; the seed refuses a non-local database and no login.
boot HATAMOTO_DEMO=1
if DATABASE_URL="postgresql://x:y@db.example.com/z" HATAMOTO_BIN="$BIN" BASE=$BASE bash scripts/demo-seed.sh >/dev/null 2>&1; then fail "seeded a remote database"; fi
ok "refuses a non-local database"
if env -u HATAMOTO_DEMO_LOGIN_PASSWORD HATAMOTO_BIN="$BIN" BASE=$BASE bash scripts/demo-seed.sh >/dev/null 2>&1; then fail "seeded without a password"; fi
ok "refuses a missing login"

# 2. the seed.
HATAMOTO_BIN="$BIN" BASE=$BASE bash scripts/demo-seed.sh >"$D/seed.out" 2>&1 || { cat "$D/seed.out"; fail "seed failed"; }
grep -q "done. Sign in" "$D/seed.out" && ok "seed ran: $(grep -c '^GET .* 200$' "$D/seed.out") pages answer 200"
grep -q "no DEMO_PLUGINS" "$D/seed.out" && ok "plugins skipped with a note"
if HATAMOTO_BIN="$BIN" BASE=$BASE bash scripts/demo-seed.sh >/dev/null 2>&1; then fail "a second seed run must stop"; fi
ok "a second run stops instead of duplicating"

# 3. demo mode: login on the page, banner, refusals.
NAV=(-H 'Sec-Fetch-Site: same-origin' -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document') # what a browser sends; the admin refuses bare requests
login=$(curl -s "${NAV[@]}" "$BASE/admin/login")
grep -q "demo@example.test" <<<"$login" && grep -q "demo-password-123" <<<"$login" || fail "the sign-in page does not show the demo login"
ok "demo login shown on the sign-in page"
grep -q "Public demo, resets every hour" <<<"$login" && ok "banner on the sign-in page" || fail "no banner on the sign-in page"
curl -s "$BASE/" | grep -q "data-hm-demo-banner" && ok "banner on the public site" || fail "no banner on the public site"
jar=$D/jar
# token PATH: the csrf meta of a navigated admin page (the sign-in screens are rate limited, so read once per sign-in state).
token() { curl -fsL "${NAV[@]}" -b "$jar" -c "$jar" "$BASE$1" | sed -n 's/.*<meta name="csrf-token" content="\([^"]*\)".*/\1/p' | head -1; }
T=$(token /admin/login)
curl -s -o /dev/null -c "$jar" -b "$jar" "${NAV[@]}" -H "Origin: $BASE" --data-urlencode "_csrf=$T" --data-urlencode "email=$HATAMOTO_DEMO_LOGIN_EMAIL" --data-urlencode "password=$HATAMOTO_DEMO_LOGIN_PASSWORD" "$BASE/admin/login"
T=$(token /admin)
for p in /admin/settings/users /admin/settings/webhooks /admin/plugins/upload /admin/settings/api-keys /admin/settings/updates/apply /admin/profile/password; do
  c=$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -b "$jar" "${NAV[@]}" -H "Origin: $BASE" -H "X-CSRF-Token: $T" -X POST "$BASE$p")
  [ "$c" = 403 ] || fail "POST $p answered $c, want 403"
done
ok "refusals answer 403"

# 4. demo off on the same database: no login hint, no banner.
boot HATAMOTO_DEMO=
login=$(curl -s "${NAV[@]}" "$BASE/admin/login")
grep -q "demo-password-123" <<<"$login" && fail "the login is shown with the demo off"
curl -s "$BASE/" | grep -q "data-hm-demo-banner" && fail "banner with the demo off"
ok "demo off: no login hint, no banner"
echo "test-demo-seed: all ok"
