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
  apt-get) [ ! -x "$HATAMOTO_INSTALL_ROOT/usr/sbin/policy-rc.d" ] || echo "policy-rc.d present during $1" >> "$CALLS" ;;
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
echo "pg_dump (PostgreSQL) ${FAKE_PG_MAJOR:-16}.4 (Fake)"
FAKE
chmod +x "$bin/id" "$bin/install" "$bin/pg_dump"

# run_build ROOT [ENV...]: source the script, stub the trust functions, run stage_build.
run_build() {
  local root="$1"; shift
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
want="install -y --no-install-recommends -o Dpkg::Options::=--force-confold ca-certificates curl jq openssl sudo iproute2 ufw unattended-upgrades postgresql-16 caddy"
grep -qxF "apt-get $want" "$tmp/calls" || fail "package list differs: $(grep apt-get "$tmp/calls")"
pass "package list"
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
if grep -rIl -e 'HATAMOTO_SETUP_CODE=' -e 'DATABASE_URL=' "$ub" 2>/dev/null | grep .; then fail "build stage wrote a secret-looking line"; fi
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
[ "$(grep -c '^apt-get update' "$tmp/calls")" = 2 ] || fail "debian must update again after adding PGDG"
pass "PGDG on debian only, key over https"

# --- refusals
ol="$(mkroot old ubuntu 22.04 jammy)"
if run_build "$ol" >/dev/null 2>"$tmp/err"; then fail "ubuntu 22.04 accepted"; fi
grep -q 'unsupported system: ubuntu 22.04' "$tmp/err" || fail "unsupported message: $(cat "$tmp/err")"
pass "unsupported OS dies with a message"
if run_build "$(mkroot pg15 debian 12 bookworm)" FAKE_PG_MAJOR=15 >/dev/null 2>"$tmp/err"; then fail "pg_dump 15 accepted"; fi
grep -q 'pg_dump is version 15, not 16' "$tmp/err" || fail "pg_dump message: $(cat "$tmp/err")"
pass "pg_dump major mismatch dies"
: > "$tmp/calls"
run_build "$(mkroot nofw ubuntu 24.04 noble)" HATAMOTO_FIREWALL=0 >/dev/null 2>&1 || fail "firewall-off build failed"
if grep -q '^ufw' "$tmp/calls"; then fail "ufw ran with HATAMOTO_FIREWALL=0"; fi
pass "HATAMOTO_FIREWALL=0 skips ufw"

echo "ok: test-build-stage"
