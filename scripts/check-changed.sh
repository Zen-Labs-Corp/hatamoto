#!/usr/bin/env bash
# check-changed.sh - run the guards that apply to a set of changed files. The
# guard is scripts/check-neutral.sh (also wired into `make check`): whole-tree
# and fast, so it runs whenever a code file is touched.
#
# Usage:
#     scripts/check-changed.sh <path> [<path> ...]   # exit 1 on violation
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT" || exit 0

paths=("$@")
[ ${#paths[@]} -eq 0 ] && exit 0

want_code=0
for p in "${paths[@]}"; do
  case "$p" in
    "$REPO_ROOT"/*) rel="${p#"$REPO_ROOT"/}" ;;
    /*)             continue ;;
    *)              rel="$p" ;;
  esac
  case "$rel" in
    plans/*|docs/*|*.md) continue ;;
    *.go|*.js|*.css|*.sql) want_code=1 ;;
  esac
done

[ "$want_code" -eq 0 ] && exit 0

if ! out=$(bash "$REPO_ROOT/scripts/check-neutral.sh" 2>&1); then
  printf '%s\n' "$out" >&2
  exit 1
fi
exit 0
