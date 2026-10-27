#!/usr/bin/env bash
# check-suites.sh - run the `make check` suites listed in scripts/check-suites.txt.
#   check-suites.sh                    every suite, in order, stop at the first failure
#   check-suites.sh --changed <range>  the `*` suites plus those whose globs match
#                                      `git diff --name-only <range>`; all of them when
#                                      check-suites.txt, this script or the Makefile changed
#   check-suites.sh --list [--changed <range>]   print the selected suite ids, run nothing
# Runs from the git toplevel of the current directory. HATAMOTO_CHECK_SUITES overrides
# the suite file (tests). The pre-push hook calls it with --changed; `make check` without.
set -euo pipefail

changed_range=
list=0
while (($#)); do
  case $1 in
    --changed) changed_range=${2:?--changed needs a range}; shift 2 ;;
    --list) list=1; shift ;;
    *) echo "usage: check-suites.sh [--changed <range>] [--list]" >&2; exit 2 ;;
  esac
done

root=$(git rev-parse --show-toplevel)
cd "$root"
file=${HATAMOTO_CHECK_SUITES:-scripts/check-suites.txt}
[[ -f $file ]] || { echo "check-suites: $file not found" >&2; exit 2; }

changed=
run_all=1
if [[ -n $changed_range ]]; then
  changed=$(git diff --name-only "$changed_range" --) || { echo "check-suites: cannot diff $changed_range" >&2; exit 2; }
  run_all=0
  grep -qxE 'scripts/check-suites\.(sh|txt)|Makefile' <<<"$changed" && run_all=1
fi

matches() { # <comma-separated globs>: does any changed path match?
  local g f
  local -a globs
  IFS=, read -ra globs <<<"$1"
  for g in "${globs[@]}"; do
    g=${g//\*\*/*}
    while IFS= read -r f; do
      [[ -n $f ]] || continue
      # shellcheck disable=SC2254 # the glob is the point
      case $f in $g) return 0 ;; esac
    done <<<"$changed"
  done
  return 1
}

ids=() cmds=()
while IFS= read -r line; do
  [[ -z $line || $line == \#* ]] && continue
  [[ $line =~ ^([^[:space:]]+)[[:space:]]+([^[:space:]]+)[[:space:]]+(.+)$ ]] \
    || { echo "check-suites: bad line: $line" >&2; exit 2; }
  id=${BASH_REMATCH[1]} globs=${BASH_REMATCH[2]} cmd=${BASH_REMATCH[3]}
  if ((run_all)) || [[ $globs == '*' ]] || matches "$globs"; then
    ids+=("$id"); cmds+=("$cmd")
  else
    ((list)) || echo "check-suites: skip $id (no matching path changed)"
  fi
done <"$file"

if ((list)); then
  ((${#ids[@]} == 0)) || printf '%s\n' "${ids[@]}"
  exit 0
fi
for i in "${!ids[@]}"; do
  echo "check-suites: ${ids[i]}: ${cmds[i]}"
  bash -c "${cmds[i]}" || { echo "check-suites: FAIL ${ids[i]}" >&2; exit 1; }
done
echo "check-suites: ${#ids[@]} suites passed"
