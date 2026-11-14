#!/usr/bin/env bash
# test-ship-preflight.sh - site_state_health (scripts/ship.sh preflight) reads STATE
# and HEALTH of `hmctl site status` by header name, whatever columns follow HEALTH.
set -Eeuo pipefail
top=$(git rev-parse --show-toplevel)
bash -n "$top/scripts/ship.sh"
eval "$(sed -n '/^site_state_health() {/,/^}/p' "$top/scripts/ship.sh")"
fails=0
check() { # name slug want table
  local got; got=$(site_state_health "$2" <<< "$4")
  if [[ "$got" == "$3" ]]; then echo "ok: $1"; else echo "FAIL: $1: want '$3' got '$got'" >&2; fails=1; fi
}
H='SLUG          STATE    PLAN    MEM   CPU   VERSION          CHANNEL  UPDATE      HEALTH  BACKUP'
tail='DB   conns 46/100 ...'
check "with BACKUP" hatamoto-dev "running ok" "$H
hatamoto-dev  running  medium  221M  0.1%  sha256-4040dae8  canary   up to date  ok      2h
$tail"
check "without BACKUP" hatamoto-dev "running ok" "SLUG          STATE    PLAN    MEM   CPU   VERSION          CHANNEL  UPDATE      HEALTH
hatamoto-dev  running  medium  221M  0.1%  sha256-4040dae8  canary   up to date  ok
$tail"
check "extra trailing column" hatamoto-dev "running ok" "$H  NOTE
hatamoto-dev  running  medium  221M  0.1%  sha256-4040dae8  canary   up to date  ok      2h      x y
$tail"
check "degraded" hatamoto-dev "running degraded" "$H
hatamoto-dev  running  medium  221M  0.1%  sha256-4040dae8  canary   up to date  degraded  2h
$tail"
check "stopped" hatamoto-dev "stopped ok" "$H
hatamoto-dev  stopped  medium  221M  0.1%  sha256-4040dae8  canary   up to date  ok      2h
$tail"
check "slug missing" other "" "$H
hatamoto-dev  running  medium  221M  0.1%  sha256-4040dae8  canary   up to date  ok      2h
$tail"
check "no HEALTH header" hatamoto-dev "" "SLUG STATE PLAN
hatamoto-dev running medium"
exit $fails
