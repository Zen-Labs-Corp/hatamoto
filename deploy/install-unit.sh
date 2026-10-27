#!/bin/sh
# Runs ON THE BOX (fed to `sh -s` over ssh by `make install-unit` / `make check-unit`).
# Installs the uploaded $UNIT.new over $UNIT, keeping the User= and Group= already
# configured there (the tracked file only names defaults for a fresh install).
#
# Refuses (exit 1, nothing changed) when the unit on the box carries settings the
# tracked unit would silently drop: drop-in files, or Environment= lines the
# tracked unit lacks. Set FORCE_UNIT=1 to install anyway. The diff is always printed.
# CHECK_ONLY=1 stops after the checks and never touches the unit.
set -eu
: "${UNIT:?}"
new=$UNIT.new
trap 'rm -f "$new"' EXIT

u=$(systemctl show -p User --value hatamoto-api 2>/dev/null || true)
g=$(systemctl show -p Group --value hatamoto-api 2>/dev/null || true)
for kv in "User=$u" "Group=$g"; do
  k=${kv%%=*}; v=${kv#*=}
  case "$v" in ''|*[!a-zA-Z0-9_.-]*) ;; *) sed -i "s/^$k=.*/$k=$v/" "$new" ;; esac
done
# a unit without Group= in the tracked file but with one on the box: add it
if [ -n "$g" ] && ! grep -q '^Group=' "$new"; then
  case "$g" in *[!a-zA-Z0-9_.-]*) ;; *) sed -i "/^User=/a Group=$g" "$new" ;; esac
fi

cur=$(systemctl show -p FragmentPath --value hatamoto-api 2>/dev/null || true)
[ -n "$cur" ] || cur=$UNIT
echo "-- unit file differences, box ($cur) vs tracked (after keeping User/Group)"
if [ -f "$cur" ]; then diff -u "$cur" "$new" || true; else echo "(no unit on the box yet)"; fi

problems=""
dropins=$(systemctl show -p DropInPaths --value hatamoto-api 2>/dev/null || true)
[ -z "$dropins" ] || problems="$problems
  drop-ins that stay active: $dropins"
if [ -f "$cur" ]; then
  lost=$(grep '^Environment=' "$cur" | while IFS= read -r l; do grep -qxF "$l" "$new" || echo "  $l"; done || true)
  [ -z "$lost" ] || problems="$problems
  Environment= lines the tracked unit lacks:
$lost"
fi
if [ -n "$problems" ] && [ "${FORCE_UNIT:-}" != 1 ]; then
  echo "REFUSING to install the unit; it would change settings the repo does not know:$problems" >&2
  echo "Fold them into deploy/systemd/hatamoto-api.service (or the env file), or rerun with FORCE_UNIT=1." >&2
  exit 1
fi
[ "${CHECK_ONLY:-}" != 1 ] || exit 0

if cmp -s "$new" "$UNIT"; then :; else mv -f "$new" "$UNIT"; fi
systemctl daemon-reload
