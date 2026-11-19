#!/usr/bin/env bash
# test-ship-health.sh - health() in scripts/ship.sh fails on records the server itself wrote
# (panic-recovered log records, runtime panic/fatal lines) and ignores request lines that
# merely carry those words in a logged path or user agent.
# SHIP_SH overrides the script under test.
set -Eeuo pipefail
top=$(git rev-parse --show-toplevel)
ship=${SHIP_SH:-$top/scripts/ship.sh}
bash -n "$ship"
eval "$(sed -n '/^PANIC_RE=/p' "$ship")"
eval "$(sed -n '/^health() {/,/^}/p' "$ship")"
sleep() { :; }
ssh() { case "$*" in *journalctl*) echo "$*" >> "$ARGS"; cat "$JOURNAL";; *) return 0;; esac; }
cell_psql() { return 0; }
curl() { case "$*" in *%{http_code}*) echo 200;; *) echo ok;; esac; }
ARGS=$(mktemp) JOURNAL=$(mktemp)
trap 'rm -f "$ARGS" "$JOURNAL"' EXIT
SLUG=x DEPLOY_HOST=h SITE_URL=http://x DB=d
fails=0
check() { # name want(0 healthy|1 unhealthy) cell journal-line
  local rc=0
  : > "$ARGS"; printf '%s\n' "$4" > "$JOURNAL"
  CELL=$3
  health "2026-11-19 11:00:00" >/dev/null 2>&1 || rc=1
  if [[ "$rc" != "$2" ]]; then echo "FAIL: $1 (cell=$3): want rc $2 got $rc" >&2; fails=1
  elif ! grep -q -- '-o cat' "$ARGS"; then echo "FAIL: $1 (cell=$3): journalctl without -o cat" >&2; fails=1
  else echo "ok: $1 (cell=$3)"; fi
}
ts='time=2026-11-19T11:02:33.130+11:00'
for cell in 0 1; do
  check "request for /panic"        0 $cell "$ts level=INFO msg=request method=GET path=/panic status=404"
  check "user agent FATAL"          0 $cell "$ts level=INFO msg=request method=GET path=/ user_agent=FATAL status=200"
  check "text record text in path"  0 $cell "$ts level=INFO msg=request method=GET path=\"/x\\n$ts level=ERROR msg=\\\"panic recovered\\\"\" status=404"
  check "json record text in path"  0 $cell '{"time":"2026-11-19T11:02:33Z","level":"INFO","msg":"request","path":"/x\n{\"time\":\"2026-11-19T11:02:33Z\",\"level\":\"ERROR\",\"msg\":\"panic recovered\"}"}'
  check "text panic record"         1 $cell "$ts level=ERROR msg=\"panic recovered\" error=boom method=GET path=/x"
  check "json panic record"         1 $cell '{"time":"2026-11-19T11:02:33Z","level":"ERROR","msg":"panic recovered","error":"boom"}'
  check "runtime panic line"        1 $cell "panic: runtime error: index out of range"
  check "runtime fatal line"        1 $cell "fatal error: concurrent map writes"
done
exit $fails
