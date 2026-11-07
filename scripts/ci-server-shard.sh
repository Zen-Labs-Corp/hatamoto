#!/usr/bin/env bash
# ci-server-shard.sh - run one hash bucket of the internal/server integration tests.
# usage (from backend/): ci-server-shard.sh <shard 1..n> <n>
# A test lands in the bucket cksum(name) % n + 1, so adding a test never moves another.
# LIST_ONLY=1 prints the bucket's test names, one per line, and runs nothing.
set -euo pipefail
i=${1:?usage: ci-server-shard.sh <shard 1..n> <n>}
n=${2:?usage: ci-server-shard.sh <shard 1..n> <n>}
{ [ "$i" -ge 1 ] && [ "$i" -le "$n" ]; } 2>/dev/null || { echo "bad shard $i/$n" >&2; exit 2; }
names=$(go test -tags=integration -list '^Test' ./internal/server | grep -E '^Test[A-Za-z0-9_]*$' | sort)
mine=$(while read -r t; do
  if [ $(( $(printf %s "$t" | cksum | cut -d' ' -f1) % n + 1 )) = "$i" ]; then echo "$t"; fi
done <<<"$names")
[ -n "$mine" ] || { echo "shard $i/$n is empty" >&2; exit 1; }
if [ "${LIST_ONLY:-}" = 1 ]; then echo "$mine"; exit 0; fi
re="^($(paste -sd'|' <<<"$mine"))\$"
exec go test -tags=integration -count=1 -timeout 30m -run "$re" ./internal/server
