#!/usr/bin/env bash
# test-ci-server-shard.sh - the internal/server shards must partition its integration tests:
# no test in two shards, none lost, none empty. N is read from the ci.yml matrix, and the
# shard count passed to the script there must agree with it (a registered check suite).
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
fail() { echo "FAIL: $*" >&2; exit 1; }
job=$(awk '/^  server-integration:/{f=1;next} f&&/^  [a-z]/{exit} f' "$ROOT/.github/workflows/ci.yml")
[ -n "$job" ] || fail "no server-integration job in ci.yml"
matrix=$(grep -oE 'shard: \[[0-9, ]+\]' <<<"$job" | head -1 || true)
[ -n "$matrix" ] || fail "no shard matrix in the server-integration job"
n=$(grep -oE '[0-9]+' <<<"${matrix#shard:}" | wc -l)
[ "$n" -ge 2 ] || fail "matrix has $n shards"
[ "$(grep -oE '[0-9]+' <<<"${matrix#shard:}" | paste -sd' ')" = "$(seq 1 "$n" | paste -sd' ')" ] || fail "matrix is not 1..$n: $matrix"
grep -qF "ci-server-shard.sh \"\$SHARD\" $n" <<<"$job" || fail "the job does not pass $n as the shard count"
grep -qF "(\${{ matrix.shard }}/$n)" <<<"$job" || fail "the job name does not say /$n"

export GO_QUEUE_FORCE_LOCAL=1
cd "$ROOT/backend"
all=$(go test -tags=integration -list '^Test' ./internal/server | grep -E '^Test[A-Za-z0-9_]*$' | sort)
[ -n "$all" ] || fail "no tests listed"
got=""
for i in $(seq 1 "$n"); do
  s=$(LIST_ONLY=1 bash ../scripts/ci-server-shard.sh "$i" "$n" | grep -E '^Test[A-Za-z0-9_]*$') || fail "shard $i/$n failed or is empty"
  [ -n "$s" ] || fail "shard $i/$n is empty"
  got+="$s"$'\n'
done
dups=$(sort <<<"${got%$'\n'}" | uniq -d)
[ -z "$dups" ] || fail "tests in two shards: $(head -3 <<<"$dups" | paste -sd' ')"
diff <(sort <<<"${got%$'\n'}") <(echo "$all") >/dev/null || fail "the shards' union differs from the test list"
echo "ci-server-shard tests ok ($n shards, $(wc -l <<<"$all") tests)"
