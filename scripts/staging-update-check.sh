#!/usr/bin/env bash
# staging-update-check.sh - does a fresh PRODUCTION box update itself from the
# real release site? (plan v1-25f D-6; RUNBOOK §2 step 7 and §4 halt drill)
#
# Two kinds of box, each with a fresh database/volume, data dir and random
# install_id, on channel edge (or --channel), talking to the real https://hatamoto.dev/releases:
#   a  a systemd --user unit made from the SHIPPED deploy/systemd/hatamoto-api.service
#   b  the Docker image built from ./Dockerfile (Postgres sidecar, /data volume)
# The binary is a production build: no `hatamoto_testkeys` tag, no HATAMOTO_TEST_*
# variable, no HATAMOTO_UPDATE_URL, so the embedded keys and the default release
# URL are what is tested. It is stamped with --bootstrap-version (default
# v0.0.0-dev.1), which must be older than --expect, so the release counts as an
# update. It is NOT taken from scripts/version.sh: from a checkout after the tag
# that gives a version newer than the release and nothing would update.
#
# Normal mode waits (up to --timeout, default 1500 s; the first check runs 1-10
# minutes after start) until the box runs --expect by itself, then checks the
# health gate, / and the service. It fails early when the box gets stuck
# (stuck_reason: switched to another release, or a check ended up to date,
# blocked, major, rolled back, failed...), since the next check is hours away. --halt is the halt drill: the box must NOT
# download --expect (the release is halted on the channel) within the window, AND
# must prove it checked: a fresh last check (after the script started, result ok)
# whose status is "up to date" or names the halt, plus no releases/<expect> staged
# on the box. A box that never checked, or whose check failed, fails the drill.
#
# Usage:   [STAGING_SERIAL=1] scripts/staging-update-check.sh --expect vX.Y.Z [--box a|b|both] [--channel canary|edge|stable] [--halt] [--timeout SECONDS] [--bootstrap-version vX.Y.Z-dev.N]
# Needs:   go, python3, curl, psql with DATABASE_URL (a role that can CREATE
#          DATABASE) for box a, systemctl --user (a), docker with the postgres:16
#          image (b; through `sg docker -c` when this shell lacks the group),
#          network access to the release site. Not part of `make check` or CI.
# Output:  one PASS:/FAIL: line per assertion, then the raw `update status`,
#          supervisor events and state, then `STAGING RESULT: PASS|FAIL`.
#          Env files are never printed, only variable names. Exit non-zero on FAIL.
#
# Helpers are COPIED, not sourced, from scripts/e2e-update-agent.sh (which runs
# at top level): pass/fail/check/die, dk, cleanup, free_port, scratch_db,
# wait_until/wait_code, shipped_unit, box_up_a/box_down_a, box_up_b/box_down_b,
# box_read/box_state/state_field/box_current, box_cli, gate_passed,
# boot_window_over. Fixes there are not picked up here; diff them when they change.
set -euo pipefail

EXPECT="" BOXES="a b" HALT=0 CHAN=edge TIMEOUT=1500 DEV_VERSION=v0.0.0-dev.1
usage() { grep '^# Usage:' "$0" | cut -c3- >&2; exit 64; }
ORIG_ARGS=("$@")
while [ $# -gt 0 ]; do
  case "$1" in
    --expect) EXPECT="${2:-}"; shift 2 ;;
    --box) case "${2:-}" in a) BOXES=a ;; b) BOXES=b ;; both) BOXES="a b" ;; *) echo "--box wants a, b or both" >&2; exit 64 ;; esac; shift 2 ;;
    --channel) case "${2:-}" in canary|edge|stable) CHAN="$2" ;; *) echo "--channel wants canary, edge or stable" >&2; exit 64 ;; esac; shift 2 ;;
    --halt) HALT=1; shift ;;
    --timeout) TIMEOUT="${2:-}"; shift 2 ;;
    --bootstrap-version) DEV_VERSION="${2:-}"; shift 2 ;;
    -h|--help) sed -n '2,/^# Helpers/p' "$0" | sed '$d'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage ;;
  esac
done
# Same shape as canonicalVersion in backend/internal/release/release.go.
SEMVER='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$'
[ -n "$EXPECT" ] || { echo "--expect vX.Y.Z is required" >&2; usage; }
[[ "$EXPECT" =~ $SEMVER ]] || { echo "--expect: not a version: $EXPECT" >&2; exit 64; }
[[ "$TIMEOUT" =~ ^[0-9]+$ ]] || { echo "--timeout wants seconds" >&2; exit 64; }
HALT_WINDOW=$TIMEOUT; [ "$HALT_WINDOW" -le 900 ] || HALT_WINDOW=900
[[ "$DEV_VERSION" =~ $SEMVER ]] || { echo "--bootstrap-version: not a version: $DEV_VERSION" >&2; exit 64; }
# semver precedence: succeeds when $1 sorts strictly below $2
version_older() {
  python3 - "$1" "$2" <<'PY'
import sys
def key(v):
    core, _, pre = v[1:].partition("-")
    c = tuple(int(x) for x in core.split("."))
    if not pre:
        return (c, 1, ())
    return (c, 0, tuple((0, int(i), "") if i.isdigit() else (1, 0, i) for i in pre.split(".")))
sys.exit(0 if key(sys.argv[1]) < key(sys.argv[2]) else 1)
PY
}
command -v python3 >/dev/null && ! version_older "$DEV_VERSION" "$EXPECT" \
  && { echo "--bootstrap-version $DEV_VERSION is not older than --expect $EXPECT: the box would never update" >&2; exit 64; }
want_box() { case " $BOXES " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

die() { echo "STAGING PREFLIGHT: $*" >&2; exit 2; }
DOCKER_MODE=""
dk() {
  if [ "$DOCKER_MODE" = sg ]; then sg docker -c "$(printf '%q ' docker "$@")"; else docker "$@"; fi
}

# ---- preflight (before anything is created) ------------------------------------
command -v go >/dev/null || die "go is not on PATH"
command -v python3 >/dev/null || die "python3 is not on PATH"
command -v curl >/dev/null || die "curl is not on PATH"
if want_box a; then
  command -v psql >/dev/null || die "psql is not on PATH"
  command -v systemctl >/dev/null || die "systemctl is not on PATH"
  [ -n "${DATABASE_URL:-}" ] || die "DATABASE_URL is not set (a role that can CREATE DATABASE)"
  psql "$DATABASE_URL" -qc 'select 1' >/dev/null 2>&1 || die "psql cannot connect with DATABASE_URL"
  systemctl --user show-environment >/dev/null 2>&1 || die "systemctl --user is not usable"
fi
if want_box b; then
  command -v docker >/dev/null || die "docker is not on PATH"
  if docker info >/dev/null 2>&1; then DOCKER_MODE=plain
  elif sg docker -c 'docker info' >/dev/null 2>&1; then DOCKER_MODE=sg
  else die "docker is not usable for this user (docker info fails, also through sg docker)"; fi
  dk image inspect postgres:16 >/dev/null 2>&1 || die "image postgres:16 is not available locally"
fi
curl -fsS -o /dev/null --max-time 15 https://hatamoto.dev/releases/channels/$CHAN.json || die "the release site is not reachable"

# Both boxes: run a and b at the same time (each waits minutes for its first check,
# so serial doubles the wall time). Each child has its own ID, temp dir, ports,
# database and containers. STAGING_SERIAL=1 keeps the old one-after-the-other run.
if [ "$BOXES" = "a b" ] && [ -z "${STAGING_SERIAL:-}" ]; then
  mkdir -p "${STAGING_TMP:-$HOME/hatamoto-staging-tmp}"
  L="$(mktemp -d "${STAGING_TMP:-$HOME/hatamoto-staging-tmp}/hm-stg-par.XXXXXX")"
  echo "== boxes a and b run in parallel (logs: $L/a.log, $L/b.log; STAGING_SERIAL=1 runs them in turn)"
  STAGING_CHILD=1 bash "$0" "${ORIG_ARGS[@]}" --box a >"$L/a.log" 2>&1 & PA=$!
  STAGING_CHILD=1 bash "$0" "${ORIG_ARGS[@]}" --box b >"$L/b.log" 2>&1 & PB=$!
  trap 'kill "$PA" "$PB" 2>/dev/null; wait; exit 130' INT TERM
  RA=0 RB=0
  wait "$PA" || RA=$?
  wait "$PB" || RB=$?
  for x in a b; do echo; echo "==== box $x"; cat "$L/$x.log"; done
  echo
  if [ "$RA" = 0 ] && [ "$RB" = 0 ]; then echo "STAGING RESULT: PASS"; exit 0; fi
  echo "STAGING RESULT: FAIL (box a exit $RA, box b exit $RB)"; exit 1
fi

ROOT="$(cd "${0%/*}/.." && pwd)"
ID="$$"
# snap-packaged docker cannot see the host /tmp nor hidden dirs under $HOME, so stage in a plain dir under $HOME
S_ROOT="${STAGING_TMP:-$HOME/hatamoto-staging-tmp}"; mkdir -p "$S_ROOT"
S="$(mktemp -d "$S_ROOT/hm-stg.XXXXXX")"
PKG=hatamoto.dev/core/app
FAILS=0
UNITS=() UNITFILES=() CONTAINERS=() NETWORKS=() VOLUMES=() IMAGES=() DBS=() PIDS=()

pass() { echo "PASS: $1 ($2)"; }
fail() { echo "FAIL: $1 ($2)"; FAILS=$((FAILS + 1)); }
check() { # check <what> <detail> <command...>
  local what="$1" detail="$2"; shift 2
  if "$@"; then pass "$what" "$detail"; else fail "$what" "$detail"; fi
}

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  set +e
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null; done
  for u in "${UNITS[@]:-}"; do
    [ -n "$u" ] && { systemctl --user stop "$u" 2>/dev/null; systemctl --user disable "$u" 2>/dev/null; systemctl --user reset-failed "$u" 2>/dev/null; }
  done
  [ "${#UNITFILES[@]}" = 0 ] || systemctl --user daemon-reload 2>/dev/null
  if [ -n "$DOCKER_MODE" ]; then
    for c in "${CONTAINERS[@]:-}"; do [ -n "$c" ] && dk rm -f -v "$c" >/dev/null 2>&1; done
    for n in "${NETWORKS[@]:-}"; do [ -n "$n" ] && dk network rm "$n" >/dev/null 2>&1; done
    for v in "${VOLUMES[@]:-}"; do [ -n "$v" ] && dk volume rm -f "$v" >/dev/null 2>&1; done
    for i in "${IMAGES[@]:-}"; do [ -n "$i" ] && dk image rm -f "$i" >/dev/null 2>&1; done
  fi
  for d in "${DBS[@]:-}"; do [ -n "$d" ] && psql "$DATABASE_URL" -qc "DROP DATABASE IF EXISTS $d WITH (FORCE)" >/dev/null 2>&1; done
  rm -rf "$S"
  exit $rc
}
trap cleanup EXIT INT TERM

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }

wait_code() { # wait_code <url> <code> <seconds>
  local i
  for i in $(seq 1 "$3"); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$1" 2>/dev/null || true)" = "$2" ] && return 0
    sleep 1
  done
  return 1
}
wait_until() { # wait_until <seconds> <command...>
  local end=$((SECONDS + $1)); shift
  while [ "$SECONDS" -lt "$end" ]; do "$@" && return 0; sleep 1; done
  return 1
}

scratch_db() {
  local name="hm_stg_${ID}_$1"
  psql "$DATABASE_URL" -qc "CREATE DATABASE $name" >/dev/null
  DBS+=("$name")
  SCRATCH_DSN="$(python3 - "$DATABASE_URL" "$name" <<'PY'
import sys, urllib.parse as u
p = u.urlparse(sys.argv[1])
print(u.urlunparse(p._replace(path="/" + sys.argv[2])))
PY
)"
}

# ---- the production build ---------------------------------------------------------
[[ "$DEV_VERSION" == *-dev* ]] || die "--bootstrap-version '$DEV_VERSION' is not a dev version"
mkdir -p "$S/bin" "$S/logs"
build_binary() {
  echo "== building the production binary $DEV_VERSION (no test tags)"
  ( cd "$ROOT/backend" && CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags "-X $PKG.Version=$DEV_VERSION -X $PKG.Commit=staging" -o "$S/bin/hatamoto" ./cmd/hatamoto )
  assert_production "$S/bin/hatamoto" "binary"
}
assert_production() { # assert_production <binary> <what>
  if go version -m "$1" | grep -Eq 'hatamoto_testkeys|hatamoto_qa'; then fail "$2 is a production build" "a test tag (hatamoto_testkeys or hatamoto_qa) is compiled in"
  else pass "$2 is a production build" "no hatamoto_testkeys or hatamoto_qa build tag"; fi
}

# ---- boxes --------------------------------------------------------------------------
BOX="" BOX_URL="" BOX_PORT="" BOX_UNIT="" BOX_CONT="" BOX_DATA="" BOX_LOG="" BOX_TAG="" BOX_SIDE="" BOX_PG="" BOX_VOL="" BOX_NET=""

box_read() { # box_read <path below the data dir>
  case "$BOX" in
    a) cat "$BOX_DATA/$1" 2>/dev/null ;;
    b) dk exec "$BOX_CONT" cat "/data/$1" 2>/dev/null ;;
  esac || true
}
box_ls() { # box_ls <dir below the data dir>
  case "$BOX" in
    a) ls "$BOX_DATA/$1" 2>/dev/null ;;
    b) dk exec "$BOX_CONT" ls "/data/$1" 2>/dev/null ;;
  esac || true
}
box_state() { box_read releases/state.json; }
state_field() { # state_field <python expression over s>
  box_state | python3 -c "import json,sys
try:
    s=json.load(sys.stdin)
except Exception:
    sys.exit(1)
print($1)" 2>/dev/null
}
box_current() {
  case "$BOX" in
    a) readlink "$BOX_DATA/releases/current" 2>/dev/null ;;
    b) dk exec "$BOX_CONT" readlink /data/releases/current 2>/dev/null ;;
  esac || true
}
box_logs() {
  case "$BOX" in
    a) tail -n "$1" "$BOX_LOG" 2>/dev/null ;;
    b) dk logs --tail "$1" "$BOX_CONT" 2>&1 ;;
  esac || true
}
# box_cli <args...>: the hatamoto CLI with the box's environment and data dir
box_cli() {
  case "$BOX" in
    a) ( while IFS= read -r line; do [ -n "$line" ] && export "${line%%=*}=${line#*=}"; done < "$S/$BOX_TAG/hatamoto.env"
         export HATAMOTO_DATA="$BOX_DATA"
         "$S/bin/hatamoto" "$@" 2>&1 ) ;;
    b) dk exec "$BOX_CONT" hatamoto "$@" 2>&1 ;;
  esac
}

# shipped_unit <out> <data> <workdir> <envfile> <exe> <logfile>: the shipped unit
# with only paths, user and port-related lines changed.
shipped_unit() {
  python3 - "$ROOT/deploy/systemd/hatamoto-api.service" "$@" <<'PY'
import sys
src, out, data, workdir, envfile, exe, log = sys.argv[1:8]
res, changed = [], []
for l in open(src).read().split("\n"):
    k = l.split("=", 1)[0]
    new = l
    if k in ("User", "Group"):
        new = None
    elif k == "WorkingDirectory":
        new = "WorkingDirectory=" + workdir
    elif k == "EnvironmentFile":
        new = "EnvironmentFile=" + envfile
    elif l.startswith("Environment=HATAMOTO_DATA="):
        new = "Environment=HATAMOTO_DATA=" + data
    elif k == "ExecStart":
        if not l.rstrip().endswith(" supervise"):
            sys.exit("shipped ExecStart does not end in 'supervise': " + l)
        new = "ExecStart=" + exe + " supervise"
    elif k == "WantedBy":
        new = "WantedBy=default.target"
    elif k in ("After", "Wants"):
        new = None
    if new != l:
        changed.append(k)
    if new is not None:
        res.append(new)
    if k == "ExecStart":
        res += ["StandardOutput=append:" + log, "StandardError=append:" + log]
text = "\n".join(res)
for must in ("KillMode=mixed", "TimeoutStopSec=60", "Restart=always", "NoNewPrivileges=true"):
    if must not in text:
        sys.exit("shipped unit lost " + must)
open(out, "w").write(text)
print("changed keys:", ",".join(sorted(set(changed))))
PY
}

# Box A env: exactly these names, nothing test-only and no update URL (so the
# default https://hatamoto.dev/releases is used).
box_up_a() {
  BOX=a; BOX_PORT="$(free_port)"; BOX_URL="http://127.0.0.1:$BOX_PORT"
  BOX_DATA="$S/$BOX_TAG/data"; BOX_LOG="$S/logs/box-$BOX_TAG.log"
  mkdir -p "$BOX_DATA" "$S/$BOX_TAG/secrets" "$S/$BOX_TAG/work"; : > "$BOX_LOG"
  scratch_db "a"
  {
    echo "DATABASE_URL=$SCRATCH_DSN"
    echo "API_HOST=127.0.0.1"; echo "API_PORT=$BOX_PORT"
    echo "HATAMOTO_SECRETS_DIR=$S/$BOX_TAG/secrets"
    echo "HATAMOTO_UPDATE_CHANNEL=$CHAN"
  } > "$S/$BOX_TAG/hatamoto.env"
  BOX_UNIT="hm-stg-$ID-a.service"
  local unit="$S/$BOX_TAG/$BOX_UNIT" changed
  changed="$(shipped_unit "$unit" "$BOX_DATA" "$S/$BOX_TAG/work" "$S/$BOX_TAG/hatamoto.env" "$S/bin/hatamoto" "$BOX_LOG")" \
    || { fail "[$BOX_TAG] the unit under test is the shipped one" "$changed"; return 1; }
  pass "[$BOX_TAG] the unit under test is the shipped one" "$changed"
  UNITS+=("$BOX_UNIT"); UNITFILES+=("$unit")
  systemctl --user link "$unit" >/dev/null
  systemctl --user daemon-reload
  systemctl --user start "$BOX_UNIT"
}
box_down_a() {
  systemctl --user stop "$BOX_UNIT" 2>/dev/null || true
  systemctl --user disable "$BOX_UNIT" 2>/dev/null || true
  systemctl --user reset-failed "$BOX_UNIT" 2>/dev/null || true
}

# Box B: network, postgres, the app container from the repo Dockerfile on the
# default bridge network (the real site is reached normally). The database
# address is passed as an IP, so it never needs the embedded DNS.
box_up_b() {
  BOX=b; BOX_PORT="$(free_port)"; BOX_URL="http://127.0.0.1:$BOX_PORT"
  BOX_NET="hm-stg-$ID-$BOX_TAG"; BOX_PG="hm-stg-$ID-pg-$BOX_TAG"
  BOX_CONT="hm-stg-$ID-app-$BOX_TAG"; BOX_VOL="hm-stg-$ID-data-$BOX_TAG"
  NETWORKS+=("$BOX_NET"); CONTAINERS+=("$BOX_PG" "$BOX_CONT"); VOLUMES+=("$BOX_VOL")
  dk network create "$BOX_NET" >/dev/null
  dk volume create "$BOX_VOL" >/dev/null
  dk run -d --name "$BOX_PG" --network "$BOX_NET" \
    -e POSTGRES_USER=hatamoto -e POSTGRES_PASSWORD=hatamoto -e POSTGRES_DB=hatamoto postgres:16 >/dev/null
  local i
  for i in $(seq 1 60); do dk exec "$BOX_PG" pg_isready -U hatamoto >/dev/null 2>&1 && break; sleep 1; done
  sleep 2 # the postgres image restarts once after its init
  for i in $(seq 1 60); do dk exec "$BOX_PG" pg_isready -U hatamoto >/dev/null 2>&1 && break; sleep 1; done
  local pgip; pgip="$(dk inspect -f '{{(index .NetworkSettings.Networks "'"$BOX_NET"'").IPAddress}}' "$BOX_PG")"
  dk run -d --name "$BOX_CONT" --network "$BOX_NET" -p "127.0.0.1:$BOX_PORT:8090" --stop-timeout 45 \
    -v "$BOX_VOL:/data" \
    -e "DATABASE_URL=postgres://hatamoto:hatamoto@$pgip:5432/hatamoto?sslmode=disable" \
    -e API_HOST=0.0.0.0 -e API_PORT=8090 -e HATAMOTO_UPDATE_CHANNEL=$CHAN "hatamoto-staging:$ID" >/dev/null
}
box_down_b() {
  dk rm -f -v "$BOX_CONT" "$BOX_PG" >/dev/null 2>&1 || true
  dk network rm "$BOX_NET" >/dev/null 2>&1 || true
  dk volume rm -f "$BOX_VOL" >/dev/null 2>&1 || true
}

box_up() { BOX="$1"; BOX_TAG="$2"; "box_up_$1"; }
box_down() {
  echo "-- box log tail ($BOX_TAG)"; box_logs 25; echo "-- end of log ($BOX_TAG)"
  "box_down_$BOX"
}

# The env of the box holds no test seam and no update URL (names only are shown).
box_env_names() {
  case "$BOX" in
    a) cut -d= -f1 "$S/$BOX_TAG/hatamoto.env" ;;
    b) dk inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$BOX_CONT" | cut -d= -f1 ;;
  esac
}
assert_box_env() {
  local names; names="$(box_env_names | sort | tr '\n' ' ')"
  check "[$BOX_TAG] the box env has no HATAMOTO_TEST_* variable and no HATAMOTO_UPDATE_URL" "names: $names" \
    bash -c '! grep -Eq "(^| )(HATAMOTO_TEST_[A-Z_]*|HATAMOTO_UPDATE_URL)( |$)" <<<"$1"' _ "$names"
  check "[$BOX_TAG] the box is on channel $CHAN" "HATAMOTO_UPDATE_CHANNEL is set" \
    bash -c 'grep -Eq "(^| )HATAMOTO_UPDATE_CHANNEL( |$)" <<<"$1"' _ "$names"
}

serving() { wait_code "$BOX_URL/" 200 "${1:-120}"; }
# The supervisor switched to $1 and its health gate passed and was kept (as in e2e-update-agent.sh).
gate_passed() {
  [ "$(state_field "s.get('phase')=='idle' and not s.get('pending') and s.get('run')=='release' and s.get('current_seen')=='$1' and not any(b['version']=='$1' for b in (s.get('bad') or []))" || true)" = True ] || return 1
  [ "$(box_current)" = "$1" ] || return 1
  box_read releases/events.jsonl | python3 -c "import json,sys
ev=[json.loads(l) for l in sys.stdin if l.strip()]
sys.exit(0 if any(e.get('to')=='$1' and e.get('phase')=='success' for e in ev) else 1)"
}
boot_window_over() { [ "$(state_field "not s.get('unproven')" || true)" = True ]; }
has_release_dir() { box_ls releases | grep -qx "$EXPECT"; }
no_release_dir() { ! has_release_dir; }
settled() { [ "$(state_field "not s.get('pending') and not s.get('unproven')" || true)" = True ]; }
container_running() { [ "$(dk inspect -f '{{.State.Running}}' "$BOX_CONT")" = true ]; }

raw_report() {
  echo "-- update status ($BOX_TAG)"; box_cli update status || true
  echo "-- supervisor events ($BOX_TAG)"; box_read releases/events.jsonl | tail -n 40
  echo "-- supervisor state ($BOX_TAG)"; box_state; echo
  echo "-- end ($BOX_TAG)"
}

# BEGIN stuck_reason
# stuck_reason <update status text> <current release> <expect>: succeeds (and
# prints why) when the box can no longer reach <expect> by itself within the
# wait: it switched to another release, or its check ended in a decision that
# stays until the next scheduled check (hours away) or needs a human. Transient
# states (no check yet, scheduled, waiting, staged, applying, busy) are not stuck.
stuck_reason() {
  local st="$1" cur="$2" want="$3" line res status
  if [[ "$cur" =~ ^v[0-9] && "$cur" != "$want" && "$cur" != *-dev.* ]]; then echo "the box switched to $cur, not $want"; return 0; fi
  line="$(grep '^last check' <<<"$st" | head -1 || true)"
  read -r _ _ _ res <<<"$line"
  [ "${res:-}" = error ] && { echo "the update check failed: $(grep '^status' <<<"$st" | head -1)"; return 0; }
  status="$(grep '^status' <<<"$st" | head -1 || true)"
  if grep -Eiq '^status +(up to date|blocked|available major|rolled back|backup failed|update failed|off|notify|rollback pending)\b' <<<"$status"; then
    echo "${status#status }" | sed 's/^ *//'; return 0
  fi
  return 1
}
# END stuck_reason

# wait_for_update <seconds>: 0 once the gate passed for $EXPECT, 1 on timeout, 2 when
# the box is stuck (STUCK says why). Status is read every 15 s, the gate every second.
STUCK=""
wait_for_update() {
  local end=$((SECONDS + $1)) next=0
  while [ "$SECONDS" -lt "$end" ]; do
    gate_passed "$EXPECT" && return 0
    if [ "$SECONDS" -ge "$next" ]; then
      next=$((SECONDS + 15))
      if STUCK="$(stuck_reason "$(box_cli update status || true)" "$(box_current)" "$EXPECT")"; then
        gate_passed "$EXPECT" && return 0
        return 2
      fi
    fi
    sleep 1
  done
  return 1
}

run_normal() {
  local tag="$BOX_TAG" ok=0 st rc=0
  check "[$tag] the dev build $DEV_VERSION serves / 200" "$BOX_URL/" serving 180
  echo "-- waiting up to ${TIMEOUT}s for the box to update itself to $EXPECT (stops early if it gets stuck)"
  wait_for_update "$TIMEOUT" || rc=$?
  [ "$rc" = 0 ] && ok=1
  [ "$rc" = 2 ] && echo "-- stopped waiting after ${SECONDS}s: $STUCK"
  check "[$tag] the box moved to $EXPECT by itself and its health gate passed" "current=$(box_current), phase=$(state_field "s.get('phase')" || true) run=$(state_field "s.get('run')" || true)${STUCK:+, stuck: $STUCK}" [ "$ok" = 1 ]
  [ "$ok" = 1 ] || { raw_report; return; }
  check "[$tag] $EXPECT serves / 200" "$BOX_URL/" serving 120
  st="$(box_cli update status)"
  check "[$tag] update status shows the running release $EXPECT" "$(grep '^version' <<<"$st")" grep -Eq "^version +$EXPECT " <<<"$st"
  wait_until 300 boot_window_over || true
  check "[$tag] state.json: nothing pending or unproven" "pending=$(state_field "s.get('pending')" || true) unproven=$(state_field "s.get('unproven')" || true)" settled
  case "$BOX" in
    a) check "[$tag] systemd --user reports the unit active" "$BOX_UNIT" systemctl --user is-active --quiet "$BOX_UNIT" ;;
    b) check "[$tag] the container is running" "$BOX_CONT" container_running ;;
  esac
  raw_report
}

# BEGIN halt_proof
# halt_proof <since-epoch> <update status text>: succeeds only when the box really
# checked the channel after the halt was published (a fresh last check dated at or
# after <since-epoch>, result ok) and the status names the halt or says up to
# date. "never checked", a check error, "no check yet" and a stale check fail.
# Prints the reason on failure.
halt_proof() {
  local since="$1" st="$2" line ts res at status
  line="$(grep '^last check' <<<"$st" | head -1 || true)"
  [ -n "$line" ] || { echo "no last check line"; return 1; }
  read -r _ _ ts res <<<"$line"
  case "$ts" in never|"") echo "the box has never checked"; return 1 ;; esac
  at="$(date -u -d "$ts" +%s 2>/dev/null)" || { echo "unreadable last check time '$ts'"; return 1; }
  [ "$at" -ge "$since" ] || { echo "last check $ts is older than the halt window"; return 1; }
  [ "$res" = ok ] || { echo "the last check was '$res', not ok"; return 1; }
  status="$(grep '^status' <<<"$st" | head -1 || true)"
  grep -Eiq '^status +(up to date|.*halt)' <<<"$status" || { echo "status is '$status', not up to date or halted"; return 1; }
  return 0
}
# END halt_proof

box_status_proof() { halt_proof "$HALT_SINCE" "$(box_cli update status || true)" >/dev/null; }

# --halt: prove the box actually checked the channel and still did not take the
# halted release. Passing because the box never checked, or the check failed,
# proves nothing.
run_halt() {
  local tag="$BOX_TAG" st out bad=0 end why
  check "[$tag] the dev build $DEV_VERSION serves / 200" "$BOX_URL/" serving 180
  echo "-- waiting up to ${HALT_WINDOW}s for the box to check, and NOT download the halted $EXPECT"
  end=$((SECONDS + HALT_WINDOW))
  while [ "$SECONDS" -lt "$end" ]; do
    if has_release_dir || [ "$(box_current)" = "$EXPECT" ]; then bad=1; break; fi
    box_status_proof && break
    sleep 10
  done
  check "[$tag] no $EXPECT release directory and no switch (the box staged no artifact)" "releases: $(box_ls releases | tr '\n' ' ')" [ "$bad" = 0 ]
  st="$(box_cli update status || true)"
  why="$(halt_proof "$HALT_SINCE" "$st" || true)"
  check "[$tag] the box checked after the halt and reports up to date or halted" "${why:-$(grep -E '^(status|last check)' <<<"$st" | tr '\n' ';')}" [ -z "$why" ]
  check "[$tag] the box still runs its dev version" "$(grep '^version' <<<"$st")" grep -Fq "$DEV_VERSION" <<<"$st"
  check "[$tag] update status is not updated, applying or staged" "$(grep '^status' <<<"$st")" \
    bash -c '! grep -Eiq "^status +(updated|applying|staged)" <<<"$1"' _ "$st"
  echo "-- update check by hand ($tag)"
  if out="$(box_cli update check)"; then pass "[$tag] update check by hand exits 0" "ok"; else fail "[$tag] update check by hand exits 0" "it failed"; fi
  echo "$out"; echo "-- end of check"
  check "[$tag] update check by hand fetched nothing" "releases: $(box_ls releases | tr '\n' ' ')" no_release_dir
  why="$(halt_proof "$HALT_SINCE" "$(box_cli update status || true)" || true)"
  check "[$tag] after the hand check the status is a fresh success: up to date or halted" "${why:-ok}" [ -z "$why" ]
  raw_report
}

run_box() { # run_box <a|b>
  local b="$1" tag="$1-stg"
  echo; echo "== box $b: fresh production box on channel $CHAN, real release site"
  HALT_SINCE="$(date -u +%s)" # the halt is published before this script runs, so any check from now on sees it
  box_up "$b" "$tag" || { fail "[$tag] the box started" "see the log"; return; }
  assert_box_env
  if [ "$HALT" = 1 ]; then run_halt; else run_normal; fi
  box_down
}

# ---- main -------------------------------------------------------------------------------
build_binary
if want_box b; then
  echo "== building the image hatamoto-staging:$ID from the Dockerfile (no TAGS)"
  IMAGES+=("hatamoto-staging:$ID")
  dk build -q --build-arg "VERSION=$DEV_VERSION" --build-arg COMMIT=staging -t "hatamoto-staging:$ID" "$ROOT" >/dev/null
  dk run --rm --entrypoint cat "hatamoto-staging:$ID" /usr/local/bin/hatamoto > "$S/bin/image-hatamoto"
  assert_production "$S/bin/image-hatamoto" "the image binary"
fi
for b in $BOXES; do run_box "$b"; done

echo
R="STAGING RESULT"; [ -n "${STAGING_CHILD:-}" ] && R="BOX $BOXES RESULT"
if [ "$FAILS" = 0 ]; then echo "$R: PASS"; else echo "$R: FAIL ($FAILS failed)"; exit 1; fi
