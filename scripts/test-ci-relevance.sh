#!/usr/bin/env bash
# test-ci-relevance.sh - offline test of scripts/ci-relevance.sh (a registered check suite).
set -euo pipefail
S="$(git rev-parse --show-toplevel)/scripts/ci-relevance.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
g() { git -C "$T" -c commit.gpgsign=false -c core.hooksPath=/dev/null -c user.name=T -c user.email=t@example.com "$@"; }
g init -q -b main
mkdir -p "$T/app" "$T/docs"
echo a > "$T/app/a.txt"; echo a > "$T/docs/a.txt"
g add -A; g commit -q -m base; base=$(g rev-parse HEAD)
echo b > "$T/docs/b.txt"; g add -A; g commit -q -m docs; docs=$(g rev-parse HEAD)
echo b > "$T/app/b.txt"; g add -A; g commit -q -m app; app=$(g rev-parse HEAD)

# run <expected> <desc> VAR=val...
run() {
  local want=$1 desc=$2 got; shift 2
  : > "$T/out"
  (cd "$T" && env -i PATH="$PATH" GITHUB_OUTPUT="$T/out" PATTERN='^app/' LABEL=app "$@" bash "$S" >/dev/null)
  got=$(cat "$T/out")
  [ "$got" = "relevant=$want" ] || fail "$desc: want relevant=$want, got '$got'"
}

run true  "full tier"                   TIER=full FULL_ONLY=1 EVENT_NAME=pull_request BASE_SHA="$base" HEAD_SHA="$docs"
run false "fast tier, full-only job"    TIER=fast FULL_ONLY=1 EVENT_NAME=pull_request BASE_SHA="$base" HEAD_SHA="$app"
run true  "PR touching a match"         TIER=fast EVENT_NAME=pull_request BASE_SHA="$base" HEAD_SHA="$app"
run false "PR touching no match"        TIER=fast EVENT_NAME=pull_request BASE_SHA="$base" HEAD_SHA="$docs"
run true  "push touching a match"       TIER=fast EVENT_NAME=push BEFORE_SHA="$docs" HEAD_SHA="$app"
run false "push touching no match"      TIER=fast EVENT_NAME=push BEFORE_SHA="$base" HEAD_SHA="$docs"
run true  "push, all-zero before"       TIER=fast EVENT_NAME=push BEFORE_SHA=0000000000000000000000000000000000000000 HEAD_SHA="$docs"
run true  "push, empty before"          TIER=fast EVENT_NAME=push BEFORE_SHA= HEAD_SHA="$docs"
run true  "push, unreachable before"    TIER=fast EVENT_NAME=push BEFORE_SHA=1111111111111111111111111111111111111111 HEAD_SHA="$docs"
# A match followed by far more than a pipe buffer of paths: grep -q exits at the first
# match and a piped printf would die of SIGPIPE (pipefail) and report "not relevant".
mkdir -p "$T/big"
for i in $(seq 1 3000); do : > "$T/big/a-very-long-file-name-to-fill-the-pipe-buffer-quickly-$i.txt"; done
g add -A; g commit -q -m big; big=$(g rev-parse HEAD)
run true  "match followed by a large diff" TIER=fast EVENT_NAME=pull_request BASE_SHA="$app" HEAD_SHA="$big" PATTERN='^(big/a-very-long-file-name-to-fill-the-pipe-buffer-quickly-1\.txt)$'
run true  "unknown event"               TIER=fast EVENT_NAME=workflow_dispatch HEAD_SHA="$docs"
echo "ci-relevance tests ok"
