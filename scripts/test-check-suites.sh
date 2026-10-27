#!/usr/bin/env bash
# test-check-suites.sh - offline test of scripts/check-suites.sh and check-suites.txt.
# Run by `make check` (itself a suite in check-suites.txt).
set -euo pipefail
top=$(git rev-parse --show-toplevel)
cd "$top"
fail() { echo "FAIL: $*" >&2; exit 1; }
txt=scripts/check-suites.txt

# --- completeness: every command `make check` ran before it became data is still a suite ---
expected=(
  'bash scripts/check-neutral.sh'
  'cmp STABILITY.md backend/pkg/sdk/STABILITY.md'
  'bash scripts/test-public-snapshot.sh'
  'bash scripts/test-check-neutral-private.sh'
  'bash scripts/test-check-neutral.sh'
  'bash scripts/test-check-dco.sh'
  'bash scripts/test-hooks.sh'
  'bash scripts/check-migrations.sh'
  'bash scripts/test-check-migrations.sh'
  'bash scripts/test-version.sh'
  'bash scripts/test-publish-release.sh'
  'bash scripts/test-publish-registry.sh'
  'bash scripts/test-publish-sdk.sh'
  'bash scripts/test-tag-release.sh'
  'bash scripts/test-check-channel-freshness.sh'
  'bash scripts/test-third-party-licenses.sh'
  'bash scripts/test-channels-private.sh'
  'bash scripts/test-staging-update-check.sh'
  'bash deploy/test-apply-caddy.sh'
  'bash deploy/test-caddy-headers.sh'
  'bash deploy/dns/test-check.sh'
  'make vendor-verify'
  'make vuln'
  'cd backend && go run ./tools/licenses'
  'cd backend && go run ./tools/licenses -check-notice ../NOTICE'
  'make docs-publish-test'
  'cd backend/tools/docs-publish && go run . --lint --root ../../../docs/site'
  'make budgets'
  'bash scripts/test-check-suites.sh'
  'bash scripts/test-affected.sh'
  'bash scripts/test-farm.sh'
)
all=$(grep -vE '^(#|$)' "$txt" | sed -E 's/^[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+//')
for c in "${expected[@]}"; do
  # test-affected / test-farm are registered by the todos that add them
  case $c in *test-affected.sh|*test-farm.sh) [[ -f ${c#bash } ]] || continue ;; esac
  grep -qxF "$c" <<<"$all" || fail "suite missing from $txt: $c"
done

# --- every suite command points at something that exists ---
while IFS= read -r c; do
  if [[ $c =~ ^bash\ ([^[:space:]]+) ]]; then
    [[ -f ${BASH_REMATCH[1]} ]] || fail "suite script missing: ${BASH_REMATCH[1]}"
  elif [[ $c =~ ^make\ ([^[:space:]]+)$ ]]; then
    grep -qE "^${BASH_REMATCH[1]}:" Makefile || fail "make target missing: ${BASH_REMATCH[1]}"
  fi
done <<<"$all"
# ids are unique
dups=$(grep -vE '^(#|$)' "$txt" | awk '{print $1}' | sort | uniq -d)
[[ -z $dups ]] || fail "duplicate suite ids: $dups"

# --- the Makefile and the hook snapshot are wired to the runner ---
mk=$(make -n check 2>/dev/null || true)
grep -q 'scripts/check-suites.sh' <<<"$mk" || fail "make check does not call check-suites.sh"
grep -q 'TestUILint' <<<"$mk" || fail "make check lost ui-lint"
grep -q 'scripts/check-suites.sh' <(sed -n '/^hooks:/,/^hooks-off:/p' Makefile) || fail "make hooks does not snapshot check-suites.sh"
grep -q 'scripts/check-suites.txt' <(sed -n '/^hooks:/,/^hooks-off:/p' Makefile) || fail "make hooks does not snapshot check-suites.txt"

# --- selection: --changed picks `*` suites plus path matches only ---
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
g() { git -C "$T" -c commit.gpgsign=false -c core.hooksPath=/dev/null -c user.name=t -c user.email=t@example.com "$@"; }
g init -q -b main
cat >"$T/suites.txt" <<'S'
# test suites
always   *                 true
docs     docs/**           true
sdk      a/go.mod,a/go.sum true
deep     x/y/*.txt         true
S
mkdir -p "$T/docs/sub" "$T/a" "$T/x/y" "$T/scripts"
touch "$T/base" "$T/docs/sub/p.md" "$T/a/go.sum" "$T/x/y/f.txt" "$T/scripts/check-suites.txt" "$T/Makefile"
g add -A; g commit -q -m base
sel() { (cd "$T" && HATAMOTO_CHECK_SUITES=$T/suites.txt bash "$top/scripts/check-suites.sh" --list "$@" | tr '\n' ' '); }
step() { echo x >>"$T/$1"; g add -A; g commit -q -m "touch $1"; }
[[ $(sel) == 'always docs sdk deep ' ]] || fail "no --changed must list every suite: $(sel)"
step base;           [[ $(sel --changed HEAD~1..HEAD) == 'always ' ]] || fail "unrelated change: $(sel --changed HEAD~1..HEAD)"
step docs/sub/p.md;  [[ $(sel --changed HEAD~1..HEAD) == 'always docs ' ]] || fail "nested ** match: $(sel --changed HEAD~1..HEAD)"
step a/go.sum;       [[ $(sel --changed HEAD~1..HEAD) == 'always sdk ' ]] || fail "exact path match: $(sel --changed HEAD~1..HEAD)"
step x/y/f.txt;      [[ $(sel --changed HEAD~1..HEAD) == 'always deep ' ]] || fail "star match: $(sel --changed HEAD~1..HEAD)"
[[ $(sel --changed HEAD~4..HEAD) == 'always docs sdk deep ' ]] || fail "a multi-commit range unions: $(sel --changed HEAD~4..HEAD)"
step Makefile;       [[ $(sel --changed HEAD~1..HEAD) == 'always docs sdk deep ' ]] || fail "a Makefile change must run everything"
step scripts/check-suites.txt; [[ $(sel --changed HEAD~1..HEAD) == 'always docs sdk deep ' ]] || fail "a suite-file change must run everything"
(sel --changed nosuchref..HEAD >/dev/null 2>&1) && fail "a bad range must fail, not select nothing"

# --- running: stop at the first failure, name it ---
printf 'one   *   echo ran-one\ntwo   *   false\nthree *   echo ran-three\n' >"$T/run.txt"
out=$(cd "$T" && HATAMOTO_CHECK_SUITES=$T/run.txt bash "$top/scripts/check-suites.sh" 2>&1) && fail "a failing suite passed"
grep -q 'ran-one' <<<"$out" || fail "the first suite did not run"
grep -q 'FAIL two' <<<"$out" || fail "the failing suite is not named: $out"
grep -q 'ran-three' <<<"$out" && fail "ran past the first failure"
out=$(cd "$T" && HATAMOTO_CHECK_SUITES=$T/suites.txt bash "$top/scripts/check-suites.sh" --changed HEAD~6..HEAD~5 2>&1)
grep -q 'skip docs' <<<"$out" || fail "skipped suites are not reported: $out"
echo "test-check-suites: ok"
