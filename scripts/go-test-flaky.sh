#!/usr/bin/env bash
# go-test-flaky.sh "<go test flags>" <package>... - run from the module directory.
# Runs `go test` and exits non-zero on any failure, except for a clean
# `--- FAIL` of a test listed (exact name) in scripts/known-flakes.txt: it is
# re-run alone, and then the REST of its package is re-run with the listed
# tests skipped; both must pass. A package that panicked, hit a fatal error or
# failed without a failing test fails whatever is listed, because tests after
# the crash never ran. A package listed as "<suffix> *" (load-sensitive as a
# whole: wasm deadlines, latency bounds) that fails cleanly is re-run whole,
# once; that run must pass.
#
# GO_TEST_TIMEOUT  per-package `go test -timeout` (default 30m). Go's own default
#         of 10m panics slow-but-passing packages (internal/admin on a loaded
#         machine). A -timeout in the flags argument still wins.
set -uo pipefail
flags="-timeout ${GO_TEST_TIMEOUT:-30m} $1"; shift
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
list=${HATAMOTO_FLAKES_FILE:-$here/known-flakes.txt}
out=$(mktemp "${TMPDIR:-/tmp}/gotest.XXXXXX"); trap 'rm -f "$out"' EXIT

# shellcheck disable=SC2086
go test -count=1 -v $flags "$@" 2>&1 | tee "$out"
rc=${PIPESTATUS[0]}
((rc)) || exit 0

listed() { # pkg test: exact test name, package suffix on a path-element boundary
  local sfx name
  [[ -f $list ]] || return 1
  while read -r sfx name _; do
    [[ -n $sfx && $sfx != '#'* && -n ${name:-} ]] || continue
    [[ $1 == "$sfx" || $1 == */"$sfx" ]] && [[ $2 == "$name" ]] && return 0
  done <"$list"
  return 1
}

# whole_listed pkg: the package is listed as "<suffix> *" (load-sensitive as a whole).
whole_listed() {
  local sfx name
  [[ -f $list ]] || return 1
  while read -r sfx name _; do
    [[ -n $sfx && $sfx != '#'* && ${name:-} == '*' ]] || continue
    [[ $1 == "$sfx" || $1 == */"$sfx" ]] && return 0
  done <"$list"
  return 1
}

# Failing tests are printed before the "FAIL <pkg>" line of their package.
status=0 pending=() crashed=0 skips=() pkgs=() wholes=()
declare -A skip_of=()
while IFS= read -r line; do
  if [[ $line =~ ^---\ FAIL:\ ([^[:space:]/]+)\  ]]; then
    pending+=("${BASH_REMATCH[1]}")
  elif [[ $line =~ ^(panic:|fatal\ error:|\[recovered\]) ]] || [[ $line == *'panic: test timed out'* ]]; then
    crashed=1
  elif [[ $line =~ ^(FAIL|ok)[[:space:]]+([^[:space:]]+) ]]; then
    if [[ ${BASH_REMATCH[1]} == FAIL ]]; then
      pkg=${BASH_REMATCH[2]}
      if ((crashed)); then
        status=1; echo "go-test-flaky: $pkg panicked or crashed: its remaining tests did not run, so no known flake is excused"
      elif ((${#pending[@]} == 0)); then
        status=1; echo "go-test-flaky: $pkg failed without a failing test (build error, TestMain, or tests did not run)"
      else
        all_listed=1
        for t in "${pending[@]}"; do listed "$pkg" "$t" || all_listed=0; done
        if ((all_listed)); then
          pkgs+=("$pkg"); skip_of[$pkg]=$(IFS='|'; echo "${pending[*]}")
        elif whole_listed "$pkg"; then
          wholes+=("$pkg")
        else
          status=1
          for t in "${pending[@]}"; do
            listed "$pkg" "$t" || echo "go-test-flaky: $t ($pkg) is not in the known-flakes list"
          done
        fi
      fi
    fi
    pending=() crashed=0
  fi
done <"$out"
# A failure with no "FAIL <pkg>" line at all (compile error before any package result).
((${#pkgs[@]} || ${#wholes[@]} || status)) || { echo "go-test-flaky: go test failed and no failing test was found"; exit 1; }

for pkg in "${pkgs[@]}"; do
  names=${skip_of[$pkg]}
  echo "go-test-flaky: re-running known flake(s) $names alone ($pkg)"
  # shellcheck disable=SC2086
  if ! go test -count=1 $flags -run "^($names)\$" "$pkg" >/dev/null 2>&1; then
    echo "go-test-flaky: $names failed again alone"; status=1; continue
  fi
  echo "go-test-flaky: re-running the rest of $pkg without $names"
  # shellcheck disable=SC2086
  if go test -count=1 $flags -skip "^($names)\$" "$pkg" >/dev/null 2>&1; then
    echo "go-test-flaky: FLAKY PASS $names (failed in the full run, passed alone, and the rest of the package passes)"
  else
    echo "go-test-flaky: the rest of $pkg fails without $names"; status=1
  fi
done
for pkg in "${wholes[@]}"; do
  echo "go-test-flaky: re-running all of $pkg once (listed as load-sensitive)"
  # shellcheck disable=SC2086
  if go test -count=1 $flags "$pkg" >"$out" 2>&1; then
    echo "go-test-flaky: FLAKY PASS $pkg (failed under load, the whole package passes on a re-run)"
    continue
  fi
  # The re-run may fail only on tests listed by name: those get their usual second chance, alone
  # (the rest of the package just passed in this re-run). Anything else, or a crash, fails.
  again=() bad=0
  while IFS= read -r line; do
    if [[ $line =~ ^---\ FAIL:\ ([^[:space:]/]+)\  ]]; then again+=("${BASH_REMATCH[1]}")
    elif [[ $line =~ ^(panic:|fatal\ error:|\[recovered\]) ]] || [[ $line == *'panic: test timed out'* ]]; then bad=1; fi
  done <"$out"
  ((${#again[@]})) || bad=1
  for t in "${again[@]}"; do listed "$pkg" "$t" || { bad=1; echo "go-test-flaky: $t ($pkg) failed again on the re-run"; }; done
  if ((bad)); then
    echo "go-test-flaky: $pkg fails again on a re-run"; tail -30 "$out"; status=1; continue
  fi
  names=$(IFS='|'; echo "${again[*]}")
  # shellcheck disable=SC2086
  if go test -count=1 $flags -run "^($names)\$" "$pkg" >/dev/null 2>&1; then
    echo "go-test-flaky: FLAKY PASS $pkg (re-run passes but for known flake(s) $names, which pass alone)"
  else
    echo "go-test-flaky: $names failed again alone ($pkg)"; status=1
  fi
done
exit $status
