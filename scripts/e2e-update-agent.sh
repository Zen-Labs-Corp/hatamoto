#!/usr/bin/env bash
# e2e-update-agent.sh - two-box end-to-end test of the update agent (PLAN §6 checks 1-5).
#
# Box A runs `hatamoto supervise` as a systemd --user service whose unit is the
# SHIPPED deploy/systemd/hatamoto-api.service, changed only for paths, user and
# port. Box B runs it as the command of the Docker image. Nothing is applied by
# hand: every install checks a local fake release site by itself (the check
# interval is a test-only knob, 5 s here instead of 6 h) and the updater does
# the rest. Every scenario gets a fresh box (own database, data dir, install_id).
#
#   s1  §6.1  an install in its 5% bucket reaches the next minor by itself and
#             its health gate passes; the CLI on that updated box reports the
#             running release (not the bootstrap), and `update check` there
#             downloads nothing and never downgrades
#   s2  §6.2  a broken release (both faults) is rolled back, outage < 10 s, never retried
#   s3  §6.3  halt: an install INSIDE the bucket (ramp 100%) that would download
#             the release does not while it is halted, then does once unhalted;
#             an install outside the 5% bucket never fetches a halted release;
#             yank v1.0.1 -> v1.0.2 moves a 5% install on although v1.0.2 is not open
#   s4  §6.4  a plugin that needs an SDK the target lacks keeps the install on N,
#             and `update status` says why
#   s5  §6.5  per check the site sees exactly one anonymous GET of the channel
#             manifest: no query, no cookie, only User-Agent and Accept-Encoding
#
# The fake site is backend/tools/fakesite: a static file server that logs every
# request as a JSON line. Its logs are printed raw at the end. Release keys are
# throwaway (generated at runtime, trusted through the test-only
# HATAMOTO_TEST_TRUST_DIR seam of binaries built with -tags hatamoto_testkeys).
# On box B the fake site runs in a sidecar container that owns the network
# namespace, and the app container joins it, so the updater reaches the site at
# 127.0.0.1 (loopback is the only plain-http host the test seam allows).
#
# Usage:   bash scripts/e2e-update-agent.sh [--only s1|s2|s3|s4|s5] [--box a|b]
# Needs:   go, python3, psql with DATABASE_URL (a role that can CREATE DATABASE),
#          systemctl --user (box A), docker with the postgres:16 image (box B).
#          Docker runs through `sg docker -c` when this shell lacks the group.
#          Not part of `make check` or CI.
# Output:  one PASS:/FAIL: line per assertion, the fake site logs, then
#          `E2E RESULT: PASS|FAIL`. Exit code is non-zero on any FAIL.
# Cleanup: units, containers, networks, volumes, the image it built, scratch
#          databases and the temp dir are removed at exit (also on ^C).
set -euo pipefail

ONLY=all BOXES="a b"
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2 ;;
    --box) BOXES="${2:-}"; shift 2 ;;
    -h|--help) sed -n '2,38p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
case "$ONLY" in all|s1|s2|s3|s4|s5) ;; *) echo "--only wants s1..s5" >&2; exit 2 ;; esac
case "$BOXES" in a|b|"a b") ;; *) echo "--box wants a or b" >&2; exit 2 ;; esac
want() { [ "$ONLY" = all ] || [ "$ONLY" = "$1" ]; }
wantbox() { case " $BOXES " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# The fake sites are seeded from releases/channels/*.json; without them there is
# nothing to run.
if ! ls "$ROOT"/releases/channels/*.json >/dev/null 2>&1; then
  echo "e2e-update-agent: no releases/channels/*.json here" >&2
  exit 0
fi
ID="$$"
# snap-packaged docker cannot see the host /tmp nor hidden dirs under $HOME, so stage in a plain dir under $HOME
E2E_ROOT="${E2E_TMP:-$HOME/hatamoto-e2e-tmp}"; mkdir -p "$E2E_ROOT"
E2E="$(mktemp -d "$E2E_ROOT/hm-e2e.XXXXXX")"
ARCH="$(cd "$ROOT/backend" && go env GOARCH)"
PKG=hatamoto.dev/core/app
DEADLINE="${E2E_DEADLINE:-300}"
MAX_OUTAGE=10
INTERVAL=5s
FAKE_B_PORT=8099
FAILS=0

UNITS=() UNITFILES=() CONTAINERS=() NETWORKS=() VOLUMES=() IMAGES=() DBS=() PIDS=() FAKELOGS=()

pass() { echo "PASS: $1 ($2)"; }
fail() { echo "FAIL: $1 ($2)"; FAILS=$((FAILS + 1)); }
check() { # check <what> <detail> <command...>
  local what="$1" detail="$2"; shift 2
  if "$@"; then pass "$what" "$detail"; else fail "$what" "$detail"; fi
}
die() { echo "E2E PREFLIGHT: $*" >&2; exit 2; }

DOCKER_MODE=""
dk() {
  if [ "$DOCKER_MODE" = sg ]; then sg docker -c "$(printf '%q ' docker "$@")"; else docker "$@"; fi
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
if wantbox a; then systemctl --user show-environment >/dev/null 2>&1 || die "systemctl --user is not usable"; fi
if wantbox b; then
  if docker info >/dev/null 2>&1; then DOCKER_MODE=plain
  elif sg docker -c 'docker info' >/dev/null 2>&1; then DOCKER_MODE=sg
  else die "docker is not usable for this user (docker info fails, also through sg docker)"; fi
  dk image inspect postgres:16 >/dev/null 2>&1 || die "image postgres:16 is not available locally"
fi

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }
now() { date +%s.%N; }

# ---- install_id seeding -----------------------------------------------------------
# bucket = big-endian uint16 of sha256(install_id + version)[0:2] mod 100; an
# install is in a rollout of pct when bucket < pct.
seed_id() { # seed_id <outfile> <in|out> <version> [<in|out> <version>]...  (5% rollouts)
  python3 - "$@" <<'PY'
import hashlib, os, sys
out, rest = sys.argv[1], sys.argv[2:]
want = list(zip(rest[0::2], rest[1::2]))
def bucket(i, v): return int.from_bytes(hashlib.sha256(i + v.encode()).digest()[:2], "big") % 100
while True:
    i = os.urandom(32)
    if all((bucket(i, v) < 5) == (w == "in") for w, v in want):
        break
with open(out, "wb") as f: f.write(i)
os.chmod(out, 0o600)
print(", ".join("%s=%d" % (v, bucket(i, v)) for _, v in want))
PY
}
# Same arithmetic as the Go golden test (id = bytes 0..31: v1.0.1 -> 75, v1.2.3 -> 35).
selfcheck_bucket() {
  python3 - <<'PY'
import hashlib, sys
i = bytes(range(32))
b = lambda v: int.from_bytes(hashlib.sha256(i + v.encode()).digest()[:2], "big") % 100
if (b("v1.0.1"), b("v1.2.3")) != (75, 35): sys.exit(1)
PY
}
check "bucket arithmetic of the seeding script matches the Go golden values" "v1.0.1 -> 75, v1.2.3 -> 35" selfcheck_bucket

# ---- keys, binaries, release files ------------------------------------------
mkdir -p "$E2E"/{bin/boot,priv,trust,rel,logs}
export HATAMOTO_TEST_TRUST_DIR="$E2E/trust"
LDF() { echo "-X $PKG.Version=$1 -X $PKG.Commit=e2e"; }
PLATFORMS="linux-amd64 linux-arm64 darwin-amd64 darwin-arm64"

# release_dir <version> <binary> <facts> <outdir>: artifacts, record, signature
release_dir() {
  local v="$1" bin="$2" facts="$3" dir="$4" p
  mkdir -p "$dir"
  for p in $PLATFORMS; do
    if [ "$p" = "linux-$ARCH" ]; then cp "$bin" "$dir/hatamoto-$p"
    else printf 'not a real %s build\n' "$p" > "$dir/hatamoto-$p"; fi
  done
  "$E2E/bin/release" record --version "$v" --kind patch --facts "$facts" \
    --rollback-floor v1.0.0 --min-from v1.0.0 --notes-url https://example.test/notes \
    --dir "$dir" --out "$dir/release.json" >/dev/null
  "$E2E/bin/release" sign-record "$dir/release.json" --key "$E2E/priv/release.key" --out "$dir/release.json.sig" >/dev/null
}

build_all() {
  cd "$ROOT/backend"
  echo "== building (tags hatamoto_testkeys)"
  go build -tags hatamoto_testkeys -o "$E2E/bin/release" ./tools/release
  go build -o "$E2E/bin/fakesite" ./tools/fakesite
  CGO_ENABLED=0 GOOS=linux go build -o "$E2E/bin/fakesite-linux" ./tools/fakesite
  (
    unset HATAMOTO_TEST_TRUST_DIR
    "$E2E/bin/release" keygen --kind release --out "$E2E/priv" >/dev/null
    "$E2E/bin/release" keygen --kind manifest --out "$E2E/priv" >/dev/null
  )
  cp "$E2E/priv/release.pub" "$E2E/trust/release-test.pub"
  cp "$E2E/priv/manifest.pub" "$E2E/trust/manifest.pub"
  local v
  for v in v1.0.0 v1.0.1 v1.0.2; do
    mkdir -p "$E2E/bin/$v"
    CGO_ENABLED=0 go build -tags hatamoto_testkeys -trimpath -ldflags "$(LDF $v)" -o "$E2E/bin/$v/hatamoto" ./cmd/hatamoto
    "$E2E/bin/$v/hatamoto" version --json > "$E2E/bin/$v/facts.json"
  done
  cp "$E2E/bin/v1.0.0/hatamoto" "$E2E/bin/boot/hatamoto"
  release_dir v1.0.1 "$E2E/bin/v1.0.1/hatamoto" "$E2E/bin/v1.0.1/facts.json" "$E2E/rel/v1.0.1"
  release_dir v1.0.2 "$E2E/bin/v1.0.2/hatamoto" "$E2E/bin/v1.0.2/facts.json" "$E2E/rel/v1.0.2"
  if want s4; then
    # a v1.0.1 whose record claims SDK 0.11.0 and no compatibility list
    python3 - "$E2E/bin/v1.0.1/facts.json" "$E2E/bin/v1.0.1/facts-sdk11.json" <<'PY'
import json, sys
f = json.load(open(sys.argv[1])); f["sdk"] = "0.11.0"; f.pop("sdk_compatible_with", None)
json.dump(f, open(sys.argv[2], "w"))
PY
    release_dir v1.0.1 "$E2E/bin/v1.0.1/hatamoto" "$E2E/bin/v1.0.1/facts-sdk11.json" "$E2E/rel-sdk11/v1.0.1"
    # the hello plugin, asking for ^0.10, signed with a throwaway package key
    mkdir -p "$E2E/plugin" "$E2E/pkgkey"
    (cd "$ROOT/examples/plugins/hello" && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o "$E2E/plugin/plugin.wasm" .)
    sed 's/"sdk": "^0.8"/"sdk": "^0.10"/' "$ROOT/examples/plugins/hello/plugin.json" > "$E2E/plugin/plugin.json"
    grep -q '"\^0.10"' "$E2E/plugin/plugin.json" || die "plugin.json rewrite failed"
    TRUSTED_KEYS="$("$E2E/bin/boot/hatamoto" keys generate --name e2e --dir "$E2E/pkgkey" | sed -n 's/^  HATAMOTO_TRUSTED_KEYS=//p')"
    [ -n "$TRUSTED_KEYS" ] || die "keys generate printed no trusted key"
    "$E2E/bin/boot/hatamoto" plugin pack "$E2E/plugin" --key "$E2E/pkgkey/e2e.key" --out "$E2E/plugin/hello.hmp" >/dev/null
  fi
  cd "$ROOT"
}

# ---- scratch databases ----------------------------------------------------------
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

# ---- poller and outage maths (as in e2e-supervisor.sh) ---------------------------
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
outages() { # prints "<count> <max seconds>"; an outage runs from the last good sample to the next good one
  awk '
    $2 != "200" { if (!out) { out = 1; start = (seen ? lastgood : $1) } ; last = $1; next }
    { seen = 1; lastgood = $1 }
    out { d = $1 - start; n++; if (d > max) max = d; out = 0 }
    END { if (out) { d = last - start; n++; if (d > max) max = d }
          printf "%d %.1f\n", n, max }' "$1"
}

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

# ---- fake site and channel helpers -----------------------------------------------
# site_new <tag> [<release dir>...]: a site dir with hard links to the release
# files (the files are big) and an editable channel source next to it.
SITE="" CHSRC="" FAKELOG=""
site_new() {
  local tag="$1"; shift
  SITE="$E2E/$tag/site"; CHSRC="$E2E/$tag/chansrc"; FAKELOG="$E2E/logs/fake-$tag.jsonl"
  mkdir -p "$SITE/releases/channels" "$CHSRC" "$E2E/$tag/pub"
  cp "$ROOT/releases/channels/"*.json "$CHSRC/"
  local d
  for d in "$@"; do cp -al "$d" "$SITE/releases/"; done
  : > "$FAKELOG"; FAKELOGS+=("$tag|$FAKELOG")
}
chan() { "$E2E/bin/release" channel "$@" --dir "$CHSRC" >/dev/null; }
chan_add() { chan add stable "$1" --record "$SITE/releases/$1/release.json" --sig "$SITE/releases/$1/release.json.sig" --ramp "$2"; }
publish() { # sign the channel source and replace the served manifest atomically
  local pub; pub="$(dirname "$CHSRC")/pub"
  "$E2E/bin/release" channel resign --channel stable --in "$CHSRC" --out "$pub" --key "$E2E/priv/manifest.key" >/dev/null
  mv -f "$pub/stable.json" "$SITE/releases/channels/stable.json"
}
# fcount <path regex>: GET requests in the fake site log whose path matches
fcount() {
  python3 - "$FAKELOG" "$1" <<'PY'
import json, re, sys
n = 0
for l in open(sys.argv[1]):
    l = l.strip()
    if l:
        r = json.loads(l)
        if r["method"] == "GET" and re.fullmatch(sys.argv[2], r["path"]): n += 1
print(n)
PY
}
fake_start_a() { # sets FAKE_PORT, FAKEPID
  FAKE_PORT="$(free_port)"
  "$E2E/bin/fakesite" --dir "$SITE" --addr "127.0.0.1:$FAKE_PORT" --log "$FAKELOG" >/dev/null 2>&1 &
  FAKEPID=$!; PIDS+=("$FAKEPID")
  wait_until 10 curl -s -o /dev/null "http://127.0.0.1:$FAKE_PORT/" || return 1
  : > "$FAKELOG" # drop the probe request
}

# ---- boxes ------------------------------------------------------------------------
# Globals for the box in use. Set before box_up: FAULT (kind or empty), BOXINTERVAL,
# IDFILE (raw install_id or empty), EXTRA_ENV (array of K=V).
BOX="" BOX_URL="" BOX_PORT="" BOX_UNIT="" BOX_CONT="" BOX_DATA="" BOX_LOG="" BOX_TAG="" BOX_SIDE="" BOX_PG="" BOX_VOL="" BOX_NET=""
FAULT="" BOXINTERVAL="$INTERVAL" IDFILE="" EXTRA_ENV=()

box_read() { # box_read <path below the data dir>
  case "$BOX" in
    a) cat "$BOX_DATA/$1" 2>/dev/null ;;
    b) dk exec "$BOX_CONT" cat "/data/$1" 2>/dev/null ;;
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
box_events_n() { box_read releases/events.jsonl | wc -l; }
box_logs() {
  case "$BOX" in
    a) tail -n "$1" "$BOX_LOG" 2>/dev/null ;;
    b) dk logs --tail "$1" "$BOX_CONT" 2>&1 ;;
  esac || true
}
box_logs_have() { box_logs 600 | grep -q "$1"; }
# box_cli <args...>: the hatamoto CLI with the box's environment and data dir
box_cli() {
  case "$BOX" in
    a) ( while IFS= read -r line; do [ -n "$line" ] && export "${line%%=*}=${line#*=}"; done < "$E2E/$BOX_TAG/hatamoto.env"
         export HATAMOTO_DATA="$BOX_DATA"
         "$E2E/bin/boot/hatamoto" "$@" 2>&1 ) ;;
    b) dk exec "$BOX_CONT" hatamoto "$@" 2>&1 ;;
  esac
}
box_cli_file() { # box_cli_file <path of a file on the host> <args before> ... : copies it into box B
  case "$BOX" in
    a) echo "$1" ;;
    b) dk cp "$1" "$BOX_CONT:/tmp/$(basename "$1")" >/dev/null; echo "/tmp/$(basename "$1")" ;;
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

box_up_a() {
  BOX=a; BOX_PORT="$(free_port)"; BOX_URL="http://127.0.0.1:$BOX_PORT"
  BOX_DATA="$E2E/$BOX_TAG/data"; BOX_LOG="$E2E/logs/box-$BOX_TAG.log"
  mkdir -p "$BOX_DATA" "$E2E/$BOX_TAG/secrets" "$E2E/$BOX_TAG/work"; : > "$BOX_LOG"
  [ -z "$IDFILE" ] || install -m 600 "$IDFILE" "$BOX_DATA/install_id"
  fake_start_a || { fail "[$BOX_TAG] fake site is up" "port $FAKE_PORT"; return 1; }
  scratch_db "a_${BOX_TAG//-/_}"
  {
    echo "DATABASE_URL=$SCRATCH_DSN"
    echo "API_HOST=127.0.0.1"; echo "API_PORT=$BOX_PORT"
    echo "HATAMOTO_SECRETS_DIR=$E2E/$BOX_TAG/secrets"
    echo "HATAMOTO_TEST_TRUST_DIR=$E2E/trust"
    echo "HATAMOTO_UPDATE_URL=http://127.0.0.1:$FAKE_PORT/releases"
    echo "HATAMOTO_TEST_UPDATE_INTERVAL=$BOXINTERVAL"
    [ -z "$FAULT" ] || echo "HATAMOTO_TEST_FAULT=$FAULT@v1.0.1"
    local kv; for kv in "${EXTRA_ENV[@]:-}"; do [ -n "$kv" ] && echo "$kv"; done
  } > "$E2E/$BOX_TAG/hatamoto.env"
  BOX_UNIT="hm-e2e-$ID-a-$BOX_TAG.service"
  local unit="$E2E/$BOX_TAG/$BOX_UNIT" changed
  changed="$(shipped_unit "$unit" "$BOX_DATA" "$E2E/$BOX_TAG/work" "$E2E/$BOX_TAG/hatamoto.env" "$E2E/bin/boot/hatamoto" "$BOX_LOG")" \
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
  kill "$FAKEPID" 2>/dev/null || true
}

# Box B: network, postgres, a sidecar that runs the fake site and owns the
# network namespace (and publishes the box port), the app container joined to
# that namespace. Premortem fallback if `db` did not resolve there: the database
# address is passed as an IP, so it never needs the embedded DNS.
box_up_b() {
  BOX=b; BOX_PORT="$(free_port)"; BOX_URL="http://127.0.0.1:$BOX_PORT"
  BOX_NET="hm-e2e-$ID-$BOX_TAG"; BOX_PG="hm-e2e-$ID-pg-$BOX_TAG"; BOX_SIDE="hm-e2e-$ID-site-$BOX_TAG"
  BOX_CONT="hm-e2e-$ID-app-$BOX_TAG"; BOX_VOL="hm-e2e-$ID-data-$BOX_TAG"
  NETWORKS+=("$BOX_NET"); CONTAINERS+=("$BOX_PG" "$BOX_SIDE" "$BOX_CONT"); VOLUMES+=("$BOX_VOL")
  dk network create "$BOX_NET" >/dev/null
  dk volume create "$BOX_VOL" >/dev/null
  if [ -n "$IDFILE" ]; then
    dk run --rm -u 0 -v "$BOX_VOL:/data" --entrypoint sh "hatamoto-e2e:$ID" -c \
      "printf %s '$(base64 -w0 "$IDFILE")' | base64 -d > /data/install_id; chown 10001:10001 /data/install_id; chmod 600 /data/install_id" >/dev/null
  fi
  dk run -d --name "$BOX_PG" --network "$BOX_NET" \
    -e POSTGRES_USER=hatamoto -e POSTGRES_PASSWORD=hatamoto -e POSTGRES_DB=hatamoto postgres:16 >/dev/null
  local i
  for i in $(seq 1 60); do dk exec "$BOX_PG" pg_isready -U hatamoto >/dev/null 2>&1 && break; sleep 1; done
  sleep 2 # the postgres image restarts once after its init
  for i in $(seq 1 60); do dk exec "$BOX_PG" pg_isready -U hatamoto >/dev/null 2>&1 && break; sleep 1; done
  local pgip; pgip="$(dk inspect -f '{{(index .NetworkSettings.Networks "'"$BOX_NET"'").IPAddress}}' "$BOX_PG")"
  dk run -d --name "$BOX_SIDE" --network "$BOX_NET" -p "127.0.0.1:$BOX_PORT:8090" \
    -u "$(id -u):$(id -g)" -v "$E2E/bin/fakesite-linux:/fakesite:ro" -v "$SITE:/site:ro" -v "$(dirname "$FAKELOG"):/log" \
    --entrypoint /fakesite "hatamoto-e2e:$ID" --dir /site --addr "127.0.0.1:$FAKE_B_PORT" --log "/log/$(basename "$FAKELOG")" >/dev/null
  local envs=(-e "DATABASE_URL=postgres://hatamoto:hatamoto@$pgip:5432/hatamoto?sslmode=disable"
    -e API_HOST=0.0.0.0 -e API_PORT=8090 -e HATAMOTO_TEST_TRUST_DIR=/trust
    -e "HATAMOTO_UPDATE_URL=http://127.0.0.1:$FAKE_B_PORT/releases" -e "HATAMOTO_TEST_UPDATE_INTERVAL=$BOXINTERVAL")
  [ -z "$FAULT" ] || envs+=(-e "HATAMOTO_TEST_FAULT=$FAULT@v1.0.1")
  local kv; for kv in "${EXTRA_ENV[@]:-}"; do [ -n "$kv" ] && envs+=(-e "$kv"); done
  dk run -d --name "$BOX_CONT" --network "container:$BOX_SIDE" --stop-timeout 45 \
    -v "$BOX_VOL:/data" -v "$E2E/trust:/trust:ro" "${envs[@]}" "hatamoto-e2e:$ID" >/dev/null
}

box_down_b() {
  dk rm -f -v "$BOX_CONT" "$BOX_SIDE" "$BOX_PG" >/dev/null 2>&1 || true
  dk network rm "$BOX_NET" >/dev/null 2>&1 || true
  dk volume rm -f "$BOX_VOL" >/dev/null 2>&1 || true
}

box_up() { # box_up <a|b> <tag>
  BOX="$1"; BOX_TAG="$2"
  "box_up_$1"
}
box_down() {
  echo "-- box log tail ($BOX_TAG)"; box_logs 25; echo "-- end of log ($BOX_TAG)"
  "box_down_$BOX"
  FAULT="" BOXINTERVAL="$INTERVAL" IDFILE="" EXTRA_ENV=()
}
reset_box() { FAULT="" BOXINTERVAL="$INTERVAL" IDFILE="" EXTRA_ENV=(); }

serving() { wait_code "$BOX_URL/" 200 "${1:-120}"; }
manifest_seen() { [ "$(fcount '/releases/channels/stable\.json')" -ge 1 ]; }
art() { echo "/releases/$1/hatamoto-linux-$ARCH"; }
at_version() { [ "$(box_current)" = "$1" ]; }
# gate_passed <version>: the supervisor switched to it and its health gate
# passed and was kept: phase idle, nothing pending, run=release, not on the
# bad list, and a success event for it (the gate's own record; "unproven"
# only means the 120 s boot window has not elapsed yet). The current symlink
# alone moves before the gate runs.
boot_window_over() { [ "$(state_field "not s.get('unproven')" || true)" = True ]; }
gate_debug() { echo "-- supervisor state"; box_state; echo; echo "-- supervisor events"; box_read releases/events.jsonl; echo "-- end"; }
gate_passed() {
  [ "$(state_field "s.get('phase')=='idle' and not s.get('pending') and s.get('run')=='release' and s.get('current_seen')=='$1' and not any(b['version']=='$1' for b in (s.get('bad') or []))" || true)" = True ] || return 1
  [ "$(box_current)" = "$1" ] || return 1
  box_read releases/events.jsonl | python3 -c "import json,sys
ev=[json.loads(l) for l in sys.stdin if l.strip()]
sys.exit(0 if any(e.get('to')=='$1' and e.get('phase')=='success' for e in ev) else 1)"
}

# ---- scenarios --------------------------------------------------------------------
s1() { # s1 <box>
  local b="$1" tag="$1-s1"; echo; echo "== box $b, s1: minor reaches an install in its bucket"
  local idinfo; site_new "$tag" "$E2E/rel/v1.0.1"
  IDFILE="$E2E/$tag.id"; idinfo="$(seed_id "$IDFILE" in v1.0.1)"
  chan_add v1.0.1 now:5; publish
  box_up "$b" "$tag" || return
  check "[$tag] v1.0.0 serves / 200" "$BOX_URL/" serving
  check "[$tag] install_id seeded in the 5% bucket" "$idinfo" true
  local end_ok=0; wait_until "$DEADLINE" gate_passed v1.0.1 && end_ok=1
  [ "$end_ok" = 1 ] || gate_debug
  check "[$tag] the install moved to v1.0.1 by itself (no update command run) and its health gate passed" "current=$(box_current), state phase=$(state_field "s.get('phase')" || true) run=$(state_field "s.get('run')" || true)" [ "$end_ok" = 1 ]
  check "[$tag] v1.0.1 serves / 200" "$BOX_URL/" serving 60
  check "[$tag] site saw the manifest GET" "$(fcount '/releases/channels/stable\.json') GETs" bash -c '[ "$1" -ge 1 ]' _ "$(fcount '/releases/channels/stable\.json')"
  check "[$tag] applying added record, sig and artifact GETs" "record $(fcount '/releases/v1\.0\.1/release\.json'), sig $(fcount '/releases/v1\.0\.1/release\.json\.sig'), artifact $(fcount "$(art v1.0.1)")" \
    bash -c '[ "$1" -ge 1 ] && [ "$2" -ge 1 ] && [ "$3" = 1 ]' _ "$(fcount '/releases/v1\.0\.1/release\.json')" "$(fcount '/releases/v1\.0\.1/release\.json\.sig')" "$(fcount "$(art v1.0.1)")"
  s1_cli_on_updated_box "$tag"
  box_down
}

# The CLI on a box that already updated itself: the CLI binary is still the
# v1.0.0 bootstrap, but what runs is v1.0.1.
s1_cli_on_updated_box() { # s1_cli_on_updated_box <tag>
  local tag="$1" st art0 art1 out
  # Until the 120 s boot window has passed the agent reports "busy" and decides nothing.
  wait_until "$DEADLINE" boot_window_over || fail "[$tag] the boot window of v1.0.1 ended" "unproven=$(state_field "s.get('unproven')" || true)"
  st="$(box_cli update status)"; echo "-- update status (updated box)"; echo "$st"; echo "-- end of status"
  check "[$tag] update status on the updated box shows the running release v1.0.1, not the bootstrap" "$(grep '^version' <<<"$st")" grep -Eq '^version +v1\.0\.1 ' <<<"$st"
  art0="$(fcount "$(art v1.0.1)")"
  out="$(box_cli update check)"; echo "-- update check (updated box)"; echo "$out"; echo "-- end of check"
  art1="$(fcount "$(art v1.0.1)")"
  check "[$tag] update check on the updated box makes no artifact GET" "artifact GETs $art0 -> $art1" [ "$art0" = "$art1" ]
  check "[$tag] update check decides on the updated box: v1.0.1, up to date, no error" "$(grep -E '^(version|status)' <<<"$out" | tr '\n' '|')" \
    bash -c 'grep -Eq "^version +v1\.0\.1 " <<<"$1" && grep -Eq "^status +up to date" <<<"$1" && ! grep -Eq "^last check .* error" <<<"$1"' _ "$out"
  # The running release is halted: the only older release is the bootstrap's v1.0.0, which is never a target.
  chan halt stable v1.0.1 --reason "e2e: running release halted"; publish
  out="$(box_cli update check)"; echo "-- update check, running release halted"; echo "$out"; echo "-- end of check"
  sleep $((2 * ${INTERVAL%s}))
  check "[$tag] with the running release halted, update check says so without picking another release" "$(grep -E '^status' <<<"$out")" \
    bash -c '! grep -Eq "^status +(applying|staged)" <<<"$1" && ! grep -Eq "^last check .* error" <<<"$1"' _ "$out"
  check "[$tag] a halted running release is not downgraded: still v1.0.1, gate state unchanged" "current=$(box_current)" \
    bash -c '[ "$1" = v1.0.1 ]' _ "$(box_current)"
  check "[$tag] ...and nothing was downloaded again" "artifact GETs $(fcount "$(art v1.0.1)")" [ "$(fcount "$(art v1.0.1)")" = "$art0" ]
  check "[$tag] ...and the checks never fetched v1.0.0" "GETs under /releases/v1.0.0/: $(fcount '/releases/v1\.0\.0/.*')" [ "$(fcount '/releases/v1\.0\.0/.*')" = 0 ]
}

s2() { # s2 <box>
  local b="$1" fault kase
  for fault in exit-after-migrate root-500; do
    local tag="$b-s2-$fault"; echo; echo "== box $b, s2: broken release ($fault) rolls back"
    site_new "$tag" "$E2E/rel/v1.0.1"
    IDFILE="$E2E/$tag.id"; seed_id "$IDFILE" in v1.0.1 >/dev/null
    FAULT="$fault"; chan_add v1.0.1 now:5; publish
    local plog="$E2E/logs/poll-$tag.log"
    # the poller starts once the bootstrap serves; the first check comes ~3 s after boot
    box_up "$b" "$tag" || return
    if ! serving 120; then fail "[$tag] v1.0.0 serves / 200" "no 200 within 120 s"; box_logs 40; box_down; continue; fi
    pass "[$tag] v1.0.0 serves / 200" "$BOX_URL/"
    poll_start "$BOX_URL/" "$plog"
    local t0 done_ok=0 elapsed end_at; t0="$(now)"
    end_at="$(python3 -c "print($t0 + $DEADLINE)")"
    while python3 -c "import sys; sys.exit(0 if $(now) < $end_at else 1)"; do
      if [ "$(state_field "'bad' if any(b['version']=='v1.0.1' for b in s.get('bad',[])) and not s.get('pending') else 'wait'" || true)" = bad ] \
         && [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$BOX_URL/" 2>/dev/null || true)" = 200 ]; then
        done_ok=1; break
      fi
      sleep 1
    done
    elapsed="$(python3 -c "print(round($(now) - $t0, 1))")"
    check "[$tag] the updater installed v1.0.1, it failed, v1.0.1 is on the bad list, / serves 200 again" "${elapsed}s after the poller started, bad: $(state_field "[b['reason'] for b in s.get('bad',[]) if b['version']=='v1.0.1']" || true)" [ "$done_ok" = 1 ]
    check "[$tag] rollback event in the supervisor log" "event=rollback" box_logs_have "event=rollback"
    check "[$tag] supervisor runs the bootstrap again" "run=$(state_field "s.get('run')" || true)" [ "$(state_field "s.get('run')" || true)" = bootstrap ]
    local ev1 art1 ev2 art2 waited=$((6 * ${INTERVAL%s}))
    ev1="$(box_events_n)"; art1="$(fcount "$(art v1.0.1)")"
    sleep "$((waited + 3))"
    poll_stop
    ev2="$(box_events_n)"; art2="$(fcount "$(art v1.0.1)")"
    check "[$tag] never retried: artifact GET count stays 1 over 6 check intervals" "artifact GETs $art1 -> $art2" bash -c '[ "$1" = 1 ] && [ "$2" = 1 ]' _ "$art1" "$art2"
    check "[$tag] never retried: no new supervisor events" "events $ev1 -> $ev2" [ "$ev1" = "$ev2" ]
    local res n max samples; res="$(outages "$plog")"; n="${res% *}"; max="${res#* }"; samples="$(wc -l < "$plog")"
    check "[$tag] the poller ran the whole time" "$samples samples" [ "$samples" -ge 20 ]
    echo "poller $tag: $samples samples every ${SAMPLE_EVERY}s, $n outage(s), LONGEST OUTAGE ${max}s; non-200 samples (time code):"
    grep -v ' 200$' "$plog" || echo "  none"
    check "[$tag] every outage under ${MAX_OUTAGE}s" "$n outage(s), longest ${max}s" python3 -c "import sys; sys.exit(0 if float('$max') < $MAX_OUTAGE else 1)"
    box_down
  done
}

s3() { # s3 <box>
  local b="$1" tag="$1-s3a" idinfo checks_n i
  echo; echo "== box $b, s3a: halted, install INSIDE the bucket (would download if it were not halted)"
  site_new "$tag" "$E2E/rel/v1.0.1"
  IDFILE="$E2E/$tag.id"; idinfo="$(seed_id "$IDFILE" in v1.0.1)"
  chan_add v1.0.1 now:100; chan halt stable v1.0.1 --reason "e2e halt"; publish
  box_up "$b" "$tag" || return
  check "[$tag] v1.0.0 serves / 200" "$BOX_URL/" serving
  for i in $(seq 1 60); do [ "$(fcount '/releases/channels/stable\.json')" -ge 4 ] && break; sleep 1; done
  checks_n="$(fcount '/releases/channels/stable\.json')"
  check "[$tag] the install checked at least 4 times (install_id in bucket: $idinfo; ramp 100%)" "$checks_n manifest GETs" [ "$checks_n" -ge 4 ]
  check "[$tag] still on v1.0.0 while halted" "current=$(box_current)" [ -z "$(box_current)" ]
  check "[$tag] while halted: zero artifact GETs (and nothing else under /releases/v1.0.1/)" "$(fcount "$(art v1.0.1)") artifact, $(fcount '/releases/v1\.0\.1/.*') under v1.0.1" [ "$(fcount '/releases/v1\.0\.1/.*')" = 0 ]
  # control: the same box takes the release once it is unhalted, so the halt was what held it back
  chan unhalt stable v1.0.1; publish
  local ok=0; wait_until "$DEADLINE" gate_passed v1.0.1 && ok=1
  check "[$tag] control: unhalted, the same install updates to v1.0.1 and passes its gate" "current=$(box_current), artifact GETs $(fcount "$(art v1.0.1)")" [ "$ok" = 1 ]
  check "[$tag] control: the artifact was fetched exactly once, after the unhalt" "artifact GETs $(fcount "$(art v1.0.1)")" [ "$(fcount "$(art v1.0.1)")" = 1 ]
  box_down

  tag="$b-s3o"; echo; echo "== box $b, s3o: halted at 5%, install outside the bucket"
  site_new "$tag" "$E2E/rel/v1.0.1"
  IDFILE="$E2E/$tag.id"; idinfo="$(seed_id "$IDFILE" out v1.0.1)"
  chan_add v1.0.1 now:5; chan halt stable v1.0.1 --reason "e2e halt"; publish
  box_up "$b" "$tag" || return
  check "[$tag] v1.0.0 serves / 200" "$BOX_URL/" serving
  for i in $(seq 1 60); do [ "$(fcount '/releases/channels/stable\.json')" -ge 4 ] && break; sleep 1; done
  checks_n="$(fcount '/releases/channels/stable\.json')"
  check "[$tag] the install checked at least 4 times (install_id out of bucket: $idinfo)" "$checks_n manifest GETs" [ "$checks_n" -ge 4 ]
  check "[$tag] still on v1.0.0" "current=$(box_current)" [ -z "$(box_current)" ]
  check "[$tag] fake site saw no GET of anything under /releases/v1.0.1/" "$(fcount '/releases/v1\.0\.1/.*') GETs" [ "$(fcount '/releases/v1\.0\.1/.*')" = 0 ]
  box_down

  tag="$b-s3b"; echo; echo "== box $b, s3b: yank v1.0.1 -> v1.0.2 moves a 5% install on"
  site_new "$tag" "$E2E/rel/v1.0.1" "$E2E/rel/v1.0.2"
  IDFILE="$E2E/$tag.id"; idinfo="$(seed_id "$IDFILE" in v1.0.1 out v1.0.2)"
  chan_add v1.0.1 now:5; publish
  box_up "$b" "$tag" || return
  check "[$tag] v1.0.0 serves / 200 (install_id: $idinfo)" "$BOX_URL/" serving
  local ok=0; wait_until "$DEADLINE" gate_passed v1.0.1 && ok=1
  check "[$tag] the install moved to v1.0.1 and its health gate passed" "current=$(box_current)" [ "$ok" = 1 ]
  check "[$tag] v1.0.1 serves / 200" "$BOX_URL/" serving 60
  # v1.0.2 is on the channel but its ramp opens in an hour; v1.0.1 is yanked in favour of it
  chan_add v1.0.2 +1h:100; chan yank stable v1.0.1 --reason "e2e yank" --to v1.0.2; publish
  ok=0; wait_until "$DEADLINE" gate_passed v1.0.2 && ok=1
  check "[$tag] the install moved to v1.0.2 though its ramp is closed (yank to), gate passed" "current=$(box_current), v1.0.2 artifact GETs $(fcount "$(art v1.0.2)")" [ "$ok" = 1 ]
  check "[$tag] v1.0.2 serves / 200" "$BOX_URL/" serving 60
  box_down
}

s4() { # s4 <box>
  local b="$1" tag="$1-s4"; echo; echo "== box $b, s4: plugin needs an SDK the target lacks"
  site_new "$tag" "$E2E/rel-sdk11/v1.0.1"
  IDFILE="$E2E/$tag.id"; seed_id "$IDFILE" in v1.0.1 >/dev/null
  EXTRA_ENV=("HATAMOTO_TRUSTED_KEYS=$TRUSTED_KEYS")
  # nothing is published yet (the site answers 404, checks fail quietly) so the
  # plugin is installed before the first decision
  box_up "$b" "$tag" || return
  check "[$tag] v1.0.0 serves / 200" "$BOX_URL/" serving
  local f out; f="$(box_cli_file "$E2E/plugin/hello.hmp")"
  out="$(box_cli package install "$f" --grant all 2>&1)" || true
  echo "package install: $out"
  check "[$tag] hello (sdk ^0.10) installed" "$(box_cli package list 2>&1 | grep -c hello) row(s)" bash -c '[ "$1" -ge 1 ]' _ "$(box_cli package list 2>&1 | grep -c hello)"
  chan_add v1.0.1 now:5; publish
  local st="" i
  for i in $(seq 1 120); do
    st="$(box_cli update status)"
    grep -Eq 'blocked.*hello.*\^0\.10' <<<"$st" && break; sleep 1
  done
  echo "-- update status"; echo "$st"; echo "-- end of status"
  check "[$tag] update status names the plugin and its range" "blocked ... hello ... ^0.10" grep -Eq 'blocked.*hello.*\^0\.10' <<<"$st"
  sleep $((3 * ${INTERVAL%s}))
  check "[$tag] the install stays on v1.0.0" "current='$(box_current)'" [ -z "$(box_current)" ]
  check "[$tag] the artifact was never fetched" "artifact GETs $(fcount "$(art v1.0.1)")" [ "$(fcount "$(art v1.0.1)")" = 0 ]
  box_down
}

s5() { # s5 <box>
  local b="$1" tag="$1-s5"; echo; echo "== box $b, s5: what one check sends"
  site_new "$tag" "$E2E/rel/v1.0.1"
  IDFILE="$E2E/$tag.id"; seed_id "$IDFILE" in v1.0.1 >/dev/null
  chan_add v1.0.1 +1h:100; publish   # a release exists, but its ramp is closed: a check is only the manifest GET
  BOXINTERVAL=1h
  box_up "$b" "$tag" || return
  check "[$tag] v1.0.0 serves / 200" "$BOX_URL/" serving
  wait_until 60 manifest_seen || true
  sleep 8
  local out; out="$(python3 - "$FAKELOG" 1 <<'PY'
import json, sys
rs = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
want = int(sys.argv[2]); bad = []
if len(rs) != want: bad.append("%d requests, want %d" % (len(rs), want))
for r in rs:
    if (r["method"], r["path"]) != ("GET", "/releases/channels/stable.json"): bad.append("unexpected request %s %s" % (r["method"], r["path"]))
    if r["query"] != "": bad.append("query %r" % r["query"])
    extra = set(r["headers"]) - {"User-Agent", "Accept-Encoding"}
    if extra: bad.append("extra headers %s" % sorted(extra))
    if not r["headers"].get("User-Agent", [""])[0].startswith("Go-http-client/"): bad.append("User-Agent %r" % r["headers"].get("User-Agent"))
    if "Cookie" in r["headers"]: bad.append("cookie")
print("; ".join(bad) if bad else "ok: %d GET /releases/channels/stable.json, empty query, headers %s" % (len(rs), sorted(rs[0]["headers"])))
sys.exit(1 if bad else 0)
PY
)" && ok=1 || ok=0
  check "[$tag] one automatic check = exactly one anonymous manifest GET" "$out" [ "$ok" = 1 ]
  local cli; cli="$(box_cli update check --dry-run)"; echo "update check --dry-run: $cli"
  sleep 1
  out="$(python3 - "$FAKELOG" 2 <<'PY'
import json, sys
rs = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
bad = []
if len(rs) != int(sys.argv[2]): bad.append("%d requests, want %s" % (len(rs), sys.argv[2]))
for r in rs:
    if (r["method"], r["path"], r["query"]) != ("GET", "/releases/channels/stable.json", ""): bad.append("unexpected %s" % r["path"])
    if set(r["headers"]) - {"User-Agent", "Accept-Encoding"} or "Cookie" in r["headers"]: bad.append("extra headers")
print("; ".join(bad) if bad else "ok: 2 GETs in total")
sys.exit(1 if bad else 0)
PY
)" && ok=1 || ok=0
  check "[$tag] one manual dry-run check adds exactly one more GET" "$out" [ "$ok" = 1 ]
  box_down
}

# ---- main -----------------------------------------------------------------------------
build_all
if wantbox b; then
  echo "== building image hatamoto-e2e:$ID"
  IMAGES+=("hatamoto-e2e:$ID")
  dk build -q --build-arg TAGS=hatamoto_testkeys --build-arg VERSION=v1.0.0 --build-arg COMMIT=e2e \
    -t "hatamoto-e2e:$ID" "$ROOT" >/dev/null
fi
for b in $BOXES; do
  for s in s1 s2 s3 s4 s5; do
    reset_box
    want "$s" && "$s" "$b"
  done
done

for e in "${FAKELOGS[@]:-}"; do
  [ -n "$e" ] || continue
  echo
  echo "==== fakesite log (${e%%|*}) ===="
  cat "${e#*|}"
  echo "==== end of fakesite log (${e%%|*}) ===="
done
echo
if [ "$FAILS" = 0 ]; then echo "E2E RESULT: PASS"; else echo "E2E RESULT: FAIL ($FAILS failed)"; exit 1; fi
