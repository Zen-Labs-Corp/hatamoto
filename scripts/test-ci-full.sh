#!/usr/bin/env bash
# test-ci-full.sh - offline test of scripts/ci-full-plan.sh and ci-full-report.sh
# (a registered check suite). gh is a stub on PATH that logs its argv.
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
PLAN="$ROOT/scripts/ci-full-plan.sh"
REPORT="$ROOT/scripts/ci-full-report.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
command -v jq >/dev/null || fail "jq is required"

mkdir "$T/bin"
cat > "$T/bin/gh" <<'GH'
#!/usr/bin/env bash
echo "$*" >> "$GH_LOG"
case "$1 $2" in
  "run list") cat "$GH_RUNS" ;;
  "issue list") cat "$GH_ISSUES" ;;
esac
GH
chmod +x "$T/bin/gh"
export GH_LOG="$T/gh.log" GH_RUNS="$T/runs.json" GH_ISSUES="$T/issues.json"

R="$T/repo"
git init -q -b main "$R"
g() { git -C "$R" -c commit.gpgsign=false -c core.hooksPath=/dev/null -c user.name=T -c user.email=t@example.com "$@"; }
g commit -q --allow-empty -m one; old=$(g rev-parse HEAD)
g commit -q --allow-empty -m two; head=$(g rev-parse HEAD)

plan() { # plan <event> <input sha>; prints the outputs file
  : > "$T/out"
  (cd "$R" && env PATH="$T/bin:$PATH" GITHUB_OUTPUT="$T/out" EVENT_NAME="$1" INPUT_SHA="$2" GH_TOKEN=x bash "$PLAN" >/dev/null) || return
  cat "$T/out"
}
want() { grep -qx "$2" <<<"$1" || fail "$3: '$2' not in: $1"; }
runs() { echo "[{\"displayTitle\":\"ci-full @ $1\",\"headSha\":\"$2\"}]" > "$GH_RUNS"; }

runs "$head" "$old"
o=$(plan schedule ""); want "$o" run=false "schedule, newest green tested the head (title)"
runs "$old" "$head"
o=$(plan schedule ""); want "$o" run=true "schedule, newest green is older (title wins over headSha)"
echo '[{"displayTitle":"ci","headSha":"'"$head"'"}]' > "$GH_RUNS"
o=$(plan schedule ""); want "$o" run=false "schedule, no sha in title: headSha is used"
echo '[]' > "$GH_RUNS"
o=$(plan schedule ""); want "$o" run=true "schedule, no green runs"
want "$o" "sha=$head" "schedule tests the head"; want "$o" is_main=true "schedule is main"
runs "$head" "$head"
o=$(plan workflow_dispatch ""); want "$o" run=true "dispatch never skips"
o=$(plan workflow_dispatch "$old"); want "$o" "sha=$old" "dispatch with a sha"
want "$o" is_main=false "an older sha is not main"; want "$o" run=true "dispatch with a sha runs"
plan workflow_dispatch "not-a-sha" >/dev/null 2>&1 && fail "a malformed sha was accepted"
plan workflow_dispatch "1111111111111111111111111111111111111111" >/dev/null 2>&1 && fail "an unknown commit was accepted"

# Main moved on while the run was queued (or the dispatch names another ref): the tested
# sha is the checked-out HEAD, the one the run name shows; origin/main only sets is_main.
# an older green run (a dispatch) does not hide a later green run on the head
echo "[{\"displayTitle\":\"ci-full @ $old\",\"headSha\":\"$old\"},{\"displayTitle\":\"ci-full @ $head\",\"headSha\":\"$head\"}]" > "$GH_RUNS"
o=$(plan schedule ""); want "$o" run=false "nightly skips when any recent green run tested the head"
g commit -q --allow-empty -m three; newer=$(g rev-parse HEAD)
g update-ref refs/remotes/origin/main "$newer"; g checkout -q --detach "$head"
runs "$old" "$old"
o=$(plan workflow_dispatch ""); want "$o" "sha=$head" "dispatch tests the checked-out commit, not origin/main"
want "$o" is_main=false "a checked-out commit behind origin/main is not main"
o=$(plan schedule ""); want "$o" "sha=$head" "a queued nightly tests the checked-out commit"
runs "$head" "$head"
o=$(plan schedule ""); want "$o" run=false "nightly skip compares the tested commit"
g checkout -q --detach "$newer"
o=$(plan workflow_dispatch ""); want "$o" "sha=$newer" "dispatch on main head"; want "$o" is_main=true "main head is main"

# A queued dispatch must not be cancelled by another pending run: the concurrency group
# is keyed on the sha or the run id, never one constant.
grep -Eq '^  group: ci-full-.*(inputs\.sha|github\.run_id)' "$ROOT/.github/workflows/ci-full.yml" ||
  fail "the ci-full concurrency group must be keyed per sha or run"

# report <result> <is_main> <issues json>; leaves the gh calls in $GH_LOG
report() {
  : > "$GH_LOG"; echo "$3" > "$GH_ISSUES"
  env PATH="$T/bin:$PATH" RESULT="$1" IS_MAIN="$2" SHA="$head" RUN_URL=https://example.invalid/run GH_TOKEN=x bash "$REPORT" >/dev/null
}
calls() { grep -c "^issue $1" "$GH_LOG" || true; }
open='[{"number":7,"title":"ci-full failing on main"},{"number":8,"title":"other"}]'

report failure true '[]'
[ "$(calls create)" = 1 ] && [ "$(calls comment)" = 0 ] || fail "failure without an issue must create exactly one"
grep -q 'issue create --title ci-full failing on main' "$GH_LOG" || fail "issue title"
report failure true "$open"
[ "$(calls create)" = 0 ] && [ "$(calls comment)" = 1 ] || fail "failure with an open issue must only comment"
grep -q '^issue comment 7 ' "$GH_LOG" || fail "the comment must go to the matching issue"
report success true "$open"
[ "$(calls comment)" = 1 ] && [ "$(calls close)" = 1 ] || fail "success with an open issue must comment and close"
grep -q '^issue close 7' "$GH_LOG" || fail "the matching issue must be closed"
report success true '[{"number":8,"title":"other"}]'
[ "$(calls comment)" = 0 ] && [ "$(calls close)" = 0 ] || fail "success without our issue must not touch others"
grep -q -e '--search in:title "ci-full failing on main"' "$GH_LOG" || fail "the issue lookup must search by title"
report failure false "$open"
! grep -q '^issue' "$GH_LOG" || fail "a non-main run must not touch issues"
report cancelled true "$open"
! grep -q '^issue' "$GH_LOG" || fail "a cancelled run must not touch issues"
report skipped true "$open"
! grep -q '^issue' "$GH_LOG" || fail "a skipped run must not touch issues"
echo "ci-full tests ok"
