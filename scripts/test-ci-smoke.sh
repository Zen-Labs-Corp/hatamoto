#!/usr/bin/env bash
# test-ci-smoke.sh - the PG18 smoke package list must be one line that
# ci-integration-pkgdb.sh's `read -r -a` parses into every package (a registered check suite).
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
fail() { echo "FAIL: $*" >&2; exit 1; }
list=$(cd "$ROOT/backend" && bash ../scripts/ci-smoke-pkgs.sh)
[ "$(wc -l <<<"$list")" = 1 ] || fail "the list must be a single line"
read -r -a pkgs <<<"$list"
[ "${#pkgs[@]}" -ge 20 ] || fail "only ${#pkgs[@]} packages parsed from: $list"
[ "${#pkgs[@]}" = "$(wc -w <<<"$list")" ] || fail "read -a and wc -w disagree"
case " $list " in *" hatamoto.dev/core/migrations "*) ;; *) fail "migrations is not in the smoke" ;; esac
while read -r ex; do
  case " $list " in *" hatamoto.dev/core/$ex "*) fail "excluded $ex is in the smoke" ;; esac
done <"$ROOT/scripts/ci-smoke-exclude.txt"
echo "ci-smoke tests ok (${#pkgs[@]} packages)"
