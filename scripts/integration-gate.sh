#!/usr/bin/env bash
# integration-gate.sh <repo-root> <package>... - run the integration-tagged Go
# tests of the given packages (paths relative to backend/, e.g. ./internal/server)
# against a throwaway, migrated Postgres.
#
# PG_BIN  directory with initdb, pg_ctl and createdb (default: found on PATH).
# TMPDIR  where the cluster lives (default /tmp); removed on exit. May contain spaces.
#         Only the Unix socket directory is elsewhere (a short /tmp path: socket paths are
#         limited to ~100 bytes); the DSN uses TCP.
# INTEGRATION_MUST_RUN  optional, one "<package> <Test>..." entry per line: after the packages, run exactly
#         those tests (-v, integration tag) and fail unless each prints "--- PASS" and none is skipped
#         (the local twin of the "Must-run tests" step of .github/workflows/ci.yml). With it set,
#         no package argument is needed.
# GO_TEST_TIMEOUT  per-package `go test -timeout` (default 30m; Go's 10m default panics slow
#         packages such as internal/admin). Honoured by go-test-flaky.sh and the must-run step.
# Exit: 0 pass, 1 test failure, 2 setup failure (no Postgres, migrate failed).
set -uo pipefail
root=$(cd "${1:?usage: integration-gate.sh <repo-root> <package>...}" && pwd); shift
((${#@})) || [[ -n ${INTEGRATION_MUST_RUN:-} ]] || { echo "integration-gate: no packages given" >&2; exit 2; }

pgdir=${PG_BIN:-}
if [[ -z $pgdir ]]; then
  initdb=$(command -v initdb) || initdb=""
  [[ -n $initdb ]] && pgdir=$(dirname "$(readlink -f "$initdb")")
fi
for b in initdb pg_ctl createdb; do
  [[ -x $pgdir/$b ]] || { echo "integration-gate: $b not found. Install PostgreSQL 16 or set PG_BIN to its bin directory." >&2; exit 2; }
done

T=$(mktemp -d "${TMPDIR:-/tmp}/int-gate.XXXXXX")
S=$(mktemp -d /tmp/hmpg.XXXX) || { echo "integration-gate: cannot create a socket directory in /tmp" >&2; exit 2; }    # socket directory only: a few bytes, short path, no spaces
mkdir "$T/home" "$T/run"
export TMPDIR=$T GOTMPDIR=$T
cleanup() { "$pgdir/pg_ctl" -D "$T/data" -m immediate stop >/dev/null 2>&1; rm -rf "$T" "$S"; }
trap cleanup EXIT
trap 'exit 143' TERM INT HUP    # a cancelled gate (pre-push) must stop Postgres and remove its directories

"$pgdir/initdb" -D "$T/data" -E UTF8 -A scram-sha-256 --pwfile=<(echo pw) -U postgres >"$T/initdb.log" 2>&1 \
  || { tail "$T/initdb.log" >&2; exit 2; }
# Pick a port and start; on "Address already in use" pick another. A probe alone races
# with other sessions' clusters and with outgoing loopback connections (which take
# ports from the ephemeral range 32768-60999, so stay below it).
port=""
for _ in 1 2 3 4 5 6; do
  p=""
  for c in $(shuf -i 20000-32000 -n 50); do
    (exec 3<>"/dev/tcp/127.0.0.1/$c") 2>/dev/null || { p=$c; break; }
  done
  [[ -n $p ]] || break
  if "$pgdir/pg_ctl" -D "$T/data" -o "-p $p -k $S -c listen_addresses=127.0.0.1" -l "$T/pg.log" -w start >/dev/null; then
    port=$p; break
  fi
  grep -q 'already in use' "$T/pg.log" 2>/dev/null || { tail "$T/pg.log" >&2; exit 2; }
  echo "integration-gate: port $p taken, retrying" >&2
  : >"$T/pg.log"
done
[[ -n $port ]] || { echo "integration-gate: no free port" >&2; tail "$T/pg.log" >&2; exit 2; }
PGPASSWORD=pw "$pgdir/createdb" -h 127.0.0.1 -p "$port" -U postgres hm_test || exit 2
dsn="postgresql://postgres:pw@127.0.0.1:$port/hm_test?sslmode=disable"

cd "$root/backend" || exit 2
go build -o "$T/hm" ./cmd/hatamoto || exit 2
(cd "$T/run" && env -i PATH="$PATH" HOME="$T/home" DATABASE_URL="$dsn" "$T/hm" migrate) >"$T/migrate.log" 2>&1 \
  || { tail -20 "$T/migrate.log" >&2; exit 2; }
echo "integration-gate: migrated on port $port"

rc=0
for p in "$@"; do
  echo "== $p"
  DATABASE_URL=$dsn HATAMOTO_TEST_DB_URL=$dsn EMAIL_DISABLED=true \
    bash "$root/scripts/go-test-flaky.sh" "-tags=integration -p 1" "$p" || rc=1
done

while read -r pkg names; do
  [[ -n ${pkg:-} ]] || continue
  echo "== must-run $pkg: $names"
  read -ra tests <<<"$names"; re=$(IFS="|"; echo "${tests[*]}")
  log=$T/mustrun.log
  DATABASE_URL=$dsn HATAMOTO_TEST_DB_URL=$dsn EMAIL_DISABLED=true \
    go test -count=1 -timeout "${GO_TEST_TIMEOUT:-30m}" -v -tags=integration -p 1 -run "^($re)\$" "$pkg" 2>&1 | tee "$log"
  for t in $names; do
    grep -q -e "^--- PASS: $t " "$log" || { echo "must-run test missing or not passing: $t" >&2; rc=1; }
    if grep -q -e "^--- SKIP: $t " "$log"; then echo "must-run test skipped: $t" >&2; rc=1; fi
  done
done <<<"${INTEGRATION_MUST_RUN:-}"
exit $rc
