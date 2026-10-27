#!/usr/bin/env bash
# test-check-channel-freshness.sh - scripts/check-channel-freshness.sh against
# file:// manifests with generated dates. No network. Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
S=$PWD/scripts/check-channel-freshness.sh
bash -n "$S"
command -v jq >/dev/null || { echo "SKIP: jq not installed"; exit 0; }
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }
manifest() { # manifest CHANNEL DAYS_FROM_NOW
  printf '{"index":{"channel":"%s","issued_at":"x","expires_at":"%s","releases":[]},"signature":"x"}\n' "$1" "$(date -u -d "now + $2 days" +%Y-%m-%dT%H:%M:%SZ)" > "$W/$1.json"; }
run() { bash "$S" --base "file://$W" "$@" 2>&1; }

for c in canary edge stable; do manifest "$c" 13; done
out=$(run) || fail "fresh manifests failed: $out"
[ "$(grep -c '^ok ' <<<"$out")" = 3 ] || fail "want three ok lines: $out"
pass "all three fresh (13 days left): ok"

manifest edge 6
if out=$(run); then fail "6 days left must fail: $out"; fi
grep -q '::error title=channel edge expires soon' <<<"$out" || fail "no loud error for edge: $out"
grep -q 'ok   canary' <<<"$out" || fail "canary was not reported: $out"
grep -q 'ok   stable' <<<"$out" || fail "stable was not reported: $out"
pass "one channel at 6 days left: fails loudly, the others still reported"

manifest edge 8
run >/dev/null || fail "8 days left must pass"
pass "8 days left: ok (limit is 7)"
if out=$(run --min-days 9 edge); then fail "--min-days 9 with 8 left must fail: $out"; fi
pass "--min-days is honoured"

manifest stable -1
if out=$(run stable); then fail "an expired manifest must fail: $out"; fi
grep -q 'expires soon' <<<"$out" || fail "expired not reported: $out"
pass "expired manifest fails"

rm "$W/canary.json"
if out=$(run canary); then fail "a missing manifest must fail: $out"; fi
grep -q 'cannot fetch' <<<"$out" || fail "missing not reported: $out"
printf '{"index":{"channel":"edge","expires_at":""}}\n' > "$W/edge.json"
if out=$(run edge); then fail "an empty expires_at must fail: $out"; fi
grep -q 'no readable expires_at' <<<"$out" || fail "empty expires_at not reported: $out"
printf 'not json' > "$W/edge.json"
if run edge >/dev/null; then fail "garbage must fail"; fi
pass "missing, empty and garbage manifests fail"

if bash "$S" --bogus >/dev/null 2>&1; then fail "unknown flag accepted"; fi
if bash "$S" --min-days x >/dev/null 2>&1; then fail "non-numeric --min-days accepted"; fi
if bash "$S" "../x" >/dev/null 2>&1; then fail "path-like channel accepted"; fi
pass "bad usage refused"
echo "all channel-freshness tests passed"
