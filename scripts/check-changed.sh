#!/usr/bin/env bash
# check-changed.sh - run the guards that apply to a set of changed files:
#   * scripts/check-neutral.sh (also in `make check`): whole-tree and fast, so it
#     runs whenever a code file is touched;
#   * `make design-check` (also in `make check`, ship.sh and the pipeline gates):
#     every static admin design check, in seconds, whenever an admin UI file, the
#     component registry, the design rules or a check itself changes.
#
# Usage:
#     scripts/check-changed.sh <path> [<path> ...]   # exit 1 on violation
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT" || exit 0

paths=("$@")
[ ${#paths[@]} -eq 0 ] && exit 0

want_code=0
want_design=0
for p in "${paths[@]}"; do
  case "$p" in
    "$REPO_ROOT"/*) rel="${p#"$REPO_ROOT"/}" ;;
    /*)             continue ;;
    *)              rel="$p" ;;
  esac
  case "$rel" in
    backend/web/admin/*|backend/internal/admin/*|docs/ADMIN_UI_RULES.md) want_design=1 ;;
  esac
  case "$rel" in
    plans/*|docs/*|*.md) continue ;;
    *.go|*.js|*.css|*.sql) want_code=1 ;;
  esac
done

rc=0
if [ "$want_code" -eq 1 ] && ! out=$(bash "$REPO_ROOT/scripts/check-neutral.sh" 2>&1); then
  printf '%s\n' "$out" >&2
  rc=1
fi
if [ "$want_design" -eq 1 ] && ! out=$(make -s -C "$REPO_ROOT" design-check 2>&1); then
  printf '%s\n' "$out" >&2
  rc=1
fi
exit "$rc"
