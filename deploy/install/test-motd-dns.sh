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
  envrun "$1" HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; FB_PENDING_DOMAIN="$2"; FB_IP=203.0.113.7; install_motd; install_dns_switch' _ "$fb" "$2"
}

# --- MOTD
r="$(mkroot motd)"; : > "$tmp/calls"
setup "$r" "" >/dev/null 2>&1 || fail "setup failed"
m="$r/etc/update-motd.d/99-hatamoto"
[ -x "$m" ] && [ "$(stat -c %a "$m")" = 755 ] && [ "$(head -n1 "$m")" = '#!/bin/sh' ] || fail "motd file"
out="$(envrun "$r" FAKE_OWNERS=0 sh "$m")"
grep -q 'Setup code: K7M2-RT9P-QW4X' <<<"$out" || fail "no code: $out"
grep -q '/k7qm-x2rf-9tdc/setup#code=K7M2-RT9P-QW4X' <<<"$out" || fail "no link"
grep -q 'Updates:    automatic (stable channel)' <<<"$out" || fail "no updates line"
if grep -q 'Domain:' <<<"$out"; then fail "domain line without pending"; fi
pass "motd shows code and link while no owner exists"
[ -z "$(envrun "$r" FAKE_OWNERS=1 sh "$m")" ] || fail "motd not silent once the owner exists"
pass "motd silent once the owner exists"
[ ! -e "$r/etc/systemd/system/hatamoto-dns-switch.timer" ] || fail "timer without a pending domain"

# --- pending domain
r="$(mkroot pend)"; : > "$tmp/calls"
setup "$r" example.com >/dev/null 2>&1 || fail "pending setup failed"
out="$(envrun "$r" FAKE_OWNERS=0 sh "$r/etc/update-motd.d/99-hatamoto")"
grep -q 'Domain:     example.com (waiting for DNS -> 203.0.113.7;' <<<"$out" || fail "pending line: $out"
t="$r/etc/systemd/system/hatamoto-dns-switch"
grep -q 'OnUnitActiveSec=5min' "$t.timer" && grep -q 'Type=oneshot' "$t.service" || fail "timer or service text"
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
grep -qx 'systemctl restart hatamoto-api' "$tmp/calls" && grep -qx 'systemctl reload caddy' "$tmp/calls" || fail "services not cycled"
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
[ ! -e "$r/etc/systemd/system/hatamoto-dns-switch.timer" ] || fail "timer for bad domain"
pass "invalid domain rejected"

# --- resolved domain removes a stale timer
r="$(mkroot stale)"; setup "$r" example.com >/dev/null 2>&1; setup "$r" "" >/dev/null 2>&1
[ ! -e "$r/etc/systemd/system/hatamoto-dns-switch.timer" ] && [ ! -e "$r/var/lib/hatamoto-install/dns-pending" ] || fail "stale timer kept"
pass "no pending domain removes the timer"

echo "ok: test-motd-dns"
