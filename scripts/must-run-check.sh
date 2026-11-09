#!/usr/bin/env bash
# must-run-check.sh <log> <Test>... - the must-run rule, shared by CI and the local gate.
# The log is `go test -v` output. Each named test must print a top-level "--- PASS" line, and
# neither it nor any of its subtests may print "--- SKIP": a parent that passes while every
# subtest skipped (no database, no matching client tools) counts as not run.
set -euo pipefail
log=$1; shift
rc=0
for t in "$@"; do
  grep -q -e "^--- PASS: $t " "$log" || { echo "must-run test missing or not passing: $t" >&2; rc=1; }
  if grep -q -E "^ *--- SKIP: $t([/ ])" "$log"; then echo "must-run test or subtest skipped: $t" >&2; rc=1; fi
done
exit $rc
