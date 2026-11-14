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
# table <row>...: cells split by |, laid out as hmctl's status.Format does (every cell padded
# to the widest in its column + 2, last cell unpadded)
table() {
  printf '%s\n' "$@" | awk -F'|' '{ for (i = 1; i <= NF; i++) { c[NR, i] = $i; if (length($i) > w[i]) w[i] = length($i) } n = NF }
    END { for (r = 1; r <= NR; r++) { line = ""
            for (i = 1; i <= n; i++) line = line (i < n ? sprintf("%-" w[i] + 2 "s", c[r, i]) : c[r, i])
            print line } }'
}
H='SLUG|STATE|PLAN|MEM|CPU|VERSION|CHANNEL|UPDATE|HEALTH|BACKUP'
R='hatamoto-dev|running|medium|221M|0.1%|sha256-4040dae8|canary|up to date|ok|2h'
tail='DB   conns 46/100 ...'
# header and rows verbatim from hatamoto-io internal/status TestFormatGolden (wide UPDATE cell,
# "27h !" BACKUP); `site status <slug>` prints a single row, so one row per table
G='SLUG  STATE      PLAN    MEM   CPU   VERSION  CHANNEL  UPDATE                                         HEALTH  BACKUP'
check "golden t2 row" t2 "running ok" "$G
t2    running    medium  175M  0.2%  v1.4.2   edge     v1.4.3 failed /ready, rolled back 11-13 03:04  ok      27h !
$tail"
check "golden t3 row" t3 "suspended -" "$G
t3    suspended  small   -     -     v1.4.2   stable   -                                              -       5h
$tail"
check "with BACKUP" hatamoto-dev "running ok" "$(table "$H" "$R")
$tail"
check "without BACKUP" hatamoto-dev "running ok" "$(table "${H%|BACKUP}" "${R%|2h}")
$tail"
check "extra trailing column" hatamoto-dev "running ok" "$(table "$H|NOTE" "$R|x y")
$tail"
check "degraded" hatamoto-dev "running degraded" "$(table "$H" "${R/|ok|/|degraded|}")
$tail"
check "stopped" hatamoto-dev "stopped ok" "$(table "$H" "${R/running/stopped}")
$tail"
check "slug missing" other "" "$(table "$H" "$R")
$tail"
check "no HEALTH header" hatamoto-dev "" "SLUG STATE PLAN
hatamoto-dev running medium"
exit $fails
