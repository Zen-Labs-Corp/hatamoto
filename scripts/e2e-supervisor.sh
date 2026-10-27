#!/usr/bin/env bash
# e2e-supervisor.sh - two-box end-to-end test of the release supervisor.
#
# Box A runs `hatamoto supervise` as a systemd --user service whose unit is the
# SHIPPED deploy/systemd/hatamoto-api.service, changed only for paths, user and
# port (the script checks that). Box B runs it as the command of the Docker image. On each box, for each fault case
# (exit-after-migrate, root-500), the script starts release v1.0.0 (the
# bootstrap binary), applies a v1.0.1 release that is broken on purpose, and
# checks that the supervisor rolls back by itself, with N serving again within
# 300 s of the apply (measured, not counted in loop turns), that no outage seen
# by the poller (0.25 s samples) lasts 10 s, that v1.0.1 is on the bad list and that applying
# it again is refused. Then it brings a fresh `docker compose` stack up
# on an empty volume and checks `/`, `/admin/setup` and the HEALTHCHECK.
#
# Everything is local: a python http.server stands in for a release site (it is
# only probed once; core fetches nothing). Release keys are throwaway: generated
# at runtime in a temp dir, deleted at exit, trusted through the test-only
# HATAMOTO_TEST_TRUST_DIR seam of binaries built with -tags hatamoto_testkeys.
# The faults are scoped with HATAMOTO_TEST_FAULT=<kind>@v1.0.1 so the bootstrap
# keeps working in the same environment.
#
# The poller samples every 0.25 s (curl gives up after 1 s); an outage is measured
# from the last good sample before it to the first good sample after it.
# Usage:   bash scripts/e2e-supervisor.sh [--only a|b|compose]
# Needs:   go, python3, psql with DATABASE_URL (a role that can CREATE DATABASE),
#          systemctl --user (box A), docker with the postgres:16 image (box B,
#          compose). Docker runs through `sg docker -c` when this shell lacks
#          the group. Not part of `make check` or CI.
# Output:  one PASS:/FAIL: line per assertion, then `E2E RESULT: PASS|FAIL`.
#          The exit code is non-zero on any FAIL.
# Cleanup: units, containers, networks, volumes, the image it built, scratch
#          databases and the temp dir are removed at exit (also on ^C).
set -euo pipefail

ONLY=all
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2 ;;
    -h|--help) sed -n '2,32p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
case "$ONLY" in all|a|b|compose) ;; *) echo "--only wants a, b or compose" >&2; exit 2 ;; esac
want() { [ "$ONLY" = all ] || [ "$ONLY" = "$1" ]; }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ID="$$"
# snap-packaged docker cannot see the host /tmp nor hidden dirs under $HOME, so stage in a plain dir under $HOME
E2E_ROOT="${E2E_TMP:-$HOME/hatamoto-e2e-tmp}"; mkdir -p "$E2E_ROOT"
E2E="$(mktemp -d "$E2E_ROOT/hm-e2e.XXXXXX")"
ARCH="$(cd "$ROOT/backend" && go env GOARCH)"
PKG=hatamoto.dev/core/app
DEADLINE_ROLLBACK=300
MAX_OUTAGE=10
FAILS=0

UNITS=() UNITFILES=() CONTAINERS=() NETWORKS=() VOLUMES=() IMAGES=() DBS=() PROJECTS=() PIDS=()

pass() { echo "PASS: $1 ($2)"; }
fail() { echo "FAIL: $1 ($2)"; FAILS=$((FAILS + 1)); }
check() { # check <what> <detail> <command...>
  local what="$1" detail="$2"; shift 2
  if "$@"; then pass "$what" "$detail"; else fail "$what" "$detail"; fi
}
die() { echo "E2E PREFLIGHT: $*" >&2; exit 2; }

# ---- docker through sg when needed ------------------------------------------
DOCKER_MODE=""
dk() {
  if [ "$DOCKER_MODE" = sg ]; then sg docker -c "$(printf '%q ' docker "$@")"; else docker "$@"; fi
}

# ---- cleanup -----------------------------------------------------------------
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
    for p in "${PROJECTS[@]:-}"; do [ -n "$p" ] && dk compose -p "$p" -f "$E2E/compose/docker-compose.yml" down -v --remove-orphans >/dev/null 2>&1; done
    for c in "${CONTAINERS[@]:-}"; do [ -n "$c" ] && dk rm -f -v "$c" >/dev/null 2>&1; done
    for n in "${NETWORKS[@]:-}"; do [ -n "$n" ] && dk network rm "$n" >/dev/null 2>&1; done
    for v in "${VOLUMES[@]:-}"; do [ -n "$v" ] && dk volume rm -f "$v" >/dev/null 2>&1; done
    for i in "${IMAGES[@]:-}"; do [ -n "$i" ] && dk image rm -f "$i" >/dev/null 2>&1; done
  fi
  for d in "${DBS[@]:-}"; do [ -n "$d" ] && psql "$DATABASE_URL" -qc "DROP DATABASE IF EXISTS $d WITH (FORCE)" >/dev/null 2>&1; done
  rm -rf "$E2E"
  exit $rc
}
trap cleanup EXIT INT TERM

# ---- preflight ----------------------------------------------------------------
command -v go >/dev/null || die "go is not on PATH"
command -v python3 >/dev/null || die "python3 is not on PATH"
command -v curl >/dev/null || die "curl is not on PATH"
[ -n "${DATABASE_URL:-}" ] || die "DATABASE_URL is not set (a role that can CREATE DATABASE)"
psql "$DATABASE_URL" -qc 'select 1' >/dev/null 2>&1 || die "psql cannot connect with DATABASE_URL"
if want a; then
  systemctl --user show-environment >/dev/null 2>&1 || die "systemctl --user is not usable"
fi
if want b || want compose; then
  if docker info >/dev/null 2>&1; then DOCKER_MODE=plain
  elif sg docker -c 'docker info' >/dev/null 2>&1; then DOCKER_MODE=sg
  else die "docker is not usable for this user (docker info fails, also through sg docker)"; fi
  dk image inspect postgres:16 >/dev/null 2>&1 || die "image postgres:16 is not available locally"
fi

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }
now() { date +%s.%N; }

# ---- keys, binaries, release files ------------------------------------------
mkdir -p "$E2E"/{bin/boot,priv,trust,site/releases/v1.0.1,n1,logs}
export HATAMOTO_TEST_TRUST_DIR="$E2E/trust"
LDF() { echo "-X $PKG.Version=$1 -X $PKG.Commit=e2e"; }

build_all() {
  cd "$ROOT/backend"
  echo "== building (tags hatamoto_testkeys)"
  go build -tags hatamoto_testkeys -o "$E2E/bin/release" ./tools/release
  (
    unset HATAMOTO_TEST_TRUST_DIR
    "$E2E/bin/release" keygen --kind release --out "$E2E/priv" >/dev/null
    "$E2E/bin/release" keygen --kind manifest --out "$E2E/priv" >/dev/null
  )
  cp "$E2E/priv/release.pub" "$E2E/trust/release-test.pub"
  cp "$E2E/priv/manifest.pub" "$E2E/trust/manifest.pub"
  CGO_ENABLED=0 go build -tags hatamoto_testkeys -trimpath -ldflags "$(LDF v1.0.0)" -o "$E2E/bin/boot/hatamoto" ./cmd/hatamoto
  CGO_ENABLED=0 go build -tags hatamoto_testkeys -trimpath -ldflags "$(LDF v1.0.1)" -o "$E2E/n1/hatamoto" ./cmd/hatamoto

  local rel="$E2E/site/releases/v1.0.1" p
  for p in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
    if [ "$p" = "linux-$ARCH" ]; then cp "$E2E/n1/hatamoto" "$rel/hatamoto-$p"
    else printf 'not a real %s build\n' "$p" > "$rel/hatamoto-$p"; fi
  done
  "$E2E/n1/hatamoto" version --json > "$E2E/n1/facts.json"
  "$E2E/bin/release" record --version v1.0.1 --kind patch --facts "$E2E/n1/facts.json" \
    --rollback-floor v1.0.0 --min-from v1.0.0 --notes-url https://example.test/notes \
    --dir "$rel" --out "$rel/release.json" >/dev/null
  "$E2E/bin/release" sign-record "$rel/release.json" --key "$E2E/priv/release.key" --out "$rel/release.json.sig" >/dev/null
  cd "$ROOT"
}

serve_site() {
  local port; port="$(free_port)"
  python3 -m http.server --bind 127.0.0.1 --directory "$E2E/site" "$port" >/dev/null 2>&1 &
  PIDS+=("$!")
  local i
  for i in $(seq 1 20); do
    curl -fsS -o /dev/null "http://127.0.0.1:$port/releases/v1.0.1/release.json" 2>/dev/null && { pass "local release site serves the record" "port $port"; return; }
    sleep 0.25
  done
  fail "local release site serves the record" "port $port"
}

# ---- scratch databases ----------------------------------------------------------
# Sets SCRATCH_DSN (not a subshell: the cleanup list must see the new name).
scratch_db() {
  local name="hm_e2e_${ID}_$1"
  psql "$DATABASE_URL" -qc "CREATE DATABASE $name" >/dev/null
  DBS+=("$name")
  SCRATCH_DSN="$(python3 - "$DATABASE_URL" "$name" <<'PY'
import sys, urllib.parse as u
p = u.urlparse(sys.argv[1])
print(u.urlunparse(p._replace(path="/" + sys.argv[2])))
PY
)"
}

# ---- poller and outage maths ------------------------------------------------------
# A sample every 0.25 s (curl gives up after 1 s, so a sample during an outage
# can take up to 1.25 s).
SAMPLE_EVERY=0.25
poll_start() { # poll_start <url> <logfile>; sets POLLPID
  : > "$2"
  ( while :; do
      printf '%s %s\n' "$(now)" "$(curl -s -o /dev/null -w '%{http_code}' --max-time 1 "$1" 2>/dev/null || true)" >> "$2"
      sleep "$SAMPLE_EVERY"
    done ) &
  POLLPID=$!
  PIDS+=("$POLLPID")
}
poll_stop() { kill "$POLLPID" 2>/dev/null || true; wait "$POLLPID" 2>/dev/null || true; }

# prints "<count> <max seconds>". Conservative: an outage runs from the last
# good sample before it to the first good sample after it, so a measured
# duration is never shorter than the real one (to the last sample when it never
# ended). The leading good sample is the previous 200, or the first sample.
outages() {
  awk '
    $2 != "200" { if (!out) { out = 1; start = (seen ? lastgood : $1) } ; last = $1; next }
    { seen = 1; lastgood = $1 }
    out { d = $1 - start; n++; if (d > max) max = d; out = 0 }
    END { if (out) { d = last - start; n++; if (d > max) max = d }
          printf "%d %.1f\n", n, max }' "$1"
}

# ---- box abstraction ------------------------------------------------------------
# A box has: BOX_URL, and functions box_start <case>, box_state, box_apply,
# box_logs <n>, box_stop. State is releases/state.json.
BOX=""; BOX_URL=""; BOX_PORT=""; BOX_UNIT=""; BOX_CONT=""; BOX_DATA=""; BOX_LOG=""

wait_code() { # wait_code <url> <code> <seconds>
  local i
  for i in $(seq 1 "$3"); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$1" 2>/dev/null || true)" = "$2" ] && return 0
    sleep 1
  done
  return 1
}

box_state() {
  case "$BOX" in
    a) cat "$BOX_DATA/releases/state.json" 2>/dev/null ;;
    b) dk exec "$BOX_CONT" cat /data/releases/state.json 2>/dev/null ;;
  esac || true
}
state_field() { # state_field <python expression over s>
  box_state | python3 -c "import json,sys
try:
    s=json.load(sys.stdin)
except Exception:
    sys.exit(1)
print($1)" 2>/dev/null
}
box_events() {
  case "$BOX" in
    a) cat "$BOX_DATA/releases/events.jsonl" 2>/dev/null ;;
    b) dk exec "$BOX_CONT" cat /data/releases/events.jsonl 2>/dev/null ;;
  esac || true
}
box_logs() {
  case "$BOX" in
    a) tail -n "$1" "$BOX_LOG" 2>/dev/null ;;
    b) dk logs --tail "$1" "$BOX_CONT" 2>&1 ;;
  esac || true
}

box_apply() {
  case "$BOX" in
    a)
      HATAMOTO_DATA="$BOX_DATA" "$E2E/bin/boot/hatamoto" update apply \
        --file "$E2E/site/releases/v1.0.1/hatamoto-linux-$ARCH" \
        --record "$E2E/site/releases/v1.0.1/release.json" \
        --sig "$E2E/site/releases/v1.0.1/release.json.sig" 2>&1 ;;
    b)
      dk exec "$BOX_CONT" hatamoto update apply \
        --file /tmp/rel/hatamoto-linux-$ARCH --record /tmp/rel/release.json --sig /tmp/rel/release.json.sig 2>&1 ;;
  esac
}

# shipped_unit <out> <data> <workdir> <envfile> <exe> <logfile>: the shipped unit
# with only paths, user and port-related lines changed. Fails when anything
# essential is missing from the result.
shipped_unit() {
  python3 - "$ROOT/deploy/systemd/hatamoto-api.service" "$@" <<'PY'
import sys
src, out, data, workdir, envfile, exe, log = sys.argv[1:8]
res, changed = [], []
for l in open(src).read().split("\n"):
    k = l.split("=", 1)[0]
    new = l
    if k in ("User", "Group"):
        new = None                      # a --user unit runs as the caller
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
        new = None                      # system targets do not exist in a user manager
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

box_start_a() { # box_start_a <case>
  BOX=a
  BOX_PORT="$(free_port)"; BOX_URL="http://127.0.0.1:$BOX_PORT"
  BOX_DATA="$E2E/a-$1/data"; BOX_LOG="$E2E/logs/a-$1.log"
  mkdir -p "$BOX_DATA" "$E2E/a-$1/secrets" "$E2E/a-$1/work"; : > "$BOX_LOG"
  scratch_db "a_${1//-/_}"; local dsn="$SCRATCH_DSN"
  cat > "$E2E/a-$1/hatamoto.env" <<ENVEOF
DATABASE_URL=$dsn
API_HOST=127.0.0.1
API_PORT=$BOX_PORT
HATAMOTO_SECRETS_DIR=$E2E/a-$1/secrets
HATAMOTO_TEST_TRUST_DIR=$E2E/trust
HATAMOTO_TEST_FAULT=$1@v1.0.1
ENVEOF
  BOX_UNIT="hatamoto-e2e-$ID-a-$1.service"
  local unit="$E2E/a-$1/$BOX_UNIT" changed
  changed="$(shipped_unit "$unit" "$BOX_DATA" "$E2E/a-$1/work" "$E2E/a-$1/hatamoto.env" "$E2E/bin/boot/hatamoto" "$BOX_LOG")" \
    || { fail "[a/$1] the unit under test is the shipped one" "$changed"; return 1; }
  pass "[a/$1] the unit under test is the shipped one" "$changed"
  UNITS+=("$BOX_UNIT"); UNITFILES+=("$unit")
  systemctl --user link "$unit" >/dev/null
  systemctl --user daemon-reload
  systemctl --user start "$BOX_UNIT"
}

box_stop_a() {
  systemctl --user stop "$BOX_UNIT" 2>/dev/null || true
  systemctl --user disable "$BOX_UNIT" 2>/dev/null || true
  systemctl --user reset-failed "$BOX_UNIT" 2>/dev/null || true
}

box_start_b() { # box_start_b <case>
  BOX=b
  BOX_PORT="$(free_port)"; BOX_URL="http://127.0.0.1:$BOX_PORT"
  local net="hm-e2e-$ID-b-$1" pg="hm-e2e-$ID-pg-$1" vol="hm-e2e-$ID-data-$1"
  BOX_CONT="hm-e2e-$ID-app-$1"
  NETWORKS+=("$net"); CONTAINERS+=("$pg" "$BOX_CONT"); VOLUMES+=("$vol")
  dk network create "$net" >/dev/null
  dk run -d --name "$pg" --network "$net" --network-alias db \
    -e POSTGRES_USER=hatamoto -e POSTGRES_PASSWORD=hatamoto -e POSTGRES_DB=hatamoto postgres:16 >/dev/null
  local i
  for i in $(seq 1 60); do dk exec "$pg" pg_isready -U hatamoto >/dev/null 2>&1 && break; sleep 1; done
  sleep 2 # the postgres image restarts once after its init
  for i in $(seq 1 60); do dk exec "$pg" pg_isready -U hatamoto >/dev/null 2>&1 && break; sleep 1; done
  dk run -d --name "$BOX_CONT" --network "$net" --stop-timeout 45 \
    -p "127.0.0.1:$BOX_PORT:8090" -v "$vol:/data" -v "$E2E/trust:/trust:ro" \
    -e "DATABASE_URL=postgres://hatamoto:hatamoto@db:5432/hatamoto?sslmode=disable" \
    -e API_HOST=0.0.0.0 -e API_PORT=8090 \
    -e HATAMOTO_TEST_TRUST_DIR=/trust -e "HATAMOTO_TEST_FAULT=$1@v1.0.1" \
    "hatamoto-e2e:$ID" >/dev/null
}

box_stage_b() {
  dk cp "$E2E/site/releases/v1.0.1" "$BOX_CONT:/tmp/rel" >/dev/null || die "docker cp of the release into $BOX_CONT failed"
  dk exec "$BOX_CONT" test -s /tmp/rel/release.json || die "release not visible inside $BOX_CONT"
}

box_stop_b() { :; } # removed by cleanup, or by drop_box_b below

drop_box_b() { # free the case's containers early so volumes can go
  local pg="hm-e2e-$ID-pg-$1"
  dk rm -f -v "$BOX_CONT" "$pg" >/dev/null 2>&1 || true
}

# ---- one case ---------------------------------------------------------------------
run_case() { # run_case <a|b> <case>
  local box="$1" kase="$2" tag="$1/$2" plog="$E2E/logs/poll-$1-$2.log"
  echo
  echo "== box $box, case $kase"
  "box_start_$box" "$kase" || return
  if wait_code "$BOX_URL/" 200 120; then pass "[$tag] v1.0.0 serves / 200" "$BOX_URL/"
  else fail "[$tag] v1.0.0 serves / 200" "no 200 within 120 s"; box_logs 40; return; fi
  [ "$box" = b ] && box_stage_b

  # the running child's smoke result is the baseline the switch is judged against
  local i got=""
  for i in $(seq 1 40); do
    got="$(state_field "s.get('smoke_baseline',{}).get('GET /','')" || true)"
    [ "$got" = 200 ] && break; sleep 1
  done
  check "[$tag] baseline recorded before the switch" "GET / = ${got:-none}" [ "$got" = 200 ]

  poll_start "$BOX_URL/" "$plog"
  sleep 3
  local t0 out rc=0; t0="$(now)"
  out="$(box_apply)" || rc=$?
  echo "apply (exit $rc): $out"
  check "[$tag] update apply accepted" "exit $rc" [ "$rc" = 0 ]

  # Rolled back = the bad list names v1.0.1, nothing is pending and / answers 200
  # again. The deadline is wall-clock time from the apply, not loop turns.
  local done_ok=0 elapsed="" end_at
  end_at="$(python3 -c "print($t0 + $DEADLINE_ROLLBACK)")"
  while python3 -c "import sys; sys.exit(0 if $(now) < $end_at else 1)"; do
    if [ "$(state_field "'bad' if any(b['version']=='v1.0.1' for b in s.get('bad',[])) and not s.get('pending') else 'wait'" || true)" = bad ] \
       && [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$BOX_URL/" 2>/dev/null || true)" = 200 ]; then
      done_ok=1; break
    fi
    sleep 1
  done
  elapsed="$(python3 -c "print(round($(now) - $t0, 1))")"
  check "[$tag] rolled back and serving again within ${DEADLINE_ROLLBACK}s of the apply" "${elapsed}s measured from the apply" \
    python3 -c "import sys; sys.exit(0 if $done_ok == 1 and $elapsed <= $DEADLINE_ROLLBACK else 1)"
  check "[$tag] v1.0.1 is on the bad list" "reasons: $(state_field "[b['reason'] for b in s.get('bad',[]) if b['version']=='v1.0.1']" || true)" \
    [ "$(state_field "any(b['version']=='v1.0.1' for b in s.get('bad',[]))" || true)" = True ]
  check "[$tag] rollback event in the supervisor log" "event=rollback" box_logs_have "event=rollback"
  check "[$tag] supervisor now runs the bootstrap" "run=$(state_field "s.get('run')" || true)" [ "$(state_field "s.get('run')" || true)" = bootstrap ]

  check "[$tag] v1.0.0 serves / 200 after the rollback" "$BOX_URL/" wait_code "$BOX_URL/" 200 60
  sleep 3
  poll_stop
  local res n max samples; res="$(outages "$plog")"; n="${res% *}"; max="${res#* }"
  samples="$(wc -l < "$plog")"
  check "[$tag] the poller ran the whole time" "$samples samples" [ "$samples" -ge 20 ]
  echo "poller $tag: $(wc -l < "$plog") samples every ${SAMPLE_EVERY}s, $n outage(s), LONGEST OUTAGE ${max}s (last good sample to next good sample); non-200 samples (time code):"
  grep -v ' 200$' "$plog" || echo "  none"
  check "[$tag] every outage under ${MAX_OUTAGE}s" "$n outage(s), longest ${max}s" \
    python3 -c "import sys; sys.exit(0 if float('$max') < $MAX_OUTAGE else 1)"

  local ev1 ev2 rc2=0 out2
  ev1="$(box_events | wc -l)"
  out2="$(box_apply)" || rc2=$?
  echo "second apply (exit $rc2): $out2"
  check "[$tag] second apply of v1.0.1 refused" "exit $rc2, message names the bad list" \
    bash -c '[ "$1" != 0 ] && grep -q "bad list" <<<"$2"' _ "$rc2" "$out2"
  sleep 2
  ev2="$(box_events | wc -l)"
  check "[$tag] the refused apply caused no switch" "events $ev1 -> $ev2" [ "$ev1" = "$ev2" ]

  echo "-- supervisor log tail ($tag)"
  box_logs 40
  echo "-- end of log ($tag)"
  case "$box" in a) box_stop_a ;; b) drop_box_b "$kase" ;; esac
}

box_logs_have() { box_logs 400 | grep -q "$1"; }

# ---- compose ------------------------------------------------------------------------
run_compose() {
  echo
  echo "== compose: empty volume"
  local proj="hm-e2e-$ID" web pgp
  PROJECTS+=("$proj")
  web="$(free_port)"; pgp="$(free_port)"
  mkdir -p "$E2E/compose"
  python3 - "$ROOT/docker-compose.yml" "$E2E/compose/docker-compose.yml" "hatamoto-e2e:$ID" "$web" "$pgp" <<'PY'
import sys
src, dst, image, web, pg = sys.argv[1:6]
s = open(src).read()
s = s.replace("    build: .\n", "    image: %s\n" % image)
s = s.replace('      ENVIRONMENT: development\n', '      ENVIRONMENT: development\n      # The harness pins the legacy address; real installs get a random one.\n      HATAMOTO_ADMIN_PATH: /admin\n')
s = s.replace('"8090:8090"', '"127.0.0.1:%s:8090"' % web)
s = s.replace('"127.0.0.1:5432:5432"', '"127.0.0.1:%s:5432"' % pg)
open(dst, "w").write(s)
PY
  if dk compose -p "$proj" -f "$E2E/compose/docker-compose.yml" up -d --no-build --pull never >"$E2E/logs/compose-up.log" 2>&1; then
    pass "compose up on an empty volume" "project $proj"
  else
    fail "compose up on an empty volume" "see below"; tail -n 20 "$E2E/logs/compose-up.log"; return
  fi
  local base="http://127.0.0.1:$web"
  check "[compose] / answers 200" "$base/" wait_code "$base/" 200 120
  local setup; setup="$(curl -s -H 'Sec-Fetch-Site: none' -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document' -o /dev/null -w '%{http_code}' "$base/admin/setup" || true)"
  check "[compose] /admin/setup reachable" "HTTP $setup" bash -c '[ "$1" = 200 ] || [ "$1" = 303 ]' _ "$setup"
  local cid status="" i
  cid="$(dk compose -p "$proj" -f "$E2E/compose/docker-compose.yml" ps -q hatamoto)"
  for i in $(seq 1 150); do
    status="$(dk inspect --format '{{.State.Health.Status}}' "$cid" 2>/dev/null || true)"
    [ "$status" = healthy ] && break; sleep 1
  done
  check "[compose] HEALTHCHECK reaches healthy" "status $status" [ "$status" = healthy ]
  local pid1; pid1="$(dk exec "$cid" sh -c 'cat /proc/1/cmdline | tr "\0" " "' 2>/dev/null || true)"
  check "[compose] container command is the supervisor" "pid 1: ${pid1:-unknown}" bash -c 'case "$1" in *supervise*) exit 0;; *) exit 1;; esac' _ "$pid1"
  echo "-- compose log tail"
  dk compose -p "$proj" -f "$E2E/compose/docker-compose.yml" logs --tail 20 hatamoto 2>&1 || true
  dk compose -p "$proj" -f "$E2E/compose/docker-compose.yml" down -v --remove-orphans >/dev/null 2>&1 || true
}

# ---- main -----------------------------------------------------------------------------
build_all
serve_site
if want b || want compose; then
  echo "== building image hatamoto-e2e:$ID"
  IMAGES+=("hatamoto-e2e:$ID")
  dk build -q --build-arg TAGS=hatamoto_testkeys --build-arg VERSION=v1.0.0 --build-arg COMMIT=e2e \
    -t "hatamoto-e2e:$ID" "$ROOT" >/dev/null
fi
if want a; then for c in exit-after-migrate root-500; do run_case a "$c"; done; fi
if want b; then for c in exit-after-migrate root-500; do run_case b "$c"; done; fi
if want compose; then run_compose; fi

echo
if [ "$FAILS" = 0 ]; then echo "E2E RESULT: PASS"; else echo "E2E RESULT: FAIL ($FAILS failed)"; exit 1; fi
