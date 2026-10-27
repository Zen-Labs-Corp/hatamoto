#!/usr/bin/env bash
# n-1.sh - does the PREVIOUS release's binary still work on THIS tree's schema?
# (docs/UPGRADES.md, Schema rules.) A rollback is "run the old binary against
# the migrated database" (no restore), so the old binary must boot and serve on
# the new schema. Run by the orchestrator locally and by the n-1 CI job (the
# workflow itself stays disabled on GitHub).
#
# Flow: build the baseline binary (newest release tag reachable from HEAD, else merge-base with
# origin/main; N1_BASE overrides) from a throwaway worktree and the HEAD binary
# from the working tree (so uncommitted migrations are exercised); migrate a
# fresh database with HEAD; seed an owner and two published pages with HEAD; use
# the BASELINE binary's CLI to WRITE to that schema (second user, an entity
# created, published, unpublished and published again, a redirect; subcommands
# are taken from the baseline's own `help`); boot the BASELINE `serve` on it, run
# the smoke set (including the entity and redirect the baseline just wrote), hold
# it for N1_HOLD seconds and scan its log, then run the baseline tree's own
# DB-backed tests against the HEAD-migrated database.
#
# What this does and does not prove: the baseline golden tests (render, server)
# are DB-free and only prove the baseline code still passes; they cannot see the
# schema. The schema coverage is the serve smoke, the baseline CLI writes and the
# DB-backed baseline packages below. The old binary's write paths are covered
# only by the CLI writes and by those packages; an HTTP admin write is not
# exercised. A baseline test that SKIPs (for example when a table is missing)
# fails the run: only a package absent from the baseline is skipped, and only
# the tests in ALLOW_SKIP (no database involved) may skip.
#
# Baseline packages whose integration tests use DATABASE_URL /
# HATAMOTO_TEST_DB_URL directly and run on the provided database:
#   internal/entity       run (repository, published_at, concurrency, public listing)
#   internal/linking/...  run (rewriter reads and writes the link index)
#   internal/auth         run (users, sessions, tokens)
#   internal/menu         run (menus and items)
#   internal/admin        run (admin screens end to end on Postgres)
#   internal/worker       run (agent-hit partitions and jobs)
#   internal/agents       run (agent hits, daily rollups, robots policy)
# Not run, with the reason:
#   internal/redirect     no database test at all (its tests are DB-free); the
#                         redirect write path is the CLI write above
#   internal/importjob, internal/packages, internal/pluginhost (capabilities),
#   internal/server (retired_prefix, admin_import), app boot/export/serve_boot,
#   migrations
#                         create their own scratch database migrated by the
#                         BASELINE, so they never see the HEAD schema
#   internal/server (rest) one big package that wipes shared tables and holds the
#                         sitemap golden, which needs a pristine database
#   internal/pluginhost (isolation, settings), app (sanitize_sections)
#                         plugin sandbox and sanitizer tests; left out of scope
#
# Env:
#   DATABASE_URL   required; the role must be able to CREATE DATABASE. A
#                  throwaway hm_n1_* database is created next to it and dropped.
#   N1_BASE        baseline ref override
#   N1_HOLD       seconds to keep the baseline serving after /ready (default 60)
#   N1_KEEP=1     keep the temp dir (binaries, serve.log)
#   N1_GOTOOLCHAIN Go toolchain override when the pinned one is not cached
#                  (e.g. "local"); default is each tree's go.mod toolchain line.
#
# Exit: 0 pass, 1 fail, 2 no baseline, 64 missing prerequisite.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"
ROOT=$PWD

for c in go psql curl git timeout; do
  command -v "$c" >/dev/null 2>&1 || { echo "n-1: missing prerequisite: $c" >&2; exit 64; }
done
[[ -n "${DATABASE_URL:-}" ]] || { echo "n-1: set DATABASE_URL (a role that can CREATE DATABASE)" >&2; exit 64; }

BASE=$(OVERRIDE="${N1_BASE:-}" bash scripts/baseline-ref.sh) || exit 2
echo "baseline: $BASE ($(git rev-parse --short "$BASE^{commit}"))"
if git diff --quiet "$BASE" -- backend/migrations && [[ -z "$(git ls-files -o --exclude-standard -- backend/migrations)" ]]; then
  echo "note: migrations identical to baseline; n-1 is trivial"
fi

WORK=$(mktemp -d)
DB="" SERVE_PID=""
# Failures are appended to a file, not an array: a failure recorded in a subshell
# (a pipeline stage, $(...)) must still fail the run.
RESULTS=$WORK/results
: >"$RESULTS"
LOG=$WORK/n1.log
exec > >(tee "$LOG") 2>&1
record() { echo "$1" >>"$RESULTS"; }

stop_serve() {
  [[ -n "$SERVE_PID" ]] || return 0
  kill -TERM "$SERVE_PID" 2>/dev/null
  for _ in $(seq 1 50); do kill -0 "$SERVE_PID" 2>/dev/null || break; sleep 0.2; done
  kill -KILL "$SERVE_PID" 2>/dev/null
  wait "$SERVE_PID" 2>/dev/null
  SERVE_PID=""
}
cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  stop_serve
  git -C "$ROOT" worktree remove --force "$WORK/base" >/dev/null 2>&1
  git -C "$ROOT" worktree prune
  [[ -z "$DB" ]] || psql "$DATABASE_URL" -qc "DROP DATABASE IF EXISTS $DB WITH (FORCE)" >/dev/null 2>&1
  if [[ "${N1_KEEP:-}" == 1 ]]; then echo "kept $WORK"; else rm -rf "$WORK"; fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

summary() {
  local n; n=$(grep -c . "$RESULTS")
  if [[ $n -eq 0 ]]; then
    # Self-check: a FAIL line in the log with nothing recorded is a lost failure.
    sleep 0.3
    if grep -q '^FAIL ' "$LOG"; then
      echo "n-1: FAIL (a FAIL line was printed but not recorded: the verdict was fail-open)"; exit 1
    fi
    echo "n-1: PASS (baseline $BASE)"; exit 0
  fi
  local list; list=$(paste -sd';' "$RESULTS" | sed 's/;/; /g')
  echo "n-1: FAIL ($n failed: $list)"; exit 1
}
fatal() { echo "FAIL $1"; record "$1"; summary; }

# url_with_db URL DB: URL with its database (last path segment) replaced.
url_with_db() {
  local u=$1 q=""
  if [[ $u == *\?* ]]; then q="?${u#*\?}"; u=${u%%\?*}; fi
  echo "${u%/*}/$2$q"
}

# toolchain TREE: the tree's go.mod toolchain line unless N1_GOTOOLCHAIN is set.
toolchain() {
  local tc=${N1_GOTOOLCHAIN:-$(awk '/^toolchain /{print $2}' "$1/backend/go.mod")}
  echo "${tc:-local}"
}
# build TREE OUT VERSION: the release-build flags (Makefile), host target only.
# The -X path comes from the tree's own go.mod: a baseline from before a module
# path change has another module path, and a wrong -X path is silently ignored.
build() {
  local tree=$1 out=$2 ver=$3 tc mod
  tc=$(toolchain "$tree")
  mod=$(awk '/^module /{print $2; exit}' "$tree/backend/go.mod")
  [[ -n $mod ]] || fatal "no module line in $tree/backend/go.mod"
  (cd "$tree/backend" && env GOENV=off GOWORK=off GOTOOLCHAIN="$tc" GOAMD64=v1 GOARM64=v8.0 GOFLAGS= GOEXPERIMENT= \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "-X $mod/app.Version=$ver -X $mod/app.Commit=n1" \
    -o "$out" ./cmd/hatamoto)
}

DB=hm_n1_$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -qc "CREATE DATABASE $DB" || { DB=""; fatal "create database"; }
DSN=$(url_with_db "$DATABASE_URL" "$DB")

echo "building HEAD (working tree) and baseline binaries"
HEAD_BIN=$WORK/hatamoto-head BASE_BIN=$WORK/hatamoto-base
git worktree add -q --detach "$WORK/base" "$BASE" || fatal "baseline worktree"
build "$ROOT" "$HEAD_BIN" n1-head || fatal "build HEAD binary (set N1_GOTOOLCHAIN=local if the pinned toolchain is not cached)"
build "$WORK/base" "$BASE_BIN" n1-base || fatal "build baseline binary (set N1_GOTOOLCHAIN=local if the pinned toolchain is not cached)"

mkdir -p "$WORK/run" "$WORK/home" "$WORK/data"
head_cli() { (cd "$WORK/run" && env -i PATH="$PATH" HOME="$WORK/home" DATABASE_URL="$DSN" "$HEAD_BIN" "$@"); }

echo "migrating with HEAD"
head_cli migrate >"$WORK/migrate.log" 2>&1 || { tail -20 "$WORK/migrate.log"; fatal "HEAD migrate"; }

# HEAD writes, baseline reads. The home page is seeded explicitly: the baseline
# only seeds one into an empty hatamoto_entities table.
PW=n1-password-123
head_cli user create --email owner@n1.test --name N1 --role owner --password-stdin <<<"$PW" >"$WORK/seed.log" 2>&1 \
  || { tail -20 "$WORK/seed.log"; fatal "seed owner"; }
head_cli entity create --schema page --name Home --slug home --publish --as owner@n1.test >>"$WORK/seed.log" 2>&1 \
  && head_cli entity create --schema page --name "N1 Page" --slug n1-page --publish --as owner@n1.test >>"$WORK/seed.log" 2>&1 \
  || { tail -20 "$WORK/seed.log"; fatal "seed pages"; }
# One hidden and one public person: the baseline knows only the status, so a
# hidden person must be a draft or a rollback would publish it.
head_cli entity create --schema person --name "N1 Hidden" --slug n1-hidden --attributes '{"public":false}' --as owner@n1.test >>"$WORK/seed.log" 2>&1 \
  && head_cli entity create --schema person --name "N1 Public" --slug n1-public --attributes '{"public":true}' --as owner@n1.test >>"$WORK/seed.log" 2>&1 \
  || { tail -20 "$WORK/seed.log"; fatal "seed people"; }

# Baseline writes: the old binary's CLI against the HEAD-migrated schema.
base_cli() { (cd "$WORK/run" && env -i PATH="$PATH" HOME="$WORK/home" DATABASE_URL="$DSN" timeout 60 "$BASE_BIN" "$@"); }
BASE_HELP=$("$BASE_BIN" help 2>&1 || true)
base_has() { grep -qF -- "$1" <<<"$BASE_HELP"; }
WRITES=$WORK/base-writes.log
: >"$WRITES"
base_write() { # label cmd...
  local label=$1; shift
  if "$@" >>"$WRITES" 2>&1; then echo "ok   baseline write: $label"
  else tail -20 "$WRITES"; fatal "baseline write: $label"; fi
}
WROTE_ENTITY=0 WROTE_REDIRECT=0
if base_has "user create"; then
  # no pipe: a pipeline stage is a subshell and would lose the recorded failure
  base_write "user create" base_cli user create --email editor@n1.test --name "N1 Editor" --role editor --password-stdin < <(printf '%s' "$PW")
else echo "note: baseline has no 'user create'; skipped"; fi
if base_has "entity create"; then
  lines=$(wc -l <"$WRITES")
  base_write "entity create" base_cli entity create --schema page --name "N1 Written" --slug n1-written --as owner@n1.test
  ENT_ID=$(tail -n +$((lines + 1)) "$WRITES" | sed -n 's/^ *"id": *"\([^"]*\)".*/\1/p' | head -1)
  [[ -n "$ENT_ID" ]] || fatal "baseline write: entity id not found in the create output"
  if base_has "entity publish" && base_has "entity unpublish"; then
    base_write "entity publish" base_cli entity publish "$ENT_ID" --as owner@n1.test
    base_write "entity unpublish (status update)" base_cli entity unpublish "$ENT_ID" --as owner@n1.test
    base_write "entity publish again" base_cli entity publish "$ENT_ID" --as owner@n1.test
    WROTE_ENTITY=1
  else echo "note: baseline cannot publish entities from the CLI; /n1-written is not smoked"; fi
else echo "note: baseline has no 'entity create'; skipped"; fi
if base_has "redirect create"; then
  base_write "redirect create" base_cli redirect create --from /n1-old --to /n1-page --status 302
  WROTE_REDIRECT=1
else echo "note: baseline has no 'redirect create'; skipped"; fi

# A free port: nothing answers on it.
while :; do
  PORT=$((20000 + RANDOM % 20000))
  (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null || break
done
echo "booting baseline serve on 127.0.0.1:$PORT"
(cd "$WORK/run" && exec env -i PATH="$PATH" HOME="$WORK/home" DATABASE_URL="$DSN" API_PORT="$PORT" API_HOST=127.0.0.1 \
  HATAMOTO_SECRET="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')" HATAMOTO_DATA="$WORK/data" EMAIL_DISABLED=true \
  "$BASE_BIN" serve) >"$WORK/serve.log" 2>&1 &
SERVE_PID=$!

ready=0
READY_DEADLINE=$((SECONDS + 120))
while (( SECONDS < READY_DEADLINE )); do
  if ! kill -0 "$SERVE_PID" 2>/dev/null; then tail -30 "$WORK/serve.log"; SERVE_PID=""; fatal "baseline serve exited during boot"; fi
  if [[ $(curl -s --max-time 10 --noproxy '*' -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/ready") == 200 ]]; then ready=1; break; fi
  sleep 0.5
done
[[ $ready -eq 1 ]] || { tail -30 "$WORK/serve.log"; fatal "baseline serve never became ready"; }
READY_AT=$SECONDS
echo "ok   baseline serve ready"

check_status() { # path want; sends what a browser address-bar visit sends (the admin answers nothing else)
  local code
  code=$(curl -s --max-time 10 --noproxy '*' -H 'Sec-Fetch-Site: none' -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document' -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT$1" || true)
  if [[ "$code" == "$2" ]]; then echo "ok   smoke GET $1: $code"
  else echo "FAIL smoke GET $1: $code (want $2)"; record "GET $1"; fi
}
check_status / 200
check_status /n1-page 200
check_status /sitemap.xml 200
check_status /admin/login 200
check_status /author/n1-hidden 404
check_status /author/n1-public 200
# /sitemap.xml is an index: read every child it lists as well.
SITEMAP=$(curl -s --max-time 10 --noproxy '*' "http://127.0.0.1:$PORT/sitemap.xml" || true)
for child in $(grep -o '/sitemap-[A-Za-z0-9_-]*\.xml' <<<"$SITEMAP" | sort -u); do
  SITEMAP+=$(curl -s --max-time 10 --noproxy '*' "http://127.0.0.1:$PORT$child" || true)
done
if grep -q 'n1-hidden' <<<"$SITEMAP"; then echo "FAIL sitemap lists the hidden person"; record "sitemap hidden person"
else echo "ok   sitemap omits the hidden person"; fi
if grep -q 'n1-public' <<<"$SITEMAP"; then echo "ok   sitemap lists the public person"
else echo "FAIL sitemap lacks the public person"; record "sitemap public person"; fi
[[ $WROTE_ENTITY -eq 0 ]] || check_status /n1-written 200
[[ $WROTE_REDIRECT -eq 0 ]] || check_status /n1-old 302

# Hold the window after the first /ready, then look at the log.
HOLD=${N1_HOLD:-60}
while (( SECONDS - READY_AT < HOLD )); do
  if ! kill -0 "$SERVE_PID" 2>/dev/null; then
    tail -30 "$WORK/serve.log"; SERVE_PID=""; echo "FAIL baseline serve exited during the hold window"; record "serve died"; break
  fi
  left=$(( HOLD - (SECONDS - READY_AT) )); sleep $(( left < 5 ? left : 5 ))
done
[[ -z "$SERVE_PID" ]] || check_status / 200

PATTERNS='plugin not loaded|plugin migrations failed|host function failed|plugin route failed|plugin template func failed|render: (section failed|theme rebuild failed|theme templates failed|plugin templates rejected|skipping plugin)'
if grep -nE "$PATTERNS" "$WORK/serve.log"; then echo "FAIL log: plugin/template errors"; record "log patterns"
else echo "ok   log: no plugin/template errors"; fi
# No allow-list: any ERROR line fails. Add an exemption only with evidence.
if grep -n 'level=ERROR' "$WORK/serve.log"; then echo "FAIL log: level=ERROR"; record "level=ERROR"
else echo "ok   log: no level=ERROR"; fi

stop_serve

# The baseline's own tests against the HEAD-migrated database.
BASE_TC=$(toolchain "$WORK/base")
base_go() { (cd "$WORK/base/backend" && env GOENV=off GOFLAGS= GOWORK=off GOTOOLCHAIN="$BASE_TC" "$@"); }
# pkg_present PKG: is the package directory in the baseline tree? (the only allowed skip)
pkg_present() { local d=${1#./}; d=${d%/...}; [[ -d "$WORK/base/backend/$d" ]]; }
golden=()
for pkg in ./internal/render ./internal/server; do
  if pkg_present "$pkg"; then golden+=("$pkg"); else echo "skip $pkg (package not present in baseline)"; fi
done
if [[ ${#golden[@]} -gt 0 ]]; then
  if base_go go test -count=1 "${golden[@]}" -run Golden; then echo "ok   baseline golden tests"
  else echo "FAIL baseline golden tests"; record "baseline golden tests"; fi
fi

# DB-backed packages (header): any build/list error, test failure or SKIP fails.
# ALLOW_SKIP: pkg-path.TestName of tests that skip without touching the database.
ALLOW_SKIP=("internal/admin.TestSettingsLiveTabsUseTheNewHref")
for pkg in ./internal/entity ./internal/linking/... ./internal/auth ./internal/menu ./internal/admin ./internal/worker ./internal/agents; do
  if ! pkg_present "$pkg"; then echo "skip $pkg (package not present in baseline)"; continue; fi
  log=$WORK/test-$(tr '/.' '__' <<<"${pkg#./}").json
  if ! base_go env HATAMOTO_TEST_DB_URL="$DSN" DATABASE_URL="$DSN" go test -json -count=1 -tags=integration "$pkg" >"$log" 2>&1; then
    tail -40 "$log"; echo "FAIL baseline $pkg"; record "baseline $pkg"; continue
  fi
  skipped=$(grep -o '"Action":"skip","Package":"[^"]*","Test":"[^"]*"' "$log" \
    | sed -E 's#.*"Package":"[^"]*/(internal/[^"]*)","Test":"([^"]*)"#\1.\2#' | sort -u)
  unexpected=""
  while IFS= read -r sk; do
    [[ -n "$sk" ]] || continue
    allowed=0
    for ok_skip in "${ALLOW_SKIP[@]}"; do [[ "$sk" == "$ok_skip" || "$sk" == "$ok_skip"/* ]] && allowed=1; done
    [[ $allowed -eq 1 ]] || unexpected+="$sk "
  done <<<"$skipped"
  if [[ -n "$unexpected" ]]; then echo "FAIL baseline $pkg: tests skipped (DB tests must run): $unexpected"; record "baseline $pkg skipped"; continue; fi
  echo "ok   baseline $pkg"
done

summary
