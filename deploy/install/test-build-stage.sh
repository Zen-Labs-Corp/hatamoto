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
for c in apt-get dpkg ufw systemctl curl useradd gpg; do
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
  curl) # the suite probe (-I) or a repository key: write the -o/--output file, tagged with its URL
    case " $* " in *" -sI "*) printf '%s' "${FAKE_PGDG_STATUS:-200}"; [ "${FAKE_PGDG_STATUS:-200}" != 000 ] || exit 7; exit 0 ;; esac
    for url; do :; done
    while [ $# -gt 0 ]; do [ "$1" != --output ] || { printf 'KEY %s\n' "$url" > "$2"; break; }; shift; done ;;
  gpg) # --show-keys: print the real pinned fingerprint of the key the URL tag names
    f=""; for f; do :; done
    case "$(cat "$f")" in
      *cloudsmith*) key=caddy; fpr=65760C51EDEA2017CEA2CA15155B6D79CA56EA34 ;; # gitleaks:allow
      *) key=pgdg; fpr=B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8 ;;
    esac
    [ "${FAKE_KEY_BAD:-}" != "$key" ] || fpr=0000000000000000000000000000000000000000
    printf 'pub:-:4096:1:X:1::-:::scSC::::::23::0:\nfpr:::::::::%s:\nfpr:::::::::SUBKEY:\n' "$fpr" ;;
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
grep -qxF 'deb [signed-by=/usr/share/keyrings/postgresql-archive-keyring.asc] https://apt.postgresql.org/pub/repos/apt noble-pgdg main' "$ub/etc/apt/sources.list.d/pgdg.list" || fail "ubuntu pgdg list wrong"
[ -s "$ub/usr/share/keyrings/postgresql-archive-keyring.asc" ] || fail "ubuntu pgdg key not stored"
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 update' "$tmp/calls")" = 2 ] || fail "ubuntu must update again after adding PGDG"
pass "PGDG on ubuntu"
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

# --- an existing binary below the picked version (the floor) is replaced, others are kept
setbin() { # setbin ROOT VERSION-TEXT
  mkdir -p "$1/opt/hatamoto/bin"
  printf '#!/bin/sh\necho "%s"\n' "$2" > "$1/opt/hatamoto/bin/hatamoto"
  chmod 0755 "$1/opt/hatamoto/bin/hatamoto"
}
ob="$(mkroot oldbin ubuntu 24.04 noble)"
setbin "$ob" "hatamoto v0.28.0 (commit abc, sdk 0.9.0)"
run_build "$ob" >"$tmp/out" 2>&1 || fail "old binary build failed: $(cat "$tmp/out")"
[ "$(cat "$ob/opt/hatamoto/bin/hatamoto")" = "fake binary" ] || fail "binary below the floor was kept"
grep -q 'v0.28.0 is older than v1.2.3, replacing it' "$tmp/out" || fail "no replace message: $(cat "$tmp/out")"
for keep in "hatamoto v1.2.3 (commit abc)" "hatamoto v2.0.0 (commit abc)" "hatamoto dev (commit abc, sdk 0.14.0)"; do
  kb="$(mkroot keepbin ubuntu 24.04 noble)"
  setbin "$kb" "$keep"
  run_build "$kb" >"$tmp/out" 2>&1 || fail "keep build failed: $(cat "$tmp/out")"
  grep -q 'already installed' "$tmp/out" || fail "binary '$keep' not kept: $(cat "$tmp/out")"
  grep -q 'echo' "$kb/opt/hatamoto/bin/hatamoto" || fail "binary '$keep' replaced"
  rm -rf "$kb"
done
pass "a binary below the picked version is replaced, a current or unreadable one is kept"

# --- Debian 12
de="$(mkroot debian debian 12 bookworm)"
: > "$tmp/calls"
rm -f "$ub/.user-created"
run_build "$de" >/dev/null 2>"$tmp/err" || fail "debian build failed: $(cat "$tmp/err")"
grep -qxF 'deb [signed-by=/usr/share/keyrings/postgresql-archive-keyring.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main' "$de/etc/apt/sources.list.d/pgdg.list" || fail "pgdg list wrong"
[ -s "$de/usr/share/keyrings/postgresql-archive-keyring.asc" ] || fail "pgdg key not stored"
grep -q -- '--proto =https' "$tmp/calls" || fail "key fetched without https pin"
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 update' "$tmp/calls")" = 2 ] || fail "debian must update again after adding PGDG"
pass "PGDG on debian, key over https"

# --- Caddy: only jammy lacks a distro package, so only jammy gets the Caddy repository
cj="$(mkroot caddyjammy ubuntu 22.04 jammy)"
: > "$tmp/calls"
run_build "$cj" >/dev/null 2>"$tmp/err" || fail "jammy build failed: $(cat "$tmp/err")"
grep -qxF 'deb [signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.asc] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main' "$cj/etc/apt/sources.list.d/caddy-stable.list" || fail "jammy caddy list wrong"
[ -s "$cj/usr/share/keyrings/caddy-stable-archive-keyring.asc" ] || fail "caddy key not stored"
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 update' "$tmp/calls")" = 2 ] || fail "jammy must update once after adding both repositories"
for sys in "ubuntu 24.04 noble" "debian 12 bookworm"; do
  set -- $sys
  cn="$(mkroot "caddyno$1" "$1" "$2" "$3")"
  run_build "$cn" >/dev/null 2>&1 || fail "$sys build failed"
  [ ! -e "$cn/etc/apt/sources.list.d/caddy-stable.list" ] || fail "$sys got the Caddy repository"
done
cb="$(mkroot caddybad ubuntu 22.04 jammy)"
if run_build "$cb" FAKE_KEY_BAD=caddy >/dev/null 2>"$tmp/err"; then fail "a Caddy key with another fingerprint was accepted"; fi
grep -q 'Caddy repository key has fingerprint' "$tmp/err" || fail "caddy fingerprint message: $(cat "$tmp/err")"
[ ! -e "$cb/etc/apt/sources.list.d/caddy-stable.list" ] || fail "caddy list written for a bad key"
pass "Caddy repository on jammy only, key fingerprint pinned"

# --- the PostgreSQL repository key must match the pinned fingerprint
pb="$(mkroot pgdgbad ubuntu 24.04 noble)"
if run_build "$pb" FAKE_KEY_BAD=pgdg >/dev/null 2>"$tmp/err"; then fail "a PostgreSQL key with another fingerprint was accepted"; fi
grep -q 'PostgreSQL repository key has fingerprint 0000.*expected B97B0AFC' "$tmp/err" || fail "pgdg fingerprint message: $(cat "$tmp/err")"
[ ! -e "$pb/etc/apt/sources.list.d/pgdg.list" ] && [ ! -e "$pb/usr/share/keyrings/postgresql-archive-keyring.asc" ] || fail "pgdg list or key kept for a bad key"
pass "PostgreSQL repository key fingerprint pinned"

# --- known codenames are never probed and keep their own suite, even when the archive looks down
for sys in "ubuntu 22.04 jammy" "ubuntu 24.04 noble" "ubuntu 26.04 resolute" "debian 12 bookworm" "debian 13 trixie"; do
  set -- $sys
  kn="$(mkroot "known$3" "$1" "$2" "$3")"
  : > "$tmp/calls"
  run_build "$kn" FAKE_PGDG_STATUS=404 >/dev/null 2>"$tmp/err" || fail "$3 build failed: $(cat "$tmp/err")"
  grep -q "repos/apt $3-pgdg main" "$kn/etc/apt/sources.list.d/pgdg.list" || fail "$3 did not get its own suite"
  if grep -q -e '-sI' "$tmp/calls"; then fail "$3 was probed"; fi
done
# a list left by an earlier fallback is corrected on a known codename
st="$(mkroot stale ubuntu 24.04 noble)"; mkdir -p "$st/etc/apt/sources.list.d"
echo 'deb [signed-by=/x] https://apt.postgresql.org/pub/repos/apt resolute-pgdg main' > "$st/etc/apt/sources.list.d/pgdg.list"
run_build "$st" >/dev/null 2>"$tmp/err" || fail "stale list build failed: $(cat "$tmp/err")"
grep -q 'repos/apt noble-pgdg main' "$st/etc/apt/sources.list.d/pgdg.list" || fail "stale fallback list stuck on a known codename"
[ "$(grep -c . "$st/etc/apt/sources.list.d/pgdg.list")" = 1 ] || fail "stale list not replaced"
pass "known codenames use their own suite without a probe"

# --- an unknown codename is probed (retry, timeout); only a definite miss falls back
nx="$(mkroot nosuite ubuntu 26.10 stonking)"
: > "$tmp/calls"
run_build "$nx" FAKE_PGDG_STATUS=404 >/dev/null 2>"$tmp/err" || fail "missing suite broke the build: $(cat "$tmp/err")"
grep -q "no suite for 'stonking'; using resolute" "$tmp/err" || fail "no fallback warning: $(cat "$tmp/err")"
grep -q 'repos/apt resolute-pgdg main' "$nx/etc/apt/sources.list.d/pgdg.list" || fail "fallback suite not written"
grep -q -e '--retry 3 --max-time 10 .*dists/stonking-pgdg/Release' "$tmp/calls" || fail "suite not probed with retry and timeout: $(grep -e '-sI' "$tmp/calls")"
nx="$(mkroot nosuited debian 13 "")"
run_build "$nx" FAKE_PGDG_STATUS=404 >/dev/null 2>"$tmp/err" || fail "empty codename broke the build: $(cat "$tmp/err")"
grep -q 'repos/apt trixie-pgdg main' "$nx/etc/apt/sources.list.d/pgdg.list" || fail "debian fallback suite not written"
nx="$(mkroot hasuite ubuntu 26.10 stonking)"
run_build "$nx" >/dev/null 2>"$tmp/err" || fail "published unknown suite broke the build: $(cat "$tmp/err")"
grep -q 'repos/apt stonking-pgdg main' "$nx/etc/apt/sources.list.d/pgdg.list" || fail "published suite not used"
for code in 000 503; do
  nx="$(mkroot "probe$code" ubuntu 26.10 stonking)"
  if run_build "$nx" FAKE_PGDG_STATUS=$code >/dev/null 2>"$tmp/err"; then fail "probe failure $code fell back"; fi
  grep -q "cannot check apt.postgresql.org for a 'stonking' suite" "$tmp/err" || fail "probe failure message: $(cat "$tmp/err")"
  [ ! -e "$nx/etc/apt/sources.list.d/pgdg.list" ] || fail "foreign suite written after probe failure $code"
done
pass "unknown pgdg suite falls back on a 404 only, with a warning"

# --- apt update retries while the lists lock is held
rt="$(mkroot retry ubuntu 24.04 noble)"
: > "$tmp/calls"
run_build "$rt" HATAMOTO_APT_RETRY_SLEEP=0 FAKE_APT_UPDATE_ONCE="$tmp/update-once" >/dev/null 2>"$tmp/err" || fail "a busy first update was not retried: $(cat "$tmp/err")"
[ "$(grep -c '^apt-get -o DPkg::Lock::Timeout=600 update' "$tmp/calls")" = 3 ] || fail "update not retried exactly once (3 = busy, retry, after PGDG)"
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
