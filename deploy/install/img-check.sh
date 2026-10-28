#!/bin/bash
# img-check.sh - is this machine (or a fixture tree) clean enough to snapshot?
#
# Modelled on the image check of DigitalOcean's marketplace-partners repository,
# plus the Hatamoto items: no env file, secret, install id, setup code or
# database password may exist. Run it after `install.sh build --seal`.
#
#   img-check.sh [ROOT]      ROOT defaults to /; prints [PASS]/[WARN]/[FAIL] lines, exit 1 on any FAIL
#
# Environment (test seams):
#   IMG_CHECK_DENY       space-separated secret values that must appear in no file
#   IMG_CHECK_FIREWALL   1 (default) require ufw enabled, 0 skip that item
set -uo pipefail

ROOT="${1:-/}"
ROOT="${ROOT%/}"
APP="$ROOT/opt/hatamoto"
fails=0

pass() { printf '[PASS] %s\n' "$1"; }
warn() { printf '[WARN] %s\n' "$1"; }
bad() { printf '[FAIL] %s: %s\n' "$1" "$2"; fails=$((fails + 1)); }

# check NAME FILES...: pass when the list is empty, else fail naming the files.
verdict() {
  local name="$1"; shift
  if [ $# -eq 0 ]; then pass "$name"; else bad "$name" "$*"; fi
}

# homes lists the directories of per-user files.
homes() {
  echo "$ROOT/root"
  local d
  for d in "$ROOT"/home/*; do [ -d "$d" ] && echo "$d"; done
}

# existing prints each existing path of the arguments (globs that matched nothing are skipped).
existing() { local f; for f in "$@"; do [ -e "$f" ] || [ -L "$f" ] && echo "$f"; done; return 0; }
nonempty() { local f; for f in "$@"; do [ -s "$f" ] && echo "$f"; done; return 0; }

# 1. ssh host keys
mapfile -t found < <(existing "$ROOT"/etc/ssh/ssh_host_*)
verdict ssh-host-keys "${found[@]}"

# 2. authorized keys, private keys, known hosts
found=()
while IFS= read -r h; do
  while IFS= read -r f; do found+=("$f"); done < <(nonempty "$h/.ssh/authorized_keys")
done < <(homes)
verdict authorized-keys "${found[@]}"
found=()
while IFS= read -r h; do
  while IFS= read -r f; do found+=("$f"); done < <(existing "$h"/.ssh/id_* "$h"/.ssh/known_hosts)
done < <(homes)
verdict ssh-private-keys "${found[@]}"

# 3. passwords on root and on regular users (uid >= 1000)
found=()
if [ -f "$ROOT/etc/shadow" ]; then
  users="root"
  if [ -f "$ROOT/etc/passwd" ]; then
    users="$users $(awk -F: '$3 >= 1000 && $3 < 65534 { print $1 }' "$ROOT/etc/passwd")"
  fi
  for u in $users; do
    h="$(awk -F: -v u="$u" '$1 == u { print $2 }' "$ROOT/etc/shadow")"
    case "$h" in
      '!'* | '*'*) ;;
      *) [ -z "$(awk -F: -v u="$u" '$1 == u' "$ROOT/etc/shadow")" ] || found+=("$u") ;;
    esac
  done
fi
verdict password "${found[@]}"

# 4. machine id
mapfile -t found < <(nonempty "$ROOT/etc/machine-id")
if [ -f "$ROOT/var/lib/dbus/machine-id" ] && [ ! -L "$ROOT/var/lib/dbus/machine-id" ]; then
  mapfile -t found2 < <(nonempty "$ROOT/var/lib/dbus/machine-id"); found+=("${found2[@]}")
fi
verdict machine-id "${found[@]}"

# 5. shell history
found=()
while IFS= read -r h; do
  while IFS= read -r f; do found+=("$f"); done < <(nonempty "$h/.bash_history" "$h/.history")
done < <(homes)
verdict history "${found[@]}"

# 6. apt lists and caches
found=()
if [ -d "$ROOT/var/lib/apt/lists" ]; then
  while IFS= read -r f; do found+=("$f"); done < <(find "$ROOT/var/lib/apt/lists" -type f ! -name lock 2>/dev/null)
fi
verdict apt-lists "${found[@]}"
mapfile -t found < <(existing "$ROOT"/var/cache/apt/*.bin "$ROOT"/var/cache/apt/archives/*.deb)
verdict apt-cache "${found[@]}"

# 7. temp directories
found=()
for d in "$ROOT/tmp" "$ROOT/var/tmp"; do
  [ -d "$d" ] || continue
  while IFS= read -r f; do found+=("$f"); done < <(find "$d" -mindepth 1 2>/dev/null | head -n 5)
done
verdict tmp "${found[@]}"

# 8. logs: nothing over 50 bytes, no rotated archives
found=()
rotated=()
if [ -d "$ROOT/var/log" ]; then
  while IFS= read -r f; do found+=("$f"); done < <(find "$ROOT/var/log" -type f -size +50c 2>/dev/null)
  while IFS= read -r f; do rotated+=("$f"); done < <(find "$ROOT/var/log" -type f \( -name '*.gz' -o -name '*.xz' -o -name '*.old' -o -name '*.[0-9]' -o -name '*-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]' \) 2>/dev/null)
fi
verdict logs "${found[@]}"
verdict rotated-logs "${rotated[@]}"

# 9. hatamoto: nothing of an install may exist yet
found=()
while IFS= read -r f; do found+=("$f"); done < <(existing "$APP/config/hatamoto.env" "$APP/data/secrets" "$APP/data/install_id")
if [ -d "$ROOT/var/lib/hatamoto-install" ]; then
  while IFS= read -r f; do found+=("$f"); done < <(find "$ROOT/var/lib/hatamoto-install" -mindepth 1 2>/dev/null)
fi
verdict hatamoto-secrets "${found[@]}"
found=()
if [ -d "$ROOT/var/lib/caddy/.local/share/caddy" ]; then
  while IFS= read -r f; do found+=("$f"); done < <(find "$ROOT/var/lib/caddy/.local/share/caddy" -mindepth 1 2>/dev/null | head -n 5)
fi
if [ -f "$ROOT/etc/caddy/Caddyfile" ] && grep -q '127\.0\.0\.1:8090' "$ROOT/etc/caddy/Caddyfile"; then found+=("$ROOT/etc/caddy/Caddyfile"); fi
verdict caddy-state "${found[@]}"

# 10. cloud-init must run again on a new instance
mapfile -t found < <(existing "$ROOT"/var/lib/cloud/instance "$ROOT"/var/lib/cloud/instances)
verdict cloud-init "${found[@]}"

# 11. firewall
if [ "${IMG_CHECK_FIREWALL:-1}" = 0 ]; then
  warn "firewall not checked (IMG_CHECK_FIREWALL=0)"
elif [ -f "$ROOT/etc/ufw/ufw.conf" ] && grep -qx 'ENABLED=yes' "$ROOT/etc/ufw/ufw.conf"; then
  pass firewall
else
  bad firewall "ufw is not enabled"
fi

# 12. database role of this install (only where Postgres runs)
if [ -z "$ROOT" ] && command -v pgrep >/dev/null 2>&1 && pgrep -x postgres >/dev/null 2>&1; then
  if [ "$(sudo -n -u postgres psql -tAc "select 1 from pg_roles where rolname = 'hatamoto'" 2>/dev/null)" = 1 ]; then
    bad postgres-role "role hatamoto exists"
  else
    pass postgres-role
  fi
else
  warn "postgres role not checked (Postgres is not running here)"
fi

# 13. secret scan: values given in IMG_CHECK_DENY must be in no file
found=()
for v in ${IMG_CHECK_DENY:-}; do
  while IFS= read -r f; do found+=("$f"); done < <(
    grep -rlaF -e "$v" "$ROOT/etc" "$ROOT/opt" "$ROOT/root" "$ROOT/home" "$ROOT/tmp" "$ROOT/var/tmp" "$ROOT/var/log" \
      "$ROOT/var/lib/hatamoto-install" "$ROOT/var/lib/postgresql" 2>/dev/null | head -n 5)
done
verdict secret-scan "${found[@]}"

if [ "$fails" -gt 0 ]; then
  printf '%s check(s) failed\n' "$fails"
  exit 1
fi
echo "image is clean"
