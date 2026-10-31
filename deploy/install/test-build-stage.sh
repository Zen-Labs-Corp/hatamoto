#!/usr/bin/env bash
# test-build-stage.sh - stage_build against fake commands and a fixture root.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

bash "$here/build-install.sh" --out "$tmp/asm" >/dev/null
script="$tmp/asm/install.sh"

# Fake commands: every call is appended to $CALLS.
bin="$tmp/bin"
mkdir -p "$bin"
for c in apt-get dpkg ufw systemctl curl useradd; do
  cat > "$bin/$c" <<'FAKE'
#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >> "$CALLS"
case "$(basename "$0")" in
  apt-get) # the verb follows the -o option
    if [ "$3" = update ]; then
      printf 'LC_ALL=%s\n' "${LC_ALL:-}" >> "$CALLS"
      [ -z "${FAKE_APT_UPDATE_WARN:-}" ] || echo "W: fake repository warning" >&2
      lockmsg="E: Could not get lock /var/lib/apt/lists/lock. It is held by process 42 (apt-get)"
      [ -z "${FAKE_APT_UPDATE_FAIL:-}" ] || { echo "E: Failed to fetch https://example.invalid 503" >&2; exit 100; }
      [ -z "${FAKE_APT_UPDATE_DOWN:-}" ] || { echo "$lockmsg" >&2; exit 100; }
      if [ -n "${FAKE_APT_UPDATE_ONCE:-}" ] && [ ! -e "$FAKE_APT_UPDATE_ONCE" ]; then : > "$FAKE_APT_UPDATE_ONCE"; echo "$lockmsg" >&2; exit 100; fi
    fi
    [ ! -x "$HATAMOTO_INSTALL_ROOT/usr/sbin/policy-rc.d" ] || echo "policy-rc.d present during $3" >> "$CALLS" ;;
  useradd) : > "$HATAMOTO_INSTALL_ROOT/.user-created" ;;
  curl) # the PGDG key: write the -o/--output file
    while [ $# -gt 0 ]; do [ "$1" != --output ] || { printf 'KEY\n' > "$2"; break; }; shift; done ;;
esac
exit 0
FAKE
  chmod +x "$bin/$c"
done
cat > "$bin/id" <<'FAKE'
#!/bin/sh
[ -e "$HATAMOTO_INSTALL_ROOT/.user-created" ]
FAKE
cat > "$bin/install" <<'FAKE'
#!/bin/sh
# Records the call; -d makes directories, otherwise copies SRC to DEST.
printf 'install %s\n' "$*" >> "$CALLS"
dirs=0; args=""
while [ $# -gt 0 ]; do
  case "$1" in
    -d) dirs=1 ;;
    -m | -o | -g) shift ;;
    *) args="$args $1" ;;
  esac
  shift
done
# shellcheck disable=SC2086
set -- $args
if [ "$dirs" = 1 ]; then mkdir -p "$@"; else cp "$1" "$2" && chmod 0755 "$2"; fi
FAKE
cat > "$bin/pg_dump" <<'FAKE'
#!/bin/sh
echo "pg_dump (PostgreSQL) ${FAKE_PG_MAJOR:-18}.4 (Fake)"
FAKE
chmod +x "$bin/id" "$bin/install" "$bin/pg_dump"

# run_build ROOT [ENV...]: source the script, stub the trust functions, run stage_build.
run_build() {
  local root="$1"; shift
  # shellcheck disable=SC2016 # the single-quoted script expands in the child shell
  env PATH="$bin:$PATH" CALLS="$tmp/calls" HATAMOTO_INSTALL_ROOT="$root" HATAMOTO_INSTALL_SOURCE_ONLY=1 "$@" \
    sh -c '. "$1"
      trust_pick_version() { echo v1.2.3; }
      trust_fetch() { printf "fake binary\n" > "$2"; }
      stage_build' _ "$script"
}

mkroot() { # mkroot NAME ID VERSION CODENAME
  local r="$tmp/$1"
  mkdir -p "$r/etc"
  printf 'ID=%s\nVERSION_ID="%s"\nVERSION_CODENAME=%s\n' "$2" "$3" "$4" > "$r/etc/os-release"
  echo "$r"
}

snapshot() { (cd "$1" && find . -type f -print0 | sort -z | xargs -0 sha256sum); }

# --- Ubuntu 24.04
ub="$(mkroot ubuntu ubuntu 24.04 noble)"
: > "$tmp/calls"
run_build "$ub" >/dev/null 2>"$tmp/err" || fail "ubuntu build failed: $(cat "$tmp/err")"
want="-o DPkg::Lock::Timeout=600 install -y --no-install-recommends -o Dpkg::Options::=--force-confold ca-certificates curl jq openssl sudo iproute2 ufw unattended-upgrades caddy postgresql-18"
grep -qxF "apt-get $want" "$tmp/calls" || fail "package list differs: $(grep apt-get "$tmp/calls")"
pass "package list"
[ "$(grep -c '^apt-get ' "$tmp/calls")" = "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 ' "$tmp/calls")" ] || fail "an apt-get call has no lock timeout"
# The seal stage only runs apt-get on a real host, so check its source.
if grep -h 'apt-get ' "$here/src/install/35-seal.sh" | grep -v '^ *#' | grep -v 'build_apt_update' | grep -v 'DPkg::Lock::Timeout=600'; then fail "seal calls apt-get without a lock timeout"; fi
pass "every apt-get call waits for the dpkg lock"
grep -q 'policy-rc.d present during install' "$tmp/calls" || fail "services were not held back during install"
[ ! -e "$ub/usr/sbin/policy-rc.d" ] || fail "policy-rc.d left behind"
pass "services held back during install, policy removed after"
[ ! -e "$ub/etc/apt/sources.list.d/pgdg.list" ] || fail "PGDG added on ubuntu"
if grep -q '^curl ' "$tmp/calls"; then fail "curl called on ubuntu"; fi
pass "no PGDG on ubuntu"
grep -qxF 'useradd --system --home /opt/hatamoto --shell /usr/sbin/nologin hatamoto' "$tmp/calls" || fail "useradd call"
grep -qx 'install -d -o root -g hatamoto -m 0750 .*/opt/hatamoto/config' "$tmp/calls" || fail "config dir mode"
grep -q 'install -m 0755 -o root -g root .*/opt/hatamoto/bin/hatamoto' "$tmp/calls" || fail "binary install call"
[ "$(cat "$ub/opt/hatamoto/bin/hatamoto")" = "fake binary" ] || fail "binary not installed"
pass "user, layout and root-owned binary"
cmp -s "$ub/etc/systemd/system/hatamoto-api.service" "$repo/deploy/systemd/hatamoto-api.service" || fail "unit differs from the tracked unit"
[ ! -e "$ub/etc/systemd/system/hatamoto-api.service.new" ] || fail "unit .new left behind"
pass "unit equals the tracked file"
exp_ufw=$'ufw default deny incoming\nufw default allow outgoing\nufw allow 22/tcp\nufw allow 80/tcp\nufw allow 443/tcp\nufw --force enable'
[ "$(grep '^ufw ' "$tmp/calls")" = "$exp_ufw" ] || fail "ufw rules differ: $(grep '^ufw ' "$tmp/calls")"
pass "ufw rules exact"
if grep -E '^systemctl (enable|start|restart).*hatamoto-api' "$tmp/calls"; then fail "hatamoto-api enabled or started"; fi
grep -qxF 'systemctl enable hatamoto-firstboot.service' "$tmp/calls" || fail "first-boot unit not enabled"
grep -qxF 'ConditionPathExists=!/var/lib/hatamoto-install/firstboot.done' "$ub/etc/systemd/system/hatamoto-firstboot.service" || fail "first-boot unit lacks the done guard"
grep -qxF 'ExecStart=/bin/sh /usr/local/lib/hatamoto/firstboot.sh first-boot' "$ub/etc/systemd/system/hatamoto-firstboot.service" || fail "first-boot ExecStart"
cmp -s "$ub/usr/local/lib/hatamoto/firstboot.sh" "$tmp/asm/firstboot.sh" || fail "firstboot.sh differs from the assembled one"
pass "first-boot unit and script"
if find "$ub" \( -name hatamoto.env -o -name secrets -o -name 'secrets.env' -o -name install_id -o -name Caddyfile -o -name 'setup*code*' \) | grep .; then
  fail "secret or site file written by the build stage"
fi
# firstboot.sh is shipped on purpose and holds the env file template text; it must not hold real values.
if grep -rIl --exclude=firstboot.sh -e 'HATAMOTO_SETUP_CODE=' -e 'DATABASE_URL=' "$ub" 2>/dev/null | grep .; then fail "build stage wrote a secret-looking line"; fi
pass "no env file, secret or Caddy site"

# --- rerun is a no-op
before="$(snapshot "$ub")"
: > "$tmp/calls"
run_build "$ub" >"$tmp/out" 2>&1 || fail "rerun failed: $(cat "$tmp/out")"
[ "$(snapshot "$ub")" = "$before" ] || fail "rerun changed files"
if grep -q '^useradd' "$tmp/calls"; then fail "rerun created the user again"; fi
grep -q 'already installed' "$tmp/out" || fail "rerun did not keep the binary"
pass "rerun changes nothing"

# --- Debian 12
de="$(mkroot debian debian 12 bookworm)"
: > "$tmp/calls"
rm -f "$ub/.user-created"
run_build "$de" >/dev/null 2>"$tmp/err" || fail "debian build failed: $(cat "$tmp/err")"
grep -qxF 'deb [signed-by=/usr/share/keyrings/postgresql-archive-keyring.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main' "$de/etc/apt/sources.list.d/pgdg.list" || fail "pgdg list wrong"
[ -s "$de/usr/share/keyrings/postgresql-archive-keyring.asc" ] || fail "pgdg key not stored"
grep -q -- '--proto =https' "$tmp/calls" || fail "key fetched without https pin"
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 update' "$tmp/calls")" = 2 ] || fail "debian must update again after adding PGDG"
pass "PGDG on debian only, key over https"

# --- apt update retries while the lists lock is held
rt="$(mkroot retry ubuntu 24.04 noble)"
: > "$tmp/calls"
run_build "$rt" HATAMOTO_APT_RETRY_SLEEP=0 FAKE_APT_UPDATE_ONCE="$tmp/update-once" >/dev/null 2>"$tmp/err" || fail "a busy first update was not retried: $(cat "$tmp/err")"
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 update' "$tmp/calls")" = 2 ] || fail "update not retried exactly once"
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 install' "$tmp/calls")" = 2 ] || fail "install was retried or skipped: $(grep "^apt-get" "$tmp/calls")"
rt="$(mkroot retrydown ubuntu 24.04 noble)"
: > "$tmp/calls"
if run_build "$rt" HATAMOTO_APT_RETRY_SLEEP=0 HATAMOTO_APT_TRIES=3 FAKE_APT_UPDATE_DOWN=1 >/dev/null 2>"$tmp/err"; then fail "a failing update was ignored"; fi
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 update' "$tmp/calls")" = 3 ] || fail "update retries not bounded by HATAMOTO_APT_TRIES"
grep -q 'apt-get update failed' "$tmp/err" || fail "update failure not named: $(cat "$tmp/err")"
rt="$(mkroot retryfail ubuntu 24.04 noble)"
: > "$tmp/calls"
if run_build "$rt" HATAMOTO_APT_RETRY_SLEEP=0 HATAMOTO_APT_TRIES=3 FAKE_APT_UPDATE_FAIL=1 >/dev/null 2>"$tmp/err"; then fail "a failing update was ignored"; fi
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 update' "$tmp/calls")" = 1 ] || fail "a non-lock update failure was retried"
grep -q 'apt-get update failed' "$tmp/err" || fail "update failure not named: $(cat "$tmp/err")"
rt="$(mkroot retrywarn ubuntu 24.04 noble)"
: > "$tmp/calls"
run_build "$rt" FAKE_APT_UPDATE_WARN=1 >/dev/null 2>"$tmp/err" || fail "a warning broke a successful update"
grep -q 'W: fake repository warning' "$tmp/err" || fail "warning of a successful update was dropped: $(cat "$tmp/err")"
grep -qx 'LC_ALL=C' "$tmp/calls" || fail "apt-get update did not run with LC_ALL=C"
! grep '^LC_ALL=' "$tmp/calls" | grep -qvx 'LC_ALL=C' || fail "an update ran without LC_ALL=C"
pass "apt update retried on the lists lock only, bounded, install not retried"

# --- refusals
systems="supported systems are Ubuntu 22.04, 24.04, 26.04 LTS and Debian 12, 13"
for bad in "fedora 40 " "debian 11 bullseye"; do
  set -- $bad
  if run_build "$(mkroot "bad$1$2" "$1" "$2" "${3:-}")" >/dev/null 2>"$tmp/err"; then fail "$1 $2 accepted"; fi
  grep -q "unsupported system: $1 $2: $systems" "$tmp/err" || fail "unsupported message for $1 $2: $(cat "$tmp/err")"
done
pass "unsupported OS dies with a message listing the five systems"

# --- accepted systems
for sys in "ubuntu 22.04 jammy" "ubuntu 24.04 noble" "ubuntu 26.04 resolute" "debian 12 bookworm" "debian 13 trixie"; do
  set -- $sys
  run_build "$(mkroot "ok$1$2" "$1" "$2" "$3")" >/dev/null 2>"$tmp/err" || fail "$sys refused: $(cat "$tmp/err")"
  if grep -q 'not a supported LTS' "$tmp/err"; then fail "$sys warned"; fi
done
: > "$tmp/calls"
run_build "$(mkroot sto ubuntu 26.10 stonking)" >/dev/null 2>"$tmp/err" || fail "ubuntu 26.10 refused: $(cat "$tmp/err")"
grep -q 'ubuntu 26.10 is not a supported LTS' "$tmp/err" || fail "no warning for ubuntu 26.10: $(cat "$tmp/err")"
pass "five systems accepted, another ubuntu warns and continues"

# --- PostgreSQL major
pg_line() { grep '^apt-get .* install ' "$tmp/calls" | grep -o 'postgresql-[0-9]*' | tail -n 1; }
for m in 16 17 18; do
  : > "$tmp/calls"
  run_build "$(mkroot "pgm$m" ubuntu 24.04 noble)" HATAMOTO_PG_MAJOR=$m FAKE_PG_MAJOR=$m >/dev/null 2>"$tmp/err" || fail "HATAMOTO_PG_MAJOR=$m refused: $(cat "$tmp/err")"
  [ "$(pg_line)" = "postgresql-$m" ] || fail "HATAMOTO_PG_MAJOR=$m installed $(pg_line)"
done
for m in 15 19 x; do
  if run_build "$(mkroot "pgbad$m" ubuntu 24.04 noble)" HATAMOTO_PG_MAJOR=$m >/dev/null 2>"$tmp/err"; then fail "HATAMOTO_PG_MAJOR=$m accepted"; fi
  grep -q 'HATAMOTO_PG_MAJOR must be 16, 17 or 18' "$tmp/err" || fail "bad major message: $(cat "$tmp/err")"
done
pass "PostgreSQL 18 by default, 16 and 17 on request, others refused"
ex="$(mkroot excl ubuntu 24.04 noble)"; mkdir -p "$ex/etc/postgresql/16/main"
: > "$tmp/calls"
run_build "$ex" FAKE_PG_MAJOR=16 >/dev/null 2>"$tmp/err" || fail "existing 16 cluster refused: $(cat "$tmp/err")"
[ "$(pg_line)" = postgresql-16 ] || fail "existing 16 cluster not kept: $(pg_line)"
run_build "$ex" HATAMOTO_PG_MAJOR=16 FAKE_PG_MAJOR=16 >/dev/null 2>&1 || fail "matching override refused"
if run_build "$ex" HATAMOTO_PG_MAJOR=18 >/dev/null 2>"$tmp/err"; then fail "override against an existing 16 accepted"; fi
grep -q 'existing PostgreSQL cluster is version 16' "$tmp/err" || fail "conflict message: $(cat "$tmp/err")"
ex="$(mkroot excl15 debian 12 bookworm)"; mkdir -p "$ex/etc/postgresql/15/main"
if run_build "$ex" >/dev/null 2>"$tmp/err"; then fail "existing 15 cluster accepted"; fi
grep -q 'needs 16, 17 or 18' "$tmp/err" || fail "15 message: $(cat "$tmp/err")"
ex="$(mkroot exmulti ubuntu 24.04 noble)"; mkdir -p "$ex/etc/postgresql/16/main" "$ex/etc/postgresql/17/main"
if run_build "$ex" >/dev/null 2>"$tmp/err"; then fail "two clusters without a choice accepted"; fi
run_build "$ex" HATAMOTO_PG_MAJOR=17 FAKE_PG_MAJOR=17 >/dev/null 2>&1 || fail "two clusters with a choice refused"
pass "an existing cluster keeps its major, one outside 16-18 is refused"
if run_build "$(mkroot pg15 debian 12 bookworm)" FAKE_PG_MAJOR=15 >/dev/null 2>"$tmp/err"; then fail "pg_dump 15 accepted"; fi
grep -q 'pg_dump is version 15, not 18' "$tmp/err" || fail "pg_dump message: $(cat "$tmp/err")"
if run_build "$(mkroot pg17 ubuntu 24.04 noble)" HATAMOTO_PG_MAJOR=17 FAKE_PG_MAJOR=16 >/dev/null 2>"$tmp/err"; then fail "pg_dump 16 accepted for 17"; fi
grep -q 'pg_dump is version 16, not 17' "$tmp/err" || fail "pg_dump message for 17: $(cat "$tmp/err")"
pass "pg_dump major mismatch dies and names the chosen major"
: > "$tmp/calls"
run_build "$(mkroot nofw ubuntu 24.04 noble)" HATAMOTO_FIREWALL=0 >/dev/null 2>&1 || fail "firewall-off build failed"
if grep -q '^ufw' "$tmp/calls"; then fail "ufw ran with HATAMOTO_FIREWALL=0"; fi
pass "HATAMOTO_FIREWALL=0 skips ufw"

echo "ok: test-build-stage"
