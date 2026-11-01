#!/usr/bin/env bash
# test-motd-dns.sh - install_motd and install_dns_switch against fake commands and a fixture root.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

bash "$here/build-install.sh" --out "$tmp/asm" >/dev/null
fb="$tmp/asm/firstboot.sh"

bin="$tmp/bin"; mkdir -p "$bin"
mk() { cat > "$bin/$1"; chmod +x "$bin/$1"; }
mk sudo <<'F'
#!/bin/sh
[ "$1" != -n ] || shift
[ "$1" != -u ] || shift 2
exec "$@"
F
mk psql <<'F'
#!/bin/sh
echo "${FAKE_OWNERS:-0}"
F
mk systemctl <<'F'
#!/bin/sh
echo "systemctl $*" >> "$CALLS"
F
mk curl <<'F'
#!/bin/sh
echo ok
F
mk getent <<'F'
#!/bin/sh
[ -n "${FAKE_DNS_IP:-}" ] || exit 2
printf '%s STREAM %s\n' "$FAKE_DNS_IP" "$2"
F
mk install <<'F'
#!/bin/sh
dirs=0; mode=""; args=""
while [ $# -gt 0 ]; do
  case "$1" in -d) dirs=1 ;; -m) mode="$2"; shift ;; -o | -g) shift ;; *) args="$args $1" ;; esac
  shift
done
set -- $args
if [ "$dirs" = 1 ]; then mkdir -p "$@"; [ -z "$mode" ] || chmod "$mode" "$@"; else cp "$1" "$2"; [ -z "$mode" ] || chmod "$mode" "$2"; fi
F
cat > "$tmp/hatamoto.fake" <<'F'
#!/bin/sh
echo "$PUBLIC_BASE_URL/k7qm-x2rf-9tdc"
F

mkroot() {
  local r="$tmp/$1"
  mkdir -p "$r/opt/hatamoto/bin" "$r/opt/hatamoto/config" "$r/etc/caddy" "$r/usr/local/lib/hatamoto" "$r/var/lib/hatamoto-install"
  cp "$tmp/hatamoto.fake" "$r/opt/hatamoto/bin/hatamoto"; chmod +x "$r/opt/hatamoto/bin/hatamoto"
  cp "$fb" "$r/usr/local/lib/hatamoto/firstboot.sh"
  printf 'DATABASE_URL=postgres://hatamoto:pw@127.0.0.1/hatamoto\nPUBLIC_BASE_URL=https://203-0-113-7.sslip.io\nHATAMOTO_SETUP_CODE=K7M2-RT9P-QW4X\n' > "$r/opt/hatamoto/config/hatamoto.env"
  chmod 0640 "$r/opt/hatamoto/config/hatamoto.env"
  echo "https://203-0-113-7.sslip.io/k7qm-x2rf-9tdc" > "$r/var/lib/hatamoto-install/link"
  echo "$r"
}
envrun() { local r="$1"; shift; env PATH="$bin:$PATH" CALLS="$tmp/calls" HATAMOTO_INSTALL_ROOT="$r" HATAMOTO_FIRSTBOOT_POLL=0 "$@"; }
setup() { # setup ROOT PENDING_DOMAIN
  # shellcheck disable=SC2016 # the single-quoted script expands in the child shell
  envrun "$1" HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; FB_PENDING_DOMAIN="$2"; FB_IP=203.0.113.7; install_motd; install_dns_switch' _ "$fb" "$2"
}

# --- MOTD
r="$(mkroot motd)"; : > "$tmp/calls"
setup "$r" "" >/dev/null 2>&1 || fail "setup failed"
m="$r/etc/update-motd.d/99-hatamoto"
{ [ -x "$m" ] && [ "$(stat -c %a "$m")" = 755 ] && [ "$(head -n1 "$m")" = '#!/bin/sh' ]; } || fail "motd file"
out="$(envrun "$r" FAKE_OWNERS=0 sh "$m")"
grep -q 'Setup code: K7M2-RT9P-QW4X' <<<"$out" || fail "no code: $out"
grep -q '/k7qm-x2rf-9tdc/setup#code=K7M2-RT9P-QW4X' <<<"$out" || fail "no link"
grep -q 'Updates:    automatic (stable channel)' <<<"$out" || fail "no updates line"
if grep -q 'Domain:' <<<"$out"; then fail "domain line without pending"; fi
pass "motd shows code and link while no owner exists"
[ -z "$(envrun "$r" FAKE_OWNERS=1 sh "$m")" ] || fail "motd not silent once the owner exists"
pass "motd silent once the owner exists"
{ [ ! -e "$r/etc/systemd/system/hatamoto-dns-switch.timer" ]; } || fail "timer without a pending domain"

# --- pending domain
r="$(mkroot pend)"; : > "$tmp/calls"
setup "$r" example.com >/dev/null 2>&1 || fail "pending setup failed"
out="$(envrun "$r" FAKE_OWNERS=0 sh "$r/etc/update-motd.d/99-hatamoto")"
grep -q 'Domain:     example.com (waiting for DNS -> 203.0.113.7;' <<<"$out" || fail "pending line: $out"
t="$r/etc/systemd/system/hatamoto-dns-switch"
{ grep -q 'OnUnitActiveSec=5min' "$t.timer" && grep -q 'Type=oneshot' "$t.service"; } || fail "timer or service text"
if grep -q '%h' "$t.service" "$t.timer"; then fail "%h in units"; fi
grep -qx 'systemctl enable --now hatamoto-dns-switch.timer' "$tmp/calls" || fail "timer not enabled"
pass "pending domain: line shown, timer installed"

sw() { envrun "$r" "$@" sh "$r/usr/local/lib/hatamoto/dns-switch.sh"; }
envf="$r/opt/hatamoto/config/hatamoto.env"
cp "$envf" "$tmp/env.before"; cp "$r/etc/caddy/Caddyfile" "$tmp/cf" 2>/dev/null || true
: > "$tmp/calls"
sw FAKE_DNS_IP=198.51.100.9 >"$tmp/o" 2>&1 || fail "mismatch run failed"
cmp -s "$envf" "$tmp/env.before" || fail "env changed on mismatch"
[ ! -e "$r/etc/caddy/Caddyfile" ] || fail "caddyfile written on mismatch"
if grep -q 'restart hatamoto-api' "$tmp/calls"; then fail "restart on mismatch"; fi
pass "dns mismatch changes nothing"

: > "$tmp/calls"
sw FAKE_DNS_IP=203.0.113.7 >"$tmp/o" 2>&1 || fail "match run failed: $(cat "$tmp/o")"
grep -qx 'PUBLIC_BASE_URL=https://example.com' "$envf" || fail "env not switched"
grep -qx 'HATAMOTO_SETUP_CODE=K7M2-RT9P-QW4X' "$envf" || fail "env lost lines"
[ "$(stat -c %a "$envf")" = 640 ] || fail "env perms $(stat -c %a "$envf")"
grep -qx 'example.com {' "$r/etc/caddy/Caddyfile" || fail "caddyfile not switched"
{ grep -qx 'systemctl restart hatamoto-api' "$tmp/calls" && grep -qx 'systemctl reload caddy' "$tmp/calls"; } || fail "services not cycled"
grep -qx 'systemctl disable --now hatamoto-dns-switch.timer' "$tmp/calls" || fail "timer not disabled"
[ "$(cat "$r/var/lib/hatamoto-install/link")" = "https://example.com/k7qm-x2rf-9tdc" ] || fail "link not refreshed"
[ "$(stat -c %a "$r/var/lib/hatamoto-install/link")" = 600 ] || fail "link perms"
if grep -q 'K7M2' "$tmp/o"; then fail "code printed by the timer script"; fi
pass "dns match switches env, Caddyfile, link; disables timer; keeps perms; no code in output"

h="$(sha256sum "$envf" "$r/etc/caddy/Caddyfile" | sha256sum)"
: > "$tmp/calls"
sw FAKE_DNS_IP=203.0.113.7 >/dev/null 2>&1 || fail "rerun failed"
[ "$h" = "$(sha256sum "$envf" "$r/etc/caddy/Caddyfile" | sha256sum)" ] || fail "rerun changed files"
if grep -q 'restart hatamoto-api' "$tmp/calls"; then fail "rerun restarted"; fi
pass "rerun idempotent"

# --- invalid domain
r="$(mkroot bad)"
if setup "$r" -evil.example.com >/dev/null 2>"$tmp/err"; then fail "bad domain accepted"; fi
grep -q 'not a valid host name' "$tmp/err" || fail "message"
{ [ ! -e "$r/etc/systemd/system/hatamoto-dns-switch.timer" ]; } || fail "timer for bad domain"
pass "invalid domain rejected"

# --- resolved domain removes a stale timer
r="$(mkroot stale)"; setup "$r" example.com >/dev/null 2>&1; setup "$r" "" >/dev/null 2>&1
{ [ ! -e "$r/etc/systemd/system/hatamoto-dns-switch.timer" ] && [ ! -e "$r/var/lib/hatamoto-install/dns-pending" ]; } || fail "stale timer kept"
pass "no pending domain removes the timer"

# --- setup code clean-up
r="$(mkroot clean)"; : > "$tmp/calls"
# shellcheck disable=SC2016 # the single-quoted script expands in the child shell
envrun "$r" HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; install_setupcode_clean' _ "$fb" >/dev/null 2>&1 || fail "clean setup failed"
c="$r/etc/systemd/system/hatamoto-setupcode-clean"
{ grep -q 'OnUnitActiveSec=5min' "$c.timer" && grep -q 'Type=oneshot' "$c.service" && [ -x "$r/usr/local/lib/hatamoto/setupcode-clean.sh" ]; } || fail "clean units or script missing"
grep -qx 'systemctl enable --now hatamoto-setupcode-clean.timer' "$tmp/calls" || fail "clean timer not enabled"
envf="$r/opt/hatamoto/config/hatamoto.env"
printf 'ONLY=after\n' >> "$envf"
cl() { envrun "$r" "$@" sh "$r/usr/local/lib/hatamoto/setupcode-clean.sh"; }
cp "$envf" "$tmp/env.before"; : > "$tmp/calls"
cl FAKE_OWNERS=0 >"$tmp/o" 2>&1 || fail "no-owner run failed"
cmp -s "$envf" "$tmp/env.before" || fail "env changed with no owner"
if grep -q 'disable' "$tmp/calls"; then fail "timer disabled with no owner"; fi
pass "no owner yet: the setup code stays"
cl FAKE_OWNERS=1 >"$tmp/o" 2>&1 || fail "owner run failed: $(cat "$tmp/o")"
if grep -q 'SETUP_CODE' "$envf"; then fail "setup code kept"; fi
grep -v '^HATAMOTO_SETUP_CODE=' "$tmp/env.before" | cmp -s - "$envf" || fail "other lines changed"
[ "$(stat -c %a "$envf")" = 640 ] || fail "env perms $(stat -c %a "$envf")"
[ "$(find "$(dirname "$envf")" -mindepth 1 -maxdepth 1 | wc -l)" = 1 ] || fail "temp file left behind"
grep -qx 'systemctl disable --now hatamoto-setupcode-clean.timer' "$tmp/calls" || fail "timer not disabled"
if grep -q 'K7M2' "$tmp/o"; then fail "code printed by the clean-up script"; fi
pass "owner exists: only the setup code line is removed, mode kept, timer disabled, nothing printed"
h="$(sha256sum "$envf")"; : > "$tmp/calls"
cl FAKE_OWNERS=1 >/dev/null 2>&1 || fail "rerun failed"
[ "$h" = "$(sha256sum "$envf")" ] || fail "rerun changed the env file"
if grep -q 'restart' "$tmp/calls"; then fail "rerun restarted something"; fi
pass "rerun is a no-op"

# --- both scripts edit one env file: they take turns under the shared lock
r="$(mkroot race)"; setup "$r" example.com >/dev/null 2>&1
# shellcheck disable=SC2016 # the single-quoted script expands in the child shell
envrun "$r" HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; install_setupcode_clean' _ "$fb" >/dev/null 2>&1 || fail "clean setup failed"
envf="$r/opt/hatamoto/config/hatamoto.env"
mkdir -p "$r/run"; mkfifo "$tmp/release"
( flock 9; timeout 10 cat "$tmp/release" >/dev/null ) 9> "$r/run/hatamoto-env.lock" &
holder=$!
sleep 0.3
cl() { envrun "$r" "$@" sh "$r/usr/local/lib/hatamoto/setupcode-clean.sh"; }
cp "$envf" "$tmp/env.before"
sw FAKE_DNS_IP=203.0.113.7 >"$tmp/o1" 2>&1 & p1=$!
cl FAKE_OWNERS=1 >"$tmp/o2" 2>&1 & p2=$!
sleep 0.5
cmp -s "$envf" "$tmp/env.before" || fail "env edited while the lock was held"
echo go > "$tmp/release"
wait "$p1" "$p2" "$holder" || fail "a script failed: $(cat "$tmp/o1" "$tmp/o2")"
grep -qx 'PUBLIC_BASE_URL=https://example.com' "$envf" || fail "dns edit lost"
if grep -q SETUP_CODE "$envf"; then fail "clean edit lost: $(cat "$tmp/o1" "$tmp/o2"; sed "s/=.*//" "$envf")"; fi
[ "$(stat -c %a "$envf")" = 640 ] || fail "env perms"
[ "$(find "$r/opt/hatamoto/config" -mindepth 1 -maxdepth 1 | wc -l)" = 1 ] || fail "temp file left: $(find "$r/opt/hatamoto/config" -mindepth 1 -maxdepth 1)"
pass "env edits are serialised, both applied, no temp left"

# --- set-domain
sd_root() { # sd_root NAME [CADDYFILE_TEXT]
  local d; d="$(mkroot "$1")"
  : > "$d/var/lib/hatamoto-install/firstboot.done"
  printf '%s' "${2:-}" > "$d/etc/caddy/Caddyfile"
  echo "$d"
}
sdrun() { local d="$1"; shift; envrun "$d" HATAMOTO_PUBLIC_IP=203.0.113.7 "$@" sh "$d/usr/local/lib/hatamoto/firstboot.sh" set-domain "${SD_ARGS[@]}"; }
auto_cf=$'# Written by the Hatamoto installer.\n{\n\temail ops@example.com\n}\n203-0-113-7.sslip.io {\n\treverse_proxy 127.0.0.1:8090\n}\n'

# DNS ok: env, both site blocks, restart, ready wait, link, no pending, timer off
r="$(sd_root sdok "$auto_cf")"; setup "$r" example.com >/dev/null 2>&1
rm -f "$r/etc/caddy/Caddyfile"; printf '%s' "$auto_cf" > "$r/etc/caddy/Caddyfile"
: > "$tmp/calls"
SD_ARGS=(Cms.Example.com --email you@example.com)
sdrun "$r" FAKE_DNS_IP=203.0.113.7 >"$tmp/o" 2>&1 || fail "set-domain failed: $(cat "$tmp/o")"
envf="$r/opt/hatamoto/config/hatamoto.env"
grep -qx 'PUBLIC_BASE_URL=https://cms.example.com' "$envf" || fail "set-domain env"
grep -qx 'HATAMOTO_SETUP_CODE=K7M2-RT9P-QW4X' "$envf" || fail "set-domain env lost lines"
cfx="$r/etc/caddy/Caddyfile"
grep -qx 'cms.example.com {' "$cfx" || fail "new host block"
grep -qx '203-0-113-7.sslip.io {' "$cfx" || fail "old host block"
grep -qxP '\tredir https://cms.example.com\{uri\} permanent' "$cfx" || fail "redirect"
grep -qxP '\temail you@example.com' "$cfx" || fail "email flag ignored"
{ grep -qx 'systemctl restart hatamoto-api' "$tmp/calls" && grep -qx 'systemctl reload caddy' "$tmp/calls"; } || fail "set-domain services"
grep -qx 'systemctl disable --now hatamoto-dns-switch.timer' "$tmp/calls" || fail "set-domain timer not disabled"
[ ! -e "$r/var/lib/hatamoto-install/dns-pending" ] || fail "pending kept"
[ "$(cat "$r/var/lib/hatamoto-install/link")" = "https://cms.example.com/k7qm-x2rf-9tdc" ] || fail "set-domain link"
grep -q 'Admin: https://cms.example.com/k7qm-x2rf-9tdc' "$tmp/o" || fail "admin link not printed"
if grep -q 'K7M2' "$tmp/o"; then fail "set-domain printed the setup code"; fi
for f in "$r"/etc/caddy/*; do
  [ "${f##*/}" = Caddyfile ] || fail "caddy temp left: ${f##*/}"
done
pass "set-domain with DNS ok rewrites env and Caddyfile, keeps the old host as a redirect"

# same host again: no-op
h="$(sha256sum "$envf" "$cfx" | sha256sum)"; : > "$tmp/calls"
SD_ARGS=(cms.example.com)
sdrun "$r" FAKE_DNS_IP=203.0.113.7 >"$tmp/o" 2>&1 || fail "same-host run failed"
[ "$h" = "$(sha256sum "$envf" "$cfx" | sha256sum)" ] || fail "same host changed files"
if grep -q restart "$tmp/calls"; then fail "same host restarted"; fi
pass "set-domain with the current host is a no-op"

# a second domain replaces the old host
SD_ARGS=(www.example.com)
sdrun "$r" FAKE_DNS_IP=203.0.113.7 >"$tmp/o" 2>&1 || fail "second set-domain failed: $(cat "$tmp/o")"
grep -qx 'cms.example.com {' "$cfx" || fail "previous host not the redirect"
if grep -q 'sslip.io' "$cfx"; then fail "two old hosts kept"; fi
grep -qx 'PUBLIC_BASE_URL=https://www.example.com' "$envf" || fail "second env"
pass "set-domain keeps only the last old host"

# DNS not ok: nothing rewritten, pending + timer
r="$(sd_root sdwait "$auto_cf")"; : > "$tmp/calls"
cp "$r/opt/hatamoto/config/hatamoto.env" "$tmp/env.before"; cp "$r/etc/caddy/Caddyfile" "$tmp/cf.before"
SD_ARGS=(cms.example.com --email you@example.com)
sdrun "$r" FAKE_DNS_IP=198.51.100.9 >"$tmp/o" 2>&1 || fail "waiting set-domain failed: $(cat "$tmp/o")"
cmp -s "$r/opt/hatamoto/config/hatamoto.env" "$tmp/env.before" || fail "env changed while waiting"
cmp -s "$r/etc/caddy/Caddyfile" "$tmp/cf.before" || fail "Caddyfile changed while waiting"
[ "$(cat "$r/var/lib/hatamoto-install/dns-pending")" = "cms.example.com 203.0.113.7 auto you@example.com" ] || fail "pending: $(cat "$r/var/lib/hatamoto-install/dns-pending")"
grep -qx 'systemctl enable --now hatamoto-dns-switch.timer' "$tmp/calls" || fail "timer not enabled"
if grep -q 'restart hatamoto-api' "$tmp/calls"; then fail "restart while waiting"; fi
grep -q 'waiting for DNS -> 203.0.113.7' "$tmp/o" || fail "no waiting message: $(cat "$tmp/o")"
[ -x "$r/usr/local/lib/hatamoto/dns-switch.sh" ] || fail "dns-switch.sh not refreshed"
pass "set-domain with DNS not ready queues the switch and changes nothing else"

# the queued switch then moves the site and keeps the redirect
: > "$tmp/calls"
sw FAKE_DNS_IP=203.0.113.7 >"$tmp/o" 2>&1 || fail "queued switch failed: $(cat "$tmp/o")"
grep -qx 'cms.example.com {' "$r/etc/caddy/Caddyfile" || fail "queued switch Caddyfile"
grep -qx '203-0-113-7.sslip.io {' "$r/etc/caddy/Caddyfile" || fail "queued switch redirect"
pass "the DNS-switch timer script keeps the old host as a redirect"

# TLS mode derived from the Caddyfile, and from the saved inputs
r="$(sd_root sdint $'203-0-113-7.sslip.io {\n\ttls internal\n\treverse_proxy 127.0.0.1:8090\n}\n')"
SD_ARGS=(cms.example.com)
sdrun "$r" FAKE_DNS_IP=203.0.113.7 >"$tmp/o" 2>&1 || fail "internal set-domain failed: $(cat "$tmp/o")"
grep -qxP '\ttls internal' "$r/etc/caddy/Caddyfile" || fail "tls internal lost"
grep -qx 'PUBLIC_BASE_URL=https://cms.example.com' "$r/opt/hatamoto/config/hatamoto.env" || fail "internal base url"
r="$(sd_root sdoff $'http://203-0-113-7.sslip.io {\n\treverse_proxy 127.0.0.1:8090\n}\n')"
sdrun "$r" FAKE_DNS_IP=203.0.113.7 >"$tmp/o" 2>&1 || fail "off set-domain failed"
grep -qx 'http://cms.example.com {' "$r/etc/caddy/Caddyfile" || fail "tls off lost"
grep -qx 'PUBLIC_BASE_URL=http://cms.example.com' "$r/opt/hatamoto/config/hatamoto.env" || fail "off base url"
r="$(sd_root sdsaved "$auto_cf")"; mkdir -p "$r/etc/default"; printf 'HATAMOTO_TLS=off\nHATAMOTO_ADMIN_EMAIL=saved@example.com\n' > "$r/etc/default/hatamoto-install"
sdrun "$r" FAKE_DNS_IP=198.51.100.9 >"$tmp/o" 2>&1 || fail "saved-input set-domain failed"
[ "$(cat "$r/var/lib/hatamoto-install/dns-pending")" = "cms.example.com 203.0.113.7 off saved@example.com" ] || fail "saved inputs not used"
pass "set-domain derives TLS mode and email from saved inputs or the Caddyfile"

# bad arguments
r="$(sd_root sdbad "$auto_cf")"; cp "$r/opt/hatamoto/config/hatamoto.env" "$tmp/env.before"
for bad in "-evil.example.com" "a b" "cms.example.com --email" "cms.example.com --email no-at" "cms.example.com --bogus" ""; do
  # shellcheck disable=SC2206 # intentional word splitting of the case string
  SD_ARGS=($bad)
  if sdrun "$r" FAKE_DNS_IP=203.0.113.7 >/dev/null 2>&1; then fail "accepted: $bad"; fi
done
cmp -s "$r/opt/hatamoto/config/hatamoto.env" "$tmp/env.before" || fail "env changed by a refused call"
r="$(mkroot nodone)"; SD_ARGS=(cms.example.com)
if sdrun "$r" FAKE_DNS_IP=203.0.113.7 >/dev/null 2>&1; then fail "set-domain before first boot accepted"; fi
pass "set-domain refuses bad host, bad email, missing value and an unfinished install"

# a failed caddy reload restores the Caddyfile
r="$(sd_root sdrb "$auto_cf")"; cp "$r/etc/caddy/Caddyfile" "$tmp/cf.before"
mk systemctl <<'F'
#!/bin/sh
echo "systemctl $*" >> "$CALLS"
case "$*" in *caddy*) [ -z "${FAKE_CADDY_FAIL:-}" ] || exit 1 ;; esac
F
SD_ARGS=(cms.example.com)
cp "$r/opt/hatamoto/config/hatamoto.env" "$tmp/env.before"
if sdrun "$r" FAKE_DNS_IP=203.0.113.7 FAKE_CADDY_FAIL=1 >"$tmp/o" 2>&1; then fail "caddy failure not reported"; fi
cmp -s "$r/etc/caddy/Caddyfile" "$tmp/cf.before" || fail "Caddyfile not restored"
pass "a Caddyfile caddy rejects is rolled back"

# the failure left env and Caddyfile untouched, so a second run with a working caddy switches
cmp -s "$r/opt/hatamoto/config/hatamoto.env" "$tmp/env.before" || fail "env not restored after caddy failure"
grep -q 'previous Caddyfile and PUBLIC_BASE_URL' "$tmp/o" || fail "rollback message: $(cat "$tmp/o")"
sdrun "$r" FAKE_DNS_IP=203.0.113.7 >"$tmp/o" 2>&1 || fail "retry failed: $(cat "$tmp/o")"
grep -qx 'PUBLIC_BASE_URL=https://cms.example.com' "$r/opt/hatamoto/config/hatamoto.env" || fail "retry did not switch env"
grep -qx 'cms.example.com {' "$r/etc/caddy/Caddyfile" || fail "retry did not switch Caddyfile"
pass "a failed caddy reload leaves env and Caddyfile as they were; the retry switches"

# --- install.sh set-domain: core pre-check, refreshed on-box script, forwarded arguments
isd="$tmp/asm/install.sh"
floor="$(cat "$here/MIN_CORE_VERSION")"
isd_root() { # isd_root NAME CORE_VERSION_OUTPUT
  local d; d="$(sd_root "$1" "$auto_cf")"
  # shellcheck disable=SC2016 # the single-quoted script expands in the stub, not here
  printf '#!/bin/sh\ncase "$1" in version) echo "%s" ;; *) echo "$PUBLIC_BASE_URL/k7qm-x2rf-9tdc" ;; esac\n' "$2" > "$d/opt/hatamoto/bin/hatamoto"
  echo "$d"
}
isdrun() { local d="$1"; shift; envrun "$d" HATAMOTO_PUBLIC_IP=203.0.113.7 FAKE_DNS_IP=203.0.113.7 sh "$isd" set-domain "$@"; }

r="$(isd_root isdold "hatamoto v0.9.0 (abc)")"; cp "$r/etc/caddy/Caddyfile" "$tmp/cf.before"
echo stale > "$r/usr/local/lib/hatamoto/firstboot.sh"
if isdrun "$r" cms.example.com >"$tmp/o" 2>&1; then fail "core below the floor accepted"; fi
grep -q "older than $floor" "$tmp/o" || fail "old core message: $(cat "$tmp/o")"
grep -q 'update Hatamoto first' "$tmp/o" || fail "old core hint"
cmp -s "$r/etc/caddy/Caddyfile" "$tmp/cf.before" || fail "Caddyfile changed for an old core"
[ "$(cat "$r/usr/local/lib/hatamoto/firstboot.sh")" = stale ] || fail "on-box script refreshed for an old core"

r="$(isd_root isdok "hatamoto $floor (abc)")"; : > "$tmp/calls"
echo stale > "$r/usr/local/lib/hatamoto/firstboot.sh"
isdrun "$r" Cms.Example.com --email you@example.com >"$tmp/o" 2>&1 || fail "install.sh set-domain failed: $(cat "$tmp/o")"
cmp -s "$r/usr/local/lib/hatamoto/firstboot.sh" "$fb" || fail "on-box firstboot.sh not refreshed"
grep -qx 'PUBLIC_BASE_URL=https://cms.example.com' "$r/opt/hatamoto/config/hatamoto.env" || fail "arguments not forwarded"
grep -qxP '\temail you@example.com' "$r/etc/caddy/Caddyfile" || fail "--email not forwarded"

r="$(isd_root isdunk "dev build")"
isdrun "$r" cms.example.com >"$tmp/o" 2>&1 || fail "unparseable core version refused: $(cat "$tmp/o")"
r="$(isd_root isdnone "hatamoto $floor")"; rm -f "$r/opt/hatamoto/bin/hatamoto"
if isdrun "$r" cms.example.com >/dev/null 2>&1; then fail "set-domain without a binary accepted"; fi
if isdrun "$r" >/dev/null 2>&1; then fail "set-domain without a host accepted"; fi
pass "install.sh set-domain refuses an old core, refreshes the on-box scripts and forwards arguments"

echo "ok: test-motd-dns"
