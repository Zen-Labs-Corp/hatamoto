#!/usr/bin/env bash
# test-bump-compose-image.sh - offline test of scripts/bump-compose-image.sh. Run by `make check`.
set -euo pipefail
S="$(git rev-parse --show-toplevel)/scripts/bump-compose-image.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
F=$T/compose.yml
mk() {
  cat >"$F" <<'Y'
services:
  db:
    image: postgres:16
  hatamoto:
    image: ghcr.io/zen-labs-corp/hatamoto:v1.0.0
  mailpit:
    image: axllent/mailpit:v1.34
Y
}
mk; cp "$F" "$T/orig"
bash "$S" v1.4.6 "$F" >/dev/null || fail "bump to v1.4.6 failed"
grep -qxF '    image: ghcr.io/zen-labs-corp/hatamoto:v1.4.6' "$F" || fail "image line not rewritten"
[[ $(diff "$T/orig" "$F" | grep -c '^>') == 1 ]] || fail "more than one line changed"
cp "$F" "$T/once"
bash "$S" v1.4.6 "$F" >/dev/null || fail "second run failed"
cmp -s "$T/once" "$F" || fail "second run changed the file"
for bad in 1.4.6 v1.4 v1.4.6-rc.1 v01.4.6 vX.Y.Z; do
  mk; rc=0; bash "$S" "$bad" "$F" >/dev/null 2>&1 || rc=$?
  [[ $rc == 2 ]] || fail "$bad: exit $rc, want 2"
  cmp -s "$T/orig" "$F" || fail "$bad: file changed"
done
grep -v 'hatamoto:v1' "$T/orig" >"$F"; cp "$F" "$T/zero"
rc=0; bash "$S" v1.4.6 "$F" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "zero image lines: exit $rc, want 1"
cmp -s "$T/zero" "$F" || fail "zero image lines: file changed"
mk; echo '    image: ghcr.io/zen-labs-corp/hatamoto:v1.1.0' >>"$F"; cp "$F" "$T/two"
rc=0; bash "$S" v1.4.6 "$F" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "two image lines: exit $rc, want 1"
cmp -s "$T/two" "$F" || fail "two image lines: file changed"
echo "bump-compose-image tests ok"
