#!/usr/bin/env bash
# test-firstboot.sh - stage_firstboot and its helpers against fake commands and a fixture root.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

bash "$here/build-install.sh" --out "$tmp/asm" >/dev/null
fbscript="$tmp/asm/firstboot.sh"

# --- fake commands -------------------------------------------------------------
bin="$tmp/bin"
mkdir -p "$bin"
mk() { cat > "$bin/$1"; chmod +x "$bin/$1"; }

mk sudo <<'FAKE'
#!/bin/sh
user=root
if [ "$1" = -u ]; then user="$2"; shift 2; fi
printf 'sudo -u %s %s\n' "$user" "$(basename "$1")" >> "$CALLS"
FAKE_SUDO_USER="$user" exec "$@"
FAKE
mk psql <<'FAKE'
#!/bin/sh
printf 'psql %s\n' "$*" >> "$CALLS"
case "$*" in
  *"select 1 from pg_roles"*) [ ! -e "$TMPD/role" ] || echo 1 ;;
  *"select 1 from pg_database"*) [ ! -e "$TMPD/db" ] || echo 1 ;;
  *"select 1"*) echo 1 ;;
  *"count(*) from hatamoto_users"*) echo "${FAKE_OWNERS:-0}" ;;
  *) # SQL on stdin: keep it outside the fixture root
    sql="$(cat)"
    printf '%s\n' "$sql" >> "$TMPD/psql-stdin"
    if [ -n "${FAKE_PSQL_FAIL:-}" ]; then
      echo "ERROR:  boom" >&2
      case "$*" in *VERBOSITY=terse*) ;; *) printf 'LINE 1: %s\n' "$sql" >&2 ;; esac
      exit 1
    fi
    case "$sql" in *"CREATE ROLE"*) : > "$TMPD/role" ;; esac
    case "$sql" in *"CREATE DATABASE"*) : > "$TMPD/db" ;; esac ;;
esac
FAKE
mk systemctl <<'FAKE'
#!/bin/sh
printf 'systemctl %s\n' "$*" >> "$CALLS"
FAKE
mk curl <<'FAKE'
#!/bin/sh
printf 'curl %s\n' "$*" >> "$CALLS"
case "$*" in
  *--resolve*) [ -z "${FAKE_PROXY_FAIL:-}" ] || exit 22; echo ok ;;
  *api.ipify.org*) [ -n "${FAKE_IP:-}" ] && echo "$FAKE_IP" || exit 22 ;;
  # Released binaries answer /ready with JSON, newer ones with "ok": only the status may count.
  */ready*) echo '{"database":"connected","status":"ready"}' ;;
  *) echo ok ;;
esac
FAKE
mk getent <<'FAKE'
#!/bin/sh
[ -n "${FAKE_DNS_IP:-}" ] || exit 2
printf '%s STREAM %s\n' "$FAKE_DNS_IP" "$2"
FAKE
mk ssh-keygen <<'FAKE'
#!/bin/sh
printf 'ssh-keygen %s\n' "$*" >> "$CALLS"
FAKE
mk make-ssl-cert <<'FAKE'
#!/bin/sh
printf 'make-ssl-cert %s\n' "$*" >> "$CALLS"
# a lock held by another job for the first calls
n="$(cat "$TMPD/sslcalls" 2>/dev/null || echo 0)"; echo $((n + 1)) > "$TMPD/sslcalls"
[ "$n" -ge "${FAKE_SSL_BUSY:-0}" ] || { echo "debconf: config.dat is locked by another process" >&2; exit 1; }
FAKE
mk ip <<'FAKE'
#!/bin/sh
case "$*" in
  *"route get"*) [ -z "${FAKE_ROUTE_SRC:-}" ] || echo "1.1.1.1 via 10.0.0.1 dev eth0 src $FAKE_ROUTE_SRC uid 0" ;;
  *addr*) for a in ${FAKE_LOCAL_IPS:-}; do echo "2: eth0    inet $a/24 brd 255.255.255.255 scope global eth0"; done ;;
esac
FAKE
mk ufw <<'FAKE'
#!/bin/sh
printf 'ufw %s\n' "$*" >> "$CALLS"
FAKE
mk install <<'FAKE'
#!/bin/sh
printf 'install %s\n' "$*" >> "$CALLS"
dirs=0; mode=""; args=""
while [ $# -gt 0 ]; do
  case "$1" in
    -d) dirs=1 ;;
    -m) mode="$2"; shift ;;
    -o | -g) shift ;;
    *) args="$args $1" ;;
  esac
  shift
done
# shellcheck disable=SC2086
set -- $args
if [ "$dirs" = 1 ]; then mkdir -p "$@"; [ -z "$mode" ] || chmod "$mode" "$@"
else cp "$1" "$2"; [ -z "$mode" ] || chmod "$mode" "$2"; fi
FAKE
# The fake hatamoto binary: migrate and admin-url only.
fake_hatamoto="$tmp/hatamoto.fake"
cat > "$fake_hatamoto" <<'FAKE'
#!/bin/sh
printf 'hatamoto %s user=%s db=%s\n' "$1" "${FAKE_SUDO_USER:-none}" "$([ -n "${DATABASE_URL:-}" ] && echo set || echo unset)" >> "$CALLS"
case "$1" in
  migrate)
    if [ -n "${FAKE_MIGRATE_FAIL:-}" ] && [ ! -e "$TMPD/migrate-failed" ]; then : > "$TMPD/migrate-failed"; exit 1; fi ;;
  admin-url)
    n="$(cat "$TMPD/adminn" 2>/dev/null || echo 0)"
    echo $((n + 1)) > "$TMPD/adminn"
    if [ "$n" -lt "${FAKE_ADMIN_NOTREADY:-0}" ]; then echo "admin address is not chosen yet; hatamoto picks one at its next start"; exit 0; fi
    echo "$PUBLIC_BASE_URL/k7qm-x2rf-9tdc" ;;
esac
FAKE

mkroot() { # mkroot NAME
  local r="$tmp/$1"
  mkdir -p "$r/opt/hatamoto/bin" "$r/opt/hatamoto/config" "$r/opt/hatamoto/data" "$r/etc/caddy" "$r/etc/ssh"
  cp "$fake_hatamoto" "$r/opt/hatamoto/bin/hatamoto"; chmod +x "$r/opt/hatamoto/bin/hatamoto"
  echo "# package default" > "$r/etc/caddy/Caddyfile"
  : > "$r/etc/ssh/ssh_host_ed25519_key"
  echo "$r"
}

# run_fb ROOT [ENV...]: stub the later parts, run stage_firstboot, stdout to $tmp/out, stderr to $tmp/err.
run_fb() {
  local root="$1"; shift
  rm -rf "$tmp/state"; mkdir -p "$tmp/state"
  # shellcheck disable=SC2016 # the single-quoted script expands in the child shell
  env PATH="$bin:$PATH" CALLS="$tmp/calls" TMPD="$tmp/state" HATAMOTO_INSTALL_ROOT="$root" HATAMOTO_INSTALL_SOURCE_ONLY=1 \
    HATAMOTO_FIRSTBOOT_POLL=0 HATAMOTO_PUBLIC_IP=203.0.113.7 "$@" \
    sh -c '. "$1"
      install_motd() { echo "install_motd" >> "$CALLS"; }
      install_setupcode_clean() { echo "install_setupcode_clean" >> "$CALLS"; }
      install_dns_switch() { echo "install_dns_switch pending=$FB_PENDING_DOMAIN host=$FB_HOST ip=$FB_IP" >> "$CALLS"; }
      setup_swap() { echo "setup_swap" >> "$CALLS"; }
      setup_backups() { echo "setup_backups" >> "$CALLS"; }
      stage_firstboot' _ "$fbscript" >"$tmp/out" 2>"$tmp/err"
}

# helpers sourced into a plain sh for the pure functions
pure() { HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; shift; "$@"' _ "$fbscript" "$@"; }

# --- pure helpers ---------------------------------------------------------------
[ "$(pure fb_sslip_name 203.0.113.7)" = 203-0-113-7.sslip.io ] || fail "sslip name"
if pure fb_sslip_name 300.1.1.1 >/dev/null 2>&1; then fail "bad IP accepted"; fi
pass "sslip name"

codes=""
for _ in $(seq 1 20); do
  c="$(pure fb_gen_code)"
  [[ "$c" =~ ^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}$ ]] || fail "code format/alphabet: $c"
  [ "$(printf '%s' "${c//-/}" | fold -w1 | sort -u | wc -l)" -ge 4 ] || fail "code has fewer than 4 distinct characters: $c"
  codes+="$c"$'\n'
done
[ "$(printf '%s' "$codes" | sort -u | wc -l)" -ge 19 ] || fail "codes are not unique across 20 runs"
pass "generated codes: format, alphabet, distinct, unique"

for ok in K7M2-RT9P-QW4X k7m2rt9pqw4x "K7M2-RT9P-QW4X-ZZ"; do pure fb_check_code "$ok" 2>/dev/null || fail "code rejected: $ok"; done
for bad in "K7M2-RT9P" "AAAA-AAAA-AAAA" "ABAB-ABAB-ABAB" "K7M2 RT9P QW4X" 'K7M2-RT9P-QW4!' "" "ABC-ABC-ABC-ABC"; do
  if pure fb_check_code "$bad" 2>/dev/null; then fail "code accepted: '$bad'"; fi
done
pass "override codes: accepted and rejected"

env_out="$(pure fb_render_env pw123 sec456 K7M2-RT9P-QW4X https://x.example.com)"
for want in 'DATABASE_URL=postgres://hatamoto:pw123@127.0.0.1:5432/hatamoto?sslmode=disable' ENVIRONMENT=production \
  PUBLIC_BASE_URL=https://x.example.com API_HOST=127.0.0.1 MEDIA_UPLOAD_DIR=/opt/hatamoto/uploads \
  HATAMOTO_CONFIG_FILE=/opt/hatamoto/config/hatamoto.env HATAMOTO_SETUP_CODE=K7M2-RT9P-QW4X HATAMOTO_SECRET=sec456; do
  printf '%s\n' "$env_out" | grep -qxF "$want" || fail "env lacks $want"
done
if printf '%s\n' "$env_out" | grep -q 'HATAMOTO_UPDATES'; then fail "env sets HATAMOTO_UPDATES"; fi
pass "env content"

# Render fully before printing: a `| grep -q` reader that exits on its first
# match must not SIGPIPE a producer that is still writing (pipefail -> 141).
cf() { local out; out="$(pure fb_render_caddyfile "$@")"; printf '%s\n' "$out"; }
cf h.example auto "" | grep -qx 'h.example {' || fail "auto caddyfile"
if cf h.example auto "" | grep -Eq 'tls internal|email'; then fail "auto caddyfile has extras"; fi
cf h.example auto ops@example.com | grep -qxP '\temail ops@example.com' || fail "email missing"
cf h.example internal "" | grep -qxP '\ttls internal' || fail "tls internal missing"
cf h.example off ops@example.com | grep -qx 'http://h.example {' || fail "off caddyfile"
if cf h.example off ops@example.com | grep -q email; then fail "off caddyfile carries an email"; fi
cf h.example auto "" | grep -qxP '\treverse_proxy 127.0.0.1:8090' || fail "reverse_proxy missing"
pass "Caddyfile for each TLS mode, with and without email"

cf h.example auto "" old.example | grep -qx 'old.example {' || fail "redirect site missing"
cf h.example auto "" old.example | grep -qxP '\tredir https://h.example\{uri\} permanent' || fail "redirect target"
[ "$(cf h.example auto "" old.example | grep -c reverse_proxy)" = 1 ] || fail "old host proxied"
cf h.example internal "" old.example | awk '/^old.example/{f=1} f' | grep -qxP '\ttls internal' || fail "redirect site lacks tls internal"
cf h.example off "" old.example | grep -qx 'http://old.example {' || fail "off redirect site"
cf h.example off "" old.example | grep -qxP '\tredir http://h.example\{uri\} permanent' || fail "off redirect target"
if cf h.example auto "" h.example | grep -q redir; then fail "same-host redirect"; fi
[ "$(cf h.example auto "")" = "$(cf h.example auto "" "")" ] || fail "3-arg output changed"
pass "Caddyfile old-host redirect for each TLS mode"

# --- full run ---------------------------------------------------------------------
r="$(mkroot full)"
: > "$tmp/calls"
FAKE_SSL_BUSY=2 FAKE_ADMIN_NOTREADY=2 run_fb "$r" HATAMOTO_SETUP_CODE=k7m2-rt9p-qw4x HATAMOTO_ADMIN_EMAIL=ops@example.com || fail "first boot failed: $(cat "$tmp/err")"
envf="$r/opt/hatamoto/config/hatamoto.env"
[ "$(stat -c %a "$envf")" = 640 ] || fail "env file mode $(stat -c %a "$envf")"
grep -qE '^install -m 0640 -o root -g hatamoto .*/opt/hatamoto/config/hatamoto.env$' "$tmp/calls" || fail "env owner/mode not root:hatamoto 0640"
grep -qx 'HATAMOTO_SETUP_CODE=k7m2-rt9p-qw4x' "$envf" || fail "pinned code not kept"
grep -qx 'PUBLIC_BASE_URL=https://203-0-113-7.sslip.io' "$envf" || fail "base url"
if grep -q 'HATAMOTO_UPDATES' "$envf"; then fail "HATAMOTO_UPDATES in env"; fi
grep -qx 'API_HOST=127.0.0.1' "$envf" || fail "API_HOST"
pass "env file: mode, owner, content"

grep -qx '203-0-113-7.sslip.io {' "$r/etc/caddy/Caddyfile" || fail "caddyfile host"
grep -qx $'\temail ops@example.com' "$r/etc/caddy/Caddyfile" || fail "caddyfile email"
[ "$(cat "$r/etc/caddy/Caddyfile.orig")" = "# package default" ] || fail "package Caddyfile not kept"
pass "Caddyfile written, original kept"

order="$(grep -nE '^systemctl (enable --now postgresql|enable caddy|restart caddy|enable hatamoto-api|restart hatamoto-api)$' "$tmp/calls" | sed 's/^[0-9]*://' | tr '\n' ',')"
[ "$order" = "systemctl enable --now postgresql,systemctl enable caddy,systemctl restart caddy,systemctl enable hatamoto-api,systemctl restart hatamoto-api," ] || fail "service order (running services must be restarted): $order"
[ "$(grep -c '^make-ssl-cert' "$tmp/calls")" = 3 ] || fail "make-ssl-cert not retried while debconf was locked"
snake="$(grep -nE '^(make-ssl-cert generate-default-snakeoil --force-overwrite|systemctl enable --now postgresql|systemctl restart postgresql)$' "$tmp/calls" | sed 's/^[0-9]*://' | uniq | tr '\n' ',')"
[ "$snake" = "make-ssl-cert generate-default-snakeoil --force-overwrite,systemctl enable --now postgresql,systemctl restart postgresql," ] || fail "a fresh snakeoil pair must exist before PostgreSQL starts: $snake"
grep -qx 'hatamoto migrate user=hatamoto db=set' "$tmp/calls" || fail "migrate not run as hatamoto with the env"
if grep -E '^hatamoto ' "$tmp/calls" | grep -vq 'user=hatamoto'; then fail "hatamoto ran as another user"; fi
[ "$(grep -c '^hatamoto admin-url' "$tmp/calls")" = 3 ] || fail "admin-url not retried until it is a URL"
pass "services in order, hatamoto only as the service user, admin-url retried"

grep -qF 'https://203-0-113-7.sslip.io/k7qm-x2rf-9tdc/setup#code=k7m2-rt9p-qw4x' "$tmp/out" || fail "banner link: $(cat "$tmp/out")"
grep -qF 'Setup code: k7m2-rt9p-qw4x' "$tmp/out" || fail "banner code"
grep -q 'Hatamoto is ready.' "$tmp/out" || fail "banner heading"
grep -q 'Updates:    automatic (stable channel)' "$tmp/out" || fail "banner updates"
if grep -q 'Domain:' "$tmp/out"; then fail "domain line without a domain"; fi
[ "$(stat -c %a "$r/var/lib/hatamoto-install/link")" = 600 ] || fail "link file mode"
[ "$(cat "$r/var/lib/hatamoto-install/link")" = "https://203-0-113-7.sslip.io/k7qm-x2rf-9tdc" ] || fail "link file content"
{ grep -qx 'install_motd' "$tmp/calls" && grep -qx 'install_setupcode_clean' "$tmp/calls" && grep -qx 'setup_swap' "$tmp/calls" && grep -qx 'setup_backups' "$tmp/calls"; } || fail "housekeeping parts not called"
[ -f "$r/var/lib/hatamoto-install/firstboot.done" ] || fail "done marker missing"
pass "banner, link file, parts, done marker"

# Swap exists before PostgreSQL starts and before migrate.
swapn="$(grep -nx 'setup_swap' "$tmp/calls" | head -n 1 | cut -d: -f1)"
pgn="$(grep -nx 'systemctl enable --now postgresql' "$tmp/calls" | head -n 1 | cut -d: -f1)"
mign="$(grep -n '^hatamoto migrate' "$tmp/calls" | head -n 1 | cut -d: -f1)"
{ [ -n "$swapn" ] && [ "$swapn" -lt "$pgn" ] && [ "$swapn" -lt "$mign" ]; } || fail "setup_swap must run before postgresql ($pgn) and migrate ($mign): $swapn"
pass "swap before PostgreSQL and migrate"

# The database password lives in the env file only.
dbpw="$(sed -n 's#^DATABASE_URL=postgres://hatamoto:\([^@]*\)@.*#\1#p' "$envf")"
[ "${#dbpw}" -ge 32 ] || fail "database password too short"
grep -q "$dbpw" "$tmp/state/psql-stdin" || fail "password did not reach psql"
if grep -rl -e "$dbpw" "$r" | grep -v 'config/hatamoto.env$' | grep .; then fail "database password found outside the env file"; fi
if grep -q -e "$dbpw" "$tmp/calls" "$tmp/out" "$tmp/err"; then fail "database password in calls or output"; fi
secret="$(sed -n 's/^HATAMOTO_SECRET=//p' "$envf")"
if grep -rl -e "$secret" "$r" | grep -v 'config/hatamoto.env$' | grep .; then fail "server secret found outside the env file"; fi
grep -q 'CREATE ROLE hatamoto WITH LOGIN CREATEROLE PASSWORD' "$tmp/state/psql-stdin" || fail "role statement"
grep -q 'SET log_min_error_statement' "$tmp/state/psql-stdin" || fail "server statement logging not turned down"
if grep -qi 'SUPERUSER' "$tmp/state/psql-stdin"; then fail "role is a superuser"; fi
pass "secrets only in the env file"

# second run is a no-op
before="$(find "$r" -type f -print0 | sort -z | xargs -0 sha256sum)"
n="$(wc -l < "$tmp/calls")"
run_fb "$r" || fail "second run failed"
grep -q 'already done' "$tmp/out" || grep -q 'already done' "$tmp/err" || fail "second run did not say it is done"
[ "$(find "$r" -type f -print0 | sort -z | xargs -0 sha256sum)" = "$before" ] || fail "second run changed files"
[ "$(wc -l < "$tmp/calls")" = "$n" ] || fail "second run ran commands"
pass "second run is a no-op"

# --- generated code when none is pinned ----------------------------------------------
r="$(mkroot gen)"
run_fb "$r" || fail "generated-code run failed: $(cat "$tmp/err")"
gc="$(sed -n 's/^HATAMOTO_SETUP_CODE=//p' "$r/opt/hatamoto/config/hatamoto.env")"
pure fb_check_code "$gc" || fail "generated code not acceptable: $gc"
grep -qF "Setup code: $gc" "$tmp/out" || fail "banner lacks the generated code"
pass "generated code lands in env and banner"

# --- refusals -----------------------------------------------------------------------
r="$(mkroot weak)"
if run_fb "$r" HATAMOTO_SETUP_CODE=short; then fail "weak code accepted"; fi
grep -q 'HATAMOTO_SETUP_CODE is not acceptable' "$tmp/err" || fail "weak code message: $(cat "$tmp/err")"
[ ! -e "$r/opt/hatamoto/config/hatamoto.env" ] || fail "env written for a rejected code"
pass "weak override refused before anything is written"
r="$(mkroot baddomain)"
if run_fb "$r" HATAMOTO_DOMAIN=-bad.example.com; then fail "bad domain accepted"; fi
grep -q 'not a valid host name' "$tmp/err" || fail "domain message"
pass "invalid domain refused"
r="$(mkroot nobin)"; rm "$r/opt/hatamoto/bin/hatamoto"
if run_fb "$r"; then fail "ran without the build stage"; fi
grep -q 'run the build stage first' "$tmp/err" || fail "missing-build message"
pass "first boot without the build stage refused"

# --- domain: pending, then resolving ----------------------------------------------------
r="$(mkroot dnsp)"
: > "$tmp/calls"
run_fb "$r" HATAMOTO_DOMAIN=Example.COM FAKE_DNS_IP=198.51.100.9 || fail "pending-domain run failed: $(cat "$tmp/err")"
grep -qx '203-0-113-7.sslip.io {' "$r/etc/caddy/Caddyfile" || fail "pending domain must serve the sslip name"
grep -q 'Domain:     example.com (waiting for DNS -> 203.0.113.7;' "$tmp/out" || fail "pending line: $(cat "$tmp/out")"
grep -q 'will switch automatically' "$tmp/out" || fail "switch line"
grep -qx 'install_dns_switch pending=example.com host=203-0-113-7.sslip.io ip=203.0.113.7' "$tmp/calls" || fail "dns switch inputs: $(grep dns "$tmp/calls")"
pass "domain not pointing here: sslip name plus pending line"
r="$(mkroot dnsok)"
: > "$tmp/calls"
run_fb "$r" HATAMOTO_DOMAIN=example.com FAKE_DNS_IP=203.0.113.7 || fail "resolving-domain run failed: $(cat "$tmp/err")"
grep -qx 'example.com {' "$r/etc/caddy/Caddyfile" || fail "resolving domain must be served directly"
grep -qx 'PUBLIC_BASE_URL=https://example.com' "$r/opt/hatamoto/config/hatamoto.env" || fail "base url for resolving domain"
if grep -q 'Domain:' "$tmp/out"; then fail "pending line for a resolving domain"; fi
grep -qx 'install_dns_switch pending= host=example.com ip=203.0.113.7' "$tmp/calls" || fail "dns switch told about a resolved domain"
pass "domain pointing here is used directly"

# --- TLS modes through the run ------------------------------------------------------------
r="$(mkroot tlsint)"
run_fb "$r" HATAMOTO_TLS=internal || fail "internal run failed"
grep -qxP '\ttls internal' "$r/etc/caddy/Caddyfile" || fail "tls internal not written"
grep -q -- '-fsSk' "$tmp/calls" || fail "internal CA health check should skip verification"
r="$(mkroot tlsoff)"
run_fb "$r" HATAMOTO_TLS=off || fail "off run failed"
grep -qx 'http://203-0-113-7.sslip.io {' "$r/etc/caddy/Caddyfile" || fail "http site"
grep -qx 'PUBLIC_BASE_URL=http://203-0-113-7.sslip.io' "$r/opt/hatamoto/config/hatamoto.env" || fail "http base url"
pass "TLS modes end to end"

# No answer through Caddy: a failure for internal/off, a warning for auto.
for m in internal off; do
  r="$(mkroot "noproxy$m")"
  if run_fb "$r" HATAMOTO_TLS=$m FAKE_PROXY_FAIL=1; then fail "TLS=$m: a missing answer through Caddy was accepted"; fi
  grep -q 'no answer through Caddy' "$tmp/err" || fail "TLS=$m: message: $(cat "$tmp/err")"
  [ ! -e "$r/var/lib/hatamoto-install/firstboot.done" ] || fail "TLS=$m: done marker after a proxy failure"
done
r="$(mkroot noproxyauto)"
run_fb "$r" FAKE_PROXY_FAIL=1 || fail "TLS=auto: a pending certificate must only warn: $(cat "$tmp/err")"
grep -q 'warning: no answer through Caddy yet' "$tmp/err" || fail "TLS=auto warning missing"
pass "no answer through Caddy: failure for internal/off, warning for auto"

# --- public IP detection --------------------------------------------------------------------
r="$(mkroot ipdetect)"
run_fb "$r" HATAMOTO_PUBLIC_IP= FAKE_IP=198.51.100.20 || fail "ip detection run failed: $(cat "$tmp/err")"
grep -q '198-51-100-20.sslip.io' "$r/etc/caddy/Caddyfile" || fail "detected IP not used"
pass "public IP detection"

# fb_public_ip against fake ip and curl: out=stdout, $tmp/pip.err=stderr
pip() { PATH="$bin:$PATH" CALLS=/dev/null pure fb_public_ip 2>"$tmp/pip.err"; }
[ "$(FAKE_IP=198.51.100.20 FAKE_LOCAL_IPS="10.0.0.5 198.51.100.20" FAKE_ROUTE_SRC=198.51.100.20 pip)" = 198.51.100.20 ] || fail "ipify answer bound locally not used"
[ ! -s "$tmp/pip.err" ] || fail "warning for a matching answer: $(cat "$tmp/pip.err")"
[ "$(FAKE_IP=198.51.100.20 FAKE_LOCAL_IPS="203.0.113.9" FAKE_ROUTE_SRC=203.0.113.9 pip)" = 203.0.113.9 ] || fail "a differing ipify answer beat the local public address"
{ grep -q '198.51.100.20' "$tmp/pip.err" && grep -q '203.0.113.9' "$tmp/pip.err"; } || fail "mismatch warning must name both: $(cat "$tmp/pip.err")"
[ "$(FAKE_IP=198.51.100.20 FAKE_LOCAL_IPS="10.0.0.5" FAKE_ROUTE_SRC=10.0.0.5 pip)" = 198.51.100.20 ] || fail "NAT: ipify answer not used"
{ grep -q '198.51.100.20' "$tmp/pip.err" && grep -q '10.0.0.5' "$tmp/pip.err" && grep -q 'HATAMOTO_PUBLIC_IP' "$tmp/pip.err"; } || fail "NAT warning: $(cat "$tmp/pip.err")"
for badip in 10.1.2.3 192.168.1.1 172.20.0.1 127.0.0.1 0.0.0.0 169.254.1.1 100.64.0.1 100.127.255.254; do
  [ "$(FAKE_IP=$badip FAKE_LOCAL_IPS="203.0.113.9" FAKE_ROUTE_SRC=203.0.113.9 pip)" = 203.0.113.9 ] || fail "ipify answer $badip accepted"
  grep -q 'not a public address' "$tmp/pip.err" || fail "no message for $badip"
done
[ "$(FAKE_IP=172.32.0.1 FAKE_LOCAL_IPS="172.32.0.1" pip)" = 172.32.0.1 ] || fail "172.32.0.1 is public"
for goodip in 100.63.0.1 100.128.0.1; do
  [ "$(FAKE_IP=$goodip FAKE_LOCAL_IPS="$goodip" pip)" = "$goodip" ] || fail "$goodip is public"
done
[ "$(FAKE_LOCAL_IPS="" FAKE_ROUTE_SRC=203.0.113.9 pip)" = 203.0.113.9 ] || fail "no ipify answer: route source not used"
[ "$(HATAMOTO_PUBLIC_IP=192.0.2.1 FAKE_IP=198.51.100.20 pip)" = 192.0.2.1 ] || fail "explicit HATAMOTO_PUBLIC_IP lost"
pass "public IP: ipify answer cross-checked against local addresses"

# --- admin email validation
for good in ops@example.com a.b+c@sub.example.org ops@example.xn--p1ai; do pure fb_valid_email "$good" || fail "good email refused: $good"; done
for bad in ops@example.c ops@example.1com ops@example.com- ops@example.-com 'a b@x.com' 'x@' 'x@y' 'ops@example.com}' $'ops@example.com\nx@example.com' '@example.com'; do
  if pure fb_valid_email "$bad"; then fail "bad email accepted: $bad"; fi
done
r="$(mkroot badmail)"
if run_fb "$r" HATAMOTO_ADMIN_EMAIL='a b@x.com'; then fail "bad email accepted by the stage"; fi
grep -q 'HATAMOTO_ADMIN_EMAIL is not an email address' "$tmp/err" || fail "bad email not explained: $(cat "$tmp/err")"
[ ! -e "$r/var/lib/hatamoto-install/firstboot.done" ] || fail "done marker after a bad email"
pass "admin email validated"

# --- the saved inputs file may lack a final newline
r="$(mkroot nonl)"
mkdir -p "$r/etc/default"; printf 'HATAMOTO_TLS=auto' > "$r/etc/default/hatamoto-install"
run_fb "$r" HATAMOTO_DOMAIN=cms.example.com || fail "no-newline run failed: $(cat "$tmp/err")"
{ grep -qx 'HATAMOTO_TLS=auto' "$r/etc/default/hatamoto-install" && grep -qx 'HATAMOTO_DOMAIN=cms.example.com' "$r/etc/default/hatamoto-install"; } || fail "inputs glued onto one line: $(cat "$r/etc/default/hatamoto-install")"
pass "inputs appended on their own line"

# --- a failing role statement never shows the password
r="$(mkroot psqlfail)"
if run_fb "$r" FAKE_PSQL_FAIL=1; then fail "psql failure ignored"; fi
dbpw="$(sed -n "s/.*PASSWORD '\([^']*\)'.*/\1/p" "$tmp/state/psql-stdin")"
[ "${#dbpw}" -ge 32 ] || fail "no password to look for"
grep -q 'boom' "$tmp/err" || fail "psql error not shown"
if grep -q -e "$dbpw" "$tmp/out" "$tmp/err"; then fail "database password in the output of a failed psql"; fi
grep -q -e '-v VERBOSITY=terse' "$tmp/calls" || fail "psql not run with terse errors"
pass "psql errors are terse"

# --- resume after a failed migrate keeps the secrets ------------------------------------------
r="$(mkroot resume)"
if FAKE_MIGRATE_FAIL=1 run_fb "$r"; then fail "migrate failure ignored"; fi
[ ! -e "$r/var/lib/hatamoto-install/firstboot.done" ] || fail "done marker after a failure"
first="$(cat "$r/opt/hatamoto/config/hatamoto.env")"
# run_fb recreates the state dir; keep the role/db markers a real server would still have.
: > "$tmp/calls"
rm -rf "$tmp/state"; mkdir -p "$tmp/state"; : > "$tmp/state/role"; : > "$tmp/state/db"; : > "$tmp/state/migrate-failed"
# shellcheck disable=SC2016 # the single-quoted script expands in the child shell
env PATH="$bin:$PATH" CALLS="$tmp/calls" TMPD="$tmp/state" HATAMOTO_INSTALL_ROOT="$r" HATAMOTO_INSTALL_SOURCE_ONLY=1 \
  HATAMOTO_FIRSTBOOT_POLL=0 HATAMOTO_PUBLIC_IP=203.0.113.7 sh -c '. "$1"
    install_motd() { :; }; install_setupcode_clean() { :; }; install_dns_switch() { :; }; setup_swap() { :; }; setup_backups() { :; }
    stage_firstboot' _ "$fbscript" >"$tmp/out" 2>"$tmp/err" || fail "resume failed: $(cat "$tmp/err")"
[ "$(cat "$r/opt/hatamoto/config/hatamoto.env")" = "$first" ] || fail "resume changed the secrets"
grep -q 'ALTER ROLE hatamoto' "$tmp/state/psql-stdin" || fail "resume did not reset the role password"
[ -f "$r/var/lib/hatamoto-install/firstboot.done" ] || fail "resume did not finish"
pass "resume keeps secrets and finishes"

# --- resume after the clean-up timer removed the setup code ----------------------------------------
r="$(mkroot nocode)"
if FAKE_MIGRATE_FAIL=1 run_fb "$r"; then fail "migrate failure ignored"; fi
envf="$r/opt/hatamoto/config/hatamoto.env"
grep -v '^HATAMOTO_SETUP_CODE=' "$envf" > "$tmp/env.nocode"; cp "$tmp/env.nocode" "$envf"
if run_fb "$r" FAKE_OWNERS=0; then fail "a missing code was accepted with no owner"; fi
grep -q 'incomplete' "$tmp/err" || fail "no-owner refusal not explained: $(cat "$tmp/err")"
[ ! -e "$r/var/lib/hatamoto-install/firstboot.done" ] || fail "done marker without code or owner"
: > "$tmp/calls"
run_fb "$r" FAKE_OWNERS=1 || fail "resume without a code but with an owner failed: $(cat "$tmp/err")"
[ -f "$r/var/lib/hatamoto-install/firstboot.done" ] || fail "resume did not finish"
if grep -q 'SETUP_CODE' "$envf"; then fail "setup code written back"; fi
if grep -q 'Setup code' "$tmp/out"; then fail "banner printed without a code"; fi
pass "resume accepts a removed setup code once the owner exists"
# The clean timer is installed after the backups part, just before the done marker.
bn="$(grep -nx 'setup_backups' "$tmp/calls" | head -n 1 | cut -d: -f1)"
cn="$(grep -nx 'install_setupcode_clean' "$tmp/calls" | head -n 1 | cut -d: -f1)"
{ [ -n "$bn" ] && [ -n "$cn" ] && [ "$bn" -lt "$cn" ]; } || fail "clean timer not installed after setup_backups"
pass "setup code clean timer installed last"

# --- a failed run keeps the inputs for the resume at the next boot ----------------------------------
r="$(mkroot inputs)"
inputs=(HATAMOTO_DOMAIN=cms.example.com HATAMOTO_TLS=internal HATAMOTO_BACKUPS=0 HATAMOTO_FIREWALL=0 HATAMOTO_ADMIN_EMAIL=ops@example.com HATAMOTO_VERSION=v1.2.3)
if FAKE_MIGRATE_FAIL=1 run_fb "$r" "${inputs[@]}" HATAMOTO_SETUP_CODE=k7m2-rt9p-qw4x; then fail "migrate failure ignored"; fi
saved="$r/etc/default/hatamoto-install"
[ "$(stat -c %a "$saved")" = 600 ] || fail "saved inputs mode $(stat -c %a "$saved" 2>&1)"
for want in "${inputs[@]}" HATAMOTO_PUBLIC_IP=203.0.113.7; do grep -qxF "$want" "$saved" || fail "saved inputs lack $want: $(cat "$saved")"; done
if grep -q 'SETUP_CODE\|k7m2' "$saved"; then fail "the setup code was saved in the inputs file"; fi
# The resumed run (the unit) starts with only what the file holds, as systemd's EnvironmentFile would.
mapfile -t fromfile < "$saved"
rm -rf "$tmp/state"; mkdir -p "$tmp/state"; : > "$tmp/state/role"; : > "$tmp/state/db"; : > "$tmp/state/migrate-failed"
# shellcheck disable=SC2016 # the single-quoted script expands in the child shell
run_resume() { env -i PATH="$bin:$PATH" CALLS="$tmp/calls" TMPD="$tmp/state" HATAMOTO_INSTALL_ROOT="$r" HATAMOTO_INSTALL_SOURCE_ONLY=1 \
  HATAMOTO_FIRSTBOOT_POLL=0 "${fromfile[@]}" sh -c '. "$1"
    install_motd() { :; }; install_setupcode_clean() { :; }; install_dns_switch() { echo "pending=$FB_PENDING_DOMAIN" >> "$CALLS"; }; setup_swap() { :; }; setup_backups() { :; }
    stage_firstboot' _ "$fbscript" >"$tmp/out" 2>"$tmp/err"; }
: > "$tmp/calls"
run_resume || fail "resume from the saved inputs failed: $(cat "$tmp/err")"
grep -qxF $'\ttls internal' "$r/etc/caddy/Caddyfile" || fail "resume lost HATAMOTO_TLS"
grep -qxF $'\temail ops@example.com' "$r/etc/caddy/Caddyfile" || fail "resume lost HATAMOTO_ADMIN_EMAIL"
grep -qx 'pending=cms.example.com' "$tmp/calls" || fail "resume lost HATAMOTO_DOMAIN"
pass "a failed run saves its inputs (0600, no setup code) and the resume sees them"
# An image author's value is never replaced.
r="$(mkroot inputs-keep)"
mkdir -p "$r/etc/default"; printf 'HATAMOTO_TLS=internal\n' > "$r/etc/default/hatamoto-install"
run_fb "$r" HATAMOTO_TLS=off HATAMOTO_DOMAIN=cms.example.com || fail "keep run failed: $(cat "$tmp/err")"
{ [ "$(grep -c '^HATAMOTO_TLS=' "$r/etc/default/hatamoto-install")" = 1 ] && grep -qx 'HATAMOTO_TLS=internal' "$r/etc/default/hatamoto-install"; } || fail "saved TLS value was replaced"
grep -qx 'HATAMOTO_DOMAIN=cms.example.com' "$r/etc/default/hatamoto-install" || fail "missing input not added"
pass "values already in the inputs file are kept"

# --- host keys of a sealed image ----------------------------------------------------------------
r="$(mkroot keys)"; rm "$r/etc/ssh/ssh_host_ed25519_key"
: > "$tmp/calls"
run_fb "$r" || fail "keys run failed"
grep -q '^ssh-keygen -A' "$tmp/calls" || fail "host keys not generated"
grep -q '^systemctl restart ssh' "$tmp/calls" || fail "sshd not restarted"
pass "missing SSH host keys are generated"

echo "ok: test-firstboot"
