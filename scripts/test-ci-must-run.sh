#!/usr/bin/env bash
# test-ci-must-run.sh - the must-run rule (scripts/must-run-check.sh) and its wiring: the CI
# step lists the D8 backup tests, a PostgreSQL client matching the server is installed before
# it, and the local gate uses the same check (a registered check suite).
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
fail() { echo "FAIL: $*" >&2; exit 1; }
chk="$ROOT/scripts/must-run-check.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

# 1. behaviour
printf '%s\n' '=== RUN   TestX' '--- PASS: TestX (0.01s)' >"$T/pass.log"
bash "$chk" "$T/pass.log" TestX || fail "plain PASS was refused"
printf '%s\n' '--- PASS: TestX (0.01s)' '    --- SKIP: TestX/superuser (0.00s)' >"$T/sub.log"
if bash "$chk" "$T/sub.log" TestX 2>/dev/null; then fail "a skipped subtest was accepted"; fi
printf '%s\n' '--- PASS: TestY (0.01s)' >"$T/miss.log"
if bash "$chk" "$T/miss.log" TestX 2>/dev/null; then fail "a missing test was accepted"; fi
printf '%s\n' '--- SKIP: TestX (0.00s)' >"$T/skip.log"
if bash "$chk" "$T/skip.log" TestX 2>/dev/null; then fail "a skipped test was accepted"; fi
printf '%s\n' '--- PASS: TestXY (0.01s)' >"$T/prefix.log"
if bash "$chk" "$T/prefix.log" TestX 2>/dev/null; then fail "a test with a longer name satisfied TestX"; fi

# 2. ci.yml
ci="$ROOT/.github/workflows/ci.yml"
step=$(awk '/^      - name: Must-run tests/{f=1;print;next} f&&/^      - name:/{exit} f' "$ci")
[ -n "$step" ] || fail "no Must-run tests step in ci.yml"
grep -qF './internal/backup' <<<"$step" || fail "the Must-run step does not list ./internal/backup"
for t in TestBackupRestoreAsSuperuserAndCreateRoleAdmin TestBootstrapNeedsGrantOptionOnDatabaseCreate \
  TestBootstrapRefusesSchemaOfAnotherOwner TestBootstrapWorksForAdminWithoutInheritance \
  TestBootstrapRefusesAdminWithoutSetOnAppRole; do
  grep -qF "$t" <<<"$step" || fail "the Must-run step does not name $t"
done
grep -qF 'must-run-check.sh' <<<"$step" || fail "the Must-run step does not call must-run-check.sh"
idle=$(awk '/^      - name: Idle memory budget really runs/{f=1;print;next} f&&/^      - name:/{exit} f' "$ci")
[ -n "$idle" ] || fail "no Idle memory budget step in ci.yml"
grep -qF 'TestBudgetIdleMemory' <<<"$idle" || fail "the idle memory step does not run TestBudgetIdleMemory"
grep -qF 'must-run-check.sh' <<<"$idle" || fail "the idle memory step does not call must-run-check.sh"
job=$(awk '/^  backend-matrix:/{f=1;print;next} f&&/^  [a-z]/{exit} f' "$ci")
inst=$(grep -nF 'postgresql-client-' <<<"$job" | head -1 | cut -d: -f1 || true)
[ -n "$inst" ] || fail "no postgresql-client install in backend-matrix"
must=$(grep -nF -- '- name: Must-run tests' <<<"$job" | head -1 | cut -d: -f1)
[ "$inst" -lt "$must" ] || fail "the client install comes after the Must-run step"
instblock=$(awk '/^      - name: Install PostgreSQL client/{f=1;print;next} f&&/^      - name:/{exit} f' "$ci")
grep -qF 'matrix.pg' <<<"$instblock" || fail "the client install does not use matrix.pg"
grep -qF '/usr/lib/postgresql/' <<<"$instblock" || fail "the client install does not put /usr/lib/postgresql/<major>/bin on PATH"

# 3. local gate
gate=$(awk '/^run_must\(\)/{f=1;print;next} f&&/^}/{print;exit} f' "$ROOT/scripts/integration-gate.sh")
grep -qF 'must-run-check.sh' <<<"$gate" || fail "integration-gate.sh run_must does not call must-run-check.sh"
echo "ci-must-run tests ok"
