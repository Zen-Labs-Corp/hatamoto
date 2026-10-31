#!/usr/bin/env bash
# test-floor.sh - the installer floor (MIN_CORE_VERSION) is a listed, non-yanked,
# non-halted release in the tracked stable channel source. Offline, needs jq only.
# Bump the floor only after that release is on stable: a floor bump PR is red until then.
#   test-floor.sh [--floor-file FILE] [--channel FILE]
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
floor_file="$here/MIN_CORE_VERSION"
channel="$repo/releases/channels/stable.json"
while [ $# -gt 0 ]; do
  case "$1" in
    --floor-file) floor_file="${2:?--floor-file needs a file}"; shift 2 ;;
    --channel) channel="${2:?--channel needs a file}"; shift 2 ;;
    *) echo "usage: $0 [--floor-file FILE] [--channel FILE]" >&2; exit 2 ;;
  esac
done

# check_floor FLOOR_FILE CHANNEL prints the reason and fails when the floor is not usable.
check_floor() {
  local f="$1" c="$2" v
  [ -s "$f" ] || { echo "floor file $f is missing or empty"; return 1; }
  [ "$(wc -l < "$f")" -le 1 ] || { echo "floor file $f must hold one line"; return 1; }
  v="$(cat "$f")"
  printf '%s' "$v" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo "floor '$v' is not vX.Y.Z"; return 1; }
  jq -e --arg v "$v" '[.releases[]? | select(.version == $v)] | length == 1' "$c" >/dev/null 2>&1 ||
    { echo "$v is not listed exactly once in $c"; return 1; }
  if jq -e --arg v "$v" 'any(.releases[]?; .version == $v and .halted == true)' "$c" >/dev/null 2>&1; then
    echo "$v is halted in $c"; return 1
  fi
  if jq -e --arg v "$v" 'any(.yanked[]?; .version == $v)' "$c" >/dev/null 2>&1; then
    echo "$v is yanked in $c"; return 1
  fi
}

command -v jq >/dev/null || { echo "FAIL: jq is required" >&2; exit 1; }

if [ "${TEST_FLOOR_SELF:-1}" = 1 ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  v=v1.2.3
  printf '%s\n' "$v" > "$tmp/ok"
  printf '{"releases":[{"version":"v1.2.2"},{"version":"%s","halted":false}],"yanked":[{"version":"v1.2.2"}]}\n' "$v" > "$tmp/c-ok.json"
  check_floor "$tmp/ok" "$tmp/c-ok.json" >/dev/null || { echo "FAIL: self-test baseline rejected"; exit 1; }
  expect_fail() { # expect_fail NAME FLOOR_FILE CHANNEL
    if check_floor "$2" "$3" >/dev/null; then echo "FAIL: accepted a floor that is $1" >&2; exit 1; fi
  }
  jq '(.releases[] | select(.version == "v1.2.3") | .halted) = true' "$tmp/c-ok.json" > "$tmp/c-halted.json"
  jq --arg v "$v" '.yanked = [{version: $v}]' "$tmp/c-ok.json" > "$tmp/c-yanked.json"
  jq '.releases = []' "$tmp/c-ok.json" > "$tmp/c-empty.json"
  jq '.releases += [{version: "v1.2.3"}]' "$tmp/c-ok.json" > "$tmp/c-twice.json"
  printf 'v9.9.9\n' > "$tmp/unlisted"; printf '1.0.2\n' > "$tmp/bare"; : > "$tmp/empty"; printf 'v1.0\n' > "$tmp/short"
  printf 'v1.0.2\nv1.0.3\n' > "$tmp/multi"
  expect_fail halted "$tmp/ok" "$tmp/c-halted.json"
  expect_fail yanked "$tmp/ok" "$tmp/c-yanked.json"
  expect_fail unlisted "$tmp/ok" "$tmp/c-empty.json"
  expect_fail "listed twice" "$tmp/ok" "$tmp/c-twice.json"
  expect_fail "not in the channel" "$tmp/unlisted" "$tmp/c-ok.json"
  expect_fail "missing the v" "$tmp/bare" "$tmp/c-ok.json"
  expect_fail empty "$tmp/empty" "$tmp/c-ok.json"
  expect_fail "two lines" "$tmp/multi" "$tmp/c-ok.json"
  expect_fail "not vX.Y.Z" "$tmp/short" "$tmp/c-ok.json"
  expect_fail "a missing file" "$tmp/nope" "$tmp/c-ok.json"
  echo "ok: floor self-test"
fi

if ! reason="$(check_floor "$floor_file" "$channel")"; then
  echo "FAIL: $reason" >&2
  exit 1
fi
echo "ok: test-floor ($(cat "$floor_file") is on the stable channel)"
