#!/usr/bin/env bash
# ship.sh - SHIP-H: merge a PR and deploy it to hatamoto.dev, with health
# checks and automatic rollback.
#
# Usage:
#     scripts/ship.sh [--dry-run] [--no-deploy] [--all] <PR>
#
#   --dry-run    print every step; mutating commands are shown as `DRY: <cmd>`
#                and never executed (read-only gh queries still run)
#   --no-deploy  merge only (skips migrations, deploy, health)
#   --all        force migrations (idempotent) + deploy
#
# Env (all optional):
#   DEPLOY_HOST               ssh host alias; if unset, read from the untracked
#                             file .deploy-host (this checkout, else the main
#                             checkout). Required; ship stops without it.
#   REMOTE_DIR                default /opt/hatamoto
#   SHIP_DB                   default hatamoto_core; must equal the database in
#                             config/hatamoto.env's DATABASE_URL (checked in
#                             preflight and again by the restore helper)
#   SHIP_CI_WAIT              seconds to wait for the PR's CI checks to finish
#                             (CI mode); default 3600, 0 = do not wait
#   SHIP_CI_POLL              seconds between check polls; default 30
#   SHIP_REEXEC=1             skip the re-exec into the deploy-SHA copy of this
#                             script (to dry-run a local ship.sh change)
#   SITE_URL                  default https://hatamoto.dev
#   SHIP_EXPECTED_ORIGIN      space-separated OWNER/NAME list the clone's origin must
#                             be (case-insensitive); default
#                             "Zen-Labs-Corp/hatamoto". Both the
#                             origin URL and the repository gh resolves it to must be
#                             in the list, so a clone of a renamed repository (GitHub
#                             redirects the old name) is refused. Tests set it.
#
# Merge gates (before `gh pr merge`, for an open PR, with or without --no-deploy):
# - CI mode (the 'ci' workflow is active): ship waits for the PR head's checks (SHIP_CI_WAIT),
#   stops at the first failed one, and merges only when every check is green, `backend` (the
#   whole Go suite with -tags=integration, vet) and `admin-ui` included. Those checks ARE the
#   full suite and the UI gate, run on GitHub's runners, so nothing is re-run here.
# - Local mode (the 'ci' workflow is disabled): the local UI gate, then the FULL suite (every
#   package and check suite) on the PR head, once per tree. It runs the installed pre-push
#   snapshot with HATAMOTO_PREPUSH_FULL=1 and reuses a `full-suite` pass recorded for the same
#   tree (`scripts/pipeline-gate.sh full`, an earlier ship). A stale snapshot aborts with "run
#   make hooks".
# There is no skip knob. `git push` itself only runs the affected gates.
#
# Bootstrap: ship.sh re-executes the copy at origin/main, but only AFTER the merge, so the merge
# gates above are those of the invoking checkout's copy. `git pull` (and `make hooks`) before
# shipping, and ship the first PR after a change to this logic from a checkout that has it.
#
# Deploys are built from a throwaway worktree at DEPLOY_SHA (= origin/main
# after fetching it); the invoking checkout's working tree is never touched
# and no repo file is read from it (the invoking checkout may be behind): every
# file ship.sh sends or runs comes from $TREE. Only .deploy-host is read from
# the invoking checkout, as it is untracked.
# Other stacks on the box are never touched. Env values (DATABASE_URL etc.) are never printed.
#
# Deploy (one binary serves the site, the admin and the API): the new `hatamoto` binary is uploaded next to the
# live one, `hatamoto migrate` runs with it (env file sourced) BEFORE the restart,
# then deploy/systemd/hatamoto-api.service is installed (daemon-reload) and the
# service restarted. Pre-ship copies of the `hatamoto` and legacy `hatamoto-api`
# binaries and of the unit file make rollback work from any prior box state,
# including the first deploy from the old hatamoto-api binary + old unit.
#
# Exit codes: 0 ok, 2 failed after the backup but rolled back,
#             3 rollback incomplete (database NOT restored, or unhealthy afterwards),
#             1 precondition failure, 64 usage.

set -Eeuo pipefail

DRY=0; NO_DEPLOY=0; ALL=0; PR=""
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    --no-deploy) NO_DEPLOY=1 ;;
    --all) ALL=1 ;;
    -h|--help) sed -n '2,41p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $a" >&2; exit 64 ;;
    *) PR="$a" ;;
  esac
done
[[ "$PR" =~ ^[0-9]+$ ]] || { sed -n '2,10p' "$0"; exit 64; }

REPO=$(git rev-parse --show-toplevel)
cd "$REPO"

# Ship only from a clone of the core repository: if the repository a clone points
# at is ever renamed, GitHub redirects the old name, so a stale clone would still
# merge there. Read-only, so it runs in --dry-run too.
EXPECTED_ORIGIN=${SHIP_EXPECTED_ORIGIN:-Zen-Labs-Corp/hatamoto}
origin_slug() { # URL -> OWNER/NAME (ssh, https or a local path)
  local u=${1%/}; u=${u%.git}; u=${u//:/\/}
  awk -F/ 'NF >= 2 {print $(NF-1) "/" $NF}' <<< "$u"
}
expected_origin() { # OWNER/NAME -> 0 when it is in EXPECTED_ORIGIN
  local e
  for e in $EXPECTED_ORIGIN; do [[ "${1,,}" == "${e,,}" ]] && return 0; done
  return 1
}
ORIGIN_URL=$(git remote get-url origin 2>/dev/null) || { echo "SHIP-H ABORT: this clone has no origin remote" >&2; exit 1; }
ORIGIN_SLUG=$(origin_slug "$ORIGIN_URL")
expected_origin "$ORIGIN_SLUG" \
  || { echo "SHIP-H ABORT: origin is $ORIGIN_SLUG ($ORIGIN_URL), not one of: $EXPECTED_ORIGIN (ship from the core clone; SHIP_EXPECTED_ORIGIN overrides)" >&2; exit 1; }
RESOLVED_SLUG=$(gh repo view "$ORIGIN_SLUG" --json nameWithOwner --jq .nameWithOwner 2>/dev/null) \
  || { echo "SHIP-H ABORT: gh cannot resolve the origin repository $ORIGIN_SLUG" >&2; exit 1; }
expected_origin "$RESOLVED_SLUG" \
  || { echo "SHIP-H ABORT: origin $ORIGIN_SLUG resolves to $RESOLVED_SLUG on GitHub (renamed?), not one of: $EXPECTED_ORIGIN" >&2; exit 1; }
# Every later gh call goes to the repository checked here (not a gh default or an upstream remote).
export GH_REPO=$RESOLVED_SLUG
# The deploy host alias is never committed: env var, else .deploy-host in this
# checkout, else in the main checkout (when run from a linked worktree).
if [[ -z "${DEPLOY_HOST:-}" ]]; then
  MAIN_CHECKOUT=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
  for f in "$REPO/.deploy-host" "$MAIN_CHECKOUT/.deploy-host"; do
    if [[ -r "$f" ]]; then DEPLOY_HOST=$(tr -d '[:space:]' < "$f"); break; fi
  done
fi
DEPLOY_HOST=${DEPLOY_HOST:-}
REMOTE_DIR=${REMOTE_DIR:-/opt/hatamoto}
DB=${SHIP_DB:-hatamoto_core}
SITE_URL=${SITE_URL:-https://hatamoto.dev}
[[ -n "$DEPLOY_HOST" ]] || { echo "SHIP-H: set DEPLOY_HOST (ssh host alias) or write it to .deploy-host" >&2; exit 1; }
# The Makefile is always the deploy tree's, and gets the host explicitly: the
# worktree has no untracked .deploy-host, so it must never fall back to it.
MAKE_VARS=(DEPLOY_HOST="$DEPLOY_HOST" REMOTE_DIR="$REMOTE_DIR")
tree_make() { env -u MAKEFLAGS -u MFLAGS make -C "$TREE" "${MAKE_VARS[@]}" "$@"; }

step() { echo; echo "== step $1: $2"; }
# run: execute, or print in dry-run. Arguments are the command and its args.
run() {
  if (( DRY )); then echo "DRY: $*"; else "$@"; fi
}

MIG=0; API=0
DEPLOYED_API=0; MIG_RAN=0; GOOSE_PRE=""
UNIT=/etc/systemd/system/hatamoto-api.service
TS=$(date +%Y%m%d-%H%M%S)
DUMP="$REMOTE_DIR/backups/hcore-$TS.dump"
TMP=""; GATE_TREE=""; FS_PID=""; FS_LOG=""

cleanup() {
  [[ -z "${SHIP_SELF_TMP:-}" ]] || rm -f "$SHIP_SELF_TMP"
  # A background full suite (see step 1) still running means the UI gate or an abort ended the
  # ship: stop its whole process group (the pre-push hook cancels its gates on TERM).
  if [[ -n "${FS_PID:-}" ]] && kill -0 "$FS_PID" 2>/dev/null; then
    kill -TERM -- -"$FS_PID" 2>/dev/null || kill -TERM "$FS_PID" 2>/dev/null || true
    wait "$FS_PID" 2>/dev/null || true
    [[ -z "${FS_LOG:-}" ]] || { echo "full suite stopped; its log: $FS_LOG" >&2; }
  fi
  ! declare -F hm_farm_release >/dev/null || hm_farm_release
  if [[ -n "$GATE_TREE" && -d "$GATE_TREE" ]]; then
    git -C "$REPO" worktree remove --force "$GATE_TREE" >/dev/null 2>&1 || true
    rm -rf "$(dirname "$GATE_TREE")"
  fi
  if [[ -n "$TMP" && -d "$TMP" ]]; then
    git -C "$REPO" worktree remove --force "$TMP" >/dev/null 2>&1 || true
    rm -rf "$(dirname "$TMP")"
  fi
}
trap cleanup EXIT

# local_ui_gate <sha>: the UI checks (static lint, then the Chrome consistency run
# incl. its self-test, strict) on the PR head commit in a throwaway worktree.
# When 'ci' is disabled this is the only UI gate, so missing Postgres or Chrome
# abort instead of skipping. Uses the discovery in scripts/admin-qa/qa-env.sh.
# It is not run (minutes of Chrome) when the PR changes no admin-UI path, or when
# the same strict run already passed on the PR head's exact tree (recorded by
# scripts/pipeline-gate.sh full or an earlier ship, in this clone's git dir; see
# .githooks/lib/gate-cache.sh, whose HM_UI_PATHS_RE this copies: ship.sh never
# sources files from a PR tree). HATAMOTO_GATE_CACHE=off ignores the record.
UI_PATHS_RE='^backend/web/admin/|^backend/internal/admin/|^scripts/admin-qa/'
# pr_fetch_head <sha>: fetch the PR head and origin/<base>, and check the head is still <sha>.
# The UI gate and the background full suite run this at the same time, and FETCH_HEAD and
# origin/<base> are shared by the clone, so it holds a lock (flock, where installed).
pr_fetch_head() {
  (
    if command -v flock >/dev/null; then exec 9>"$(git rev-parse --git-common-dir)/ship-fetch.lock"; flock 9; fi
    git fetch -q origin "pull/$PR/head" \
      || { echo "SHIP-H ABORT: cannot fetch the head of PR #$PR" >&2; exit 1; }
    [[ "$(git rev-parse FETCH_HEAD)" == "$1" ]] \
      || { echo "SHIP-H ABORT: PR #$PR head moved while fetching ($1); run ship again" >&2; exit 1; }
    git fetch -q origin "$BASE_REF" \
      || { echo "SHIP-H ABORT: cannot fetch origin/$BASE_REF" >&2; exit 1; }
  ) || exit 1
}
local_ui_gate() {
  local sha=$1 changed tree key
  pr_fetch_head "$sha"
  changed=$(git diff --name-only "origin/$BASE_REF...$sha") \
    || { echo "SHIP-H ABORT: cannot diff PR #$PR against origin/$BASE_REF" >&2; exit 1; }
  if ! grep -qE "$UI_PATHS_RE" <<< "$changed"; then
    echo "local UI gate not needed: PR #$PR changes no admin-UI path"
    return 0
  fi
  tree=$(git rev-parse "$sha^{tree}") \
    || { echo "SHIP-H ABORT: cannot read the tree of $sha" >&2; exit 1; }
  key="$(cd "$(git rev-parse --git-common-dir)" && pwd)/hatamoto-gate-cache/$tree/ui-gate"
  if [[ ${HATAMOTO_GATE_CACHE:-on} != off && -f "$key" ]]; then
    echo "local UI gate already passed on this tree ($(cat "$key")); not re-running"
    return 0
  fi
  GATE_TREE="$(mktemp -d)/gate-$PR"
  git worktree add -q --detach "$GATE_TREE" "$sha" \
    || { echo "SHIP-H ABORT: cannot create the gate worktree at $sha" >&2; exit 1; }
  [[ -f "$GATE_TREE/scripts/admin-qa/run-local.sh" && -f "$GATE_TREE/scripts/admin-qa/qa-env.sh" ]] \
    || { echo "SHIP-H ABORT: PR #$PR head has no scripts/admin-qa; rebase it onto main" >&2; exit 1; }
  # Everything below runs PR-supplied code (qa-env.sh, go test, run-local.sh), so
  # it runs in a subshell: nothing it defines or exports reaches this script.
  (
    set -Eeuo pipefail
    cd "$GATE_TREE"
    # shellcheck source=admin-qa/qa-env.sh
    . scripts/admin-qa/qa-env.sh
    if [[ -z "${DATABASE_URL:-}" ]]; then
      pgbin=$(qa_find_pg_bin)
      [[ -n "$pgbin" ]] || { echo "SHIP-H ABORT: local UI gate needs Postgres (initdb); set PG_BIN. Not skipping: it is the only UI gate" >&2; exit 1; }
    fi
    chrome=${CHROME:-/usr/bin/google-chrome}
    [[ -x "$chrome" ]] || command -v "$chrome" >/dev/null \
      || { echo "SHIP-H ABORT: local UI gate needs Chrome; set CHROME. Not skipping: it is the only UI gate" >&2; exit 1; }
    echo "local UI gate on $sha: make design-check"
    make design-check \
      || { echo "SHIP-H ABORT: design-check failed on $sha" >&2; exit 1; }
    echo "local UI gate on $sha: admin-qa run-local.sh (self-test + consistency, strict)"
    QA_STRICT=1 bash scripts/admin-qa/run-local.sh \
      || { echo "SHIP-H ABORT: admin-qa consistency failed on $sha" >&2; exit 1; }
  ) || exit 1
  mkdir -p "$(dirname "$key")" && date -u +%FT%TZ > "$key" || true
  echo "local UI gate passed on $sha"
}

# full_suite_gate <sha>: the FULL suite (every package, every check suite) on the PR head,
# once per tree. The installed pre-push hook is the one definition of "full": it runs from the
# snapshot `make hooks` took (the driver is the trusted snapshot, never a file of the PR tree) with HATAMOTO_PREPUSH_FULL=1
# on a worktree of <sha> that it makes itself, and records `full-suite` for the tree only when
# every gate passed and none was skipped (and the knobs that change what a run checks or skips are unset for it). A red run,
# or a pass with no record, aborts before the merge. The host-wide farm slot (the snapshot's
# farm.sh) is held for the run and handed to the hook through HATAMOTO_FARM_SLOT_HELD.
# `git push` runs only the affected gates, so this is where the whole suite runs.
# What the driver runs is NOT trusted: the PR's own tests, gate scripts and Makefile execute here,
# as this user, so review them before shipping. Two limits: a PR from another repository is refused
# up front (step 1), and the run gets a scrubbed environment (credentials, DEPLOY_HOST, the post-ship
# hook; see full_suite_env). SSH_AUTH_SOCK is dropped too: the build-host offload (go-remote-exec,
# BatchMode ssh) authenticates with the default key, not the agent. The private keys on disk stay
# readable by that code, which is why the same-repository rule matters more than the scrub.
full_suite_key() { # <tree>
  echo "$(cd "$(git rev-parse --git-common-dir)" && pwd)/hatamoto-gate-cache/$1/full-suite"
}
# full_suite_env: print the `-u NAME` arguments of an `env` that drops secrets before PR code runs.
full_suite_env() {
  local v
  for v in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN DEPLOY_HOST HATAMOTO_POST_SHIP_HOOK SSH_AUTH_SOCK \
           HATAMOTO_CHECK_SUITES HATAMOTO_FLAKES_FILE HATAMOTO_SKIP_INTEGRATION HATAMOTO_GATE_CACHE \
           PGPASSWORD DATABASE_URL; do
    printf '%s\n' "$v"
  done
  compgen -e | grep -E '^AWS_|_(TOKEN|SECRET|PASSWORD|API_KEY|KEY_ID|ACCESS_KEY|DSN|CREDENTIALS)$' || true
}
# docs_only <sha>: succeed (and set DOCS_ONLY_N) when every file the PR changes is under .pi/ or is
# Markdown that no test or check suite reads, so the full suite would test nothing the PR touched.
# "Read" = a glob of scripts/affected-readers.txt and scripts/check-suites.txt as they are on the
# fetched origin/<base> (trusted, always current; never the PR's own, never the hooks snapshot, which
# goes stale), anything under docs/site/, backend/ (embedded
# files), the gate and deploy directories, or one of the docs the tests name. Fails closed: any
# other file, a mode other than a plain file, an empty diff, a table that cannot be read from
# origin/<base>, an origin/<base> that is not an ancestor of the PR head (stale check rules), a bad table line or any git error returns 1 and the full suite runs.
docs_only() {
  local sha=$1 line id globs g f meta path raw new rc=0 readers suites
  local -a pats=() m
  if (( !DRY )); then
    git fetch -q origin "pull/$PR/head" && [[ "$(git rev-parse FETCH_HEAD)" == "$sha" ]] || return 1
    git fetch -q origin "$BASE_REF" || return 1
  fi
  # The content checks run the PR tree's own check scripts and rules: only current ones when the PR
  # contains origin/<base>. A branch behind it runs the full suite (also in a dry run: the ref is local).
  git merge-base --is-ancestor "origin/$BASE_REF" "$sha" || return 1
  readers=$(git show "origin/$BASE_REF:scripts/affected-readers.txt" 2>/dev/null) || return 1
  suites=$(git show "origin/$BASE_REF:scripts/check-suites.txt" 2>/dev/null) || return 1
  while IFS= read -r line; do
    [[ -z $line || $line == \#* ]] && continue
    read -r g _ <<< "$line"; pats+=("${g//\*\*/*}")
  done <<< "$readers"
  while IFS= read -r line; do
    [[ -z $line || $line == \#* ]] && continue
    [[ $line =~ ^([^[:space:]]+)[[:space:]]+([^[:space:]]+)[[:space:]]+(.+)$ ]] || return 1
    globs=${BASH_REMATCH[2]}
    [[ $globs == '*' ]] && continue
    IFS=, read -ra m <<< "$globs"
    for g in "${m[@]}"; do pats+=("${g//\*\*/*}"); done
  done <<< "$suites"
  raw=$(mktemp) || return 1
  if git diff --raw -z --no-renames --no-abbrev "origin/$BASE_REF...$sha" > "$raw" 2>/dev/null; then
    DOCS_ONLY_N=0
    while IFS= read -r -d '' meta && IFS= read -r -d '' path; do
      read -r _ new _ <<< "$meta"
      [[ $new == 100644 || $new == 000000 ]] || { rc=1; break; }
      if [[ $path != .pi/* ]]; then
        [[ $path == *.md ]] || { rc=1; break; }
        case $path in
          backend/*|scripts/*|.githooks/*|.github/*|deploy/*|examples/*|design/*|releases/*|docs/site/*) rc=1; break ;;
          docs/ADMIN_UI_RULES.md|docs/CANVAS.md|docs/MCP.md|docs/UPGRADES.md|README.md|CONTRIBUTING.md|STABILITY.md) rc=1; break ;;
        esac
        for g in "${pats[@]}"; do
          # shellcheck disable=SC2254 # the glob is the point
          case $path in $g) rc=1; break 2 ;; esac
        done
      fi
      DOCS_ONLY_N=$((DOCS_ONLY_N + 1))
    done < "$raw"
    (( DOCS_ONLY_N > 0 )) || rc=1
  else
    rc=1
  fi
  rm -f "$raw"
  return $rc
}
# full_suite_gate <sha> [content]: with "content" (a docs-only PR) the same trusted hook runs in its
# normal affected mode (no HATAMOTO_PREPUSH_FULL): gofmt, gitleaks, DCO, the always-suites and the
# private-key scan on the PR's content. It records nothing and a red run aborts before the merge.
full_suite_gate() {
  local sha=$1 mode=${2:-full} snap hook tree full=()
  [[ $mode == content ]] || full=(HATAMOTO_PREPUSH_FULL=1)
  snap="$(cd "$(git rev-parse --git-common-dir)" && pwd)/hatamoto-hooks/snapshot/.githooks"
  hook=$snap/pre-push
  # An old hook takes --hook-api for the remote name and reads stdin: give it none, and a time limit.
  local to=(); ! command -v timeout >/dev/null || to=(timeout 10)
  local api; api=$("${to[@]}" "$hook" --hook-api </dev/null 2>/dev/null)
  [[ -x "$hook" && -f "$snap/lib/farm.sh" && $api =~ ^[0-9]+$ ]] && (( api >= 3 )) \
    || { echo "SHIP-H ABORT: the installed pre-push hook cannot run the full suite (stale or missing): run make hooks" >&2; exit 1; }
  pr_fetch_head "$sha"
  tree=$(git rev-parse "$sha^{tree}") \
    || { echo "SHIP-H ABORT: cannot read the tree of $sha" >&2; exit 1; }
  # shellcheck source=/dev/null
  . "$snap/lib/farm.sh"
  if [[ $mode == content ]]; then
    echo "full suite on ${sha:0:12}: docs only, running the content checks (pre-push, affected mode)"
  else
    echo "full suite on ${sha:0:12}: running the full pre-push suite (HATAMOTO_PREPUSH_FULL=1)"
  fi
  hm_farm_acquire "ship #$PR"
  local scrub=() v
  while IFS= read -r v; do scrub+=(-u "$v"); done < <(full_suite_env | sort -u)
  printf 'refs/heads/%s %s refs/heads/%s %s\n' "$HEAD_REF" "$sha" "$HEAD_REF" 0000000000000000000000000000000000000000 \
    | env "${scrub[@]}" ${full[@]+"${full[@]}"} HATAMOTO_PREPUSH_SINCE="origin/$BASE_REF" HATAMOTO_FARM_SLOT_HELD=1 "$hook" origin "$(git remote get-url origin)" \
    || { echo "SHIP-H ABORT: the $([[ $mode == content ]] && echo content checks || echo full suite) failed on $sha; nothing was merged" >&2; exit 1; }
  if [[ $mode == content ]]; then
    hm_farm_release
    echo "full suite on ${sha:0:12}: skipped (plans/docs only: $DOCS_ONLY_N files); content checks: ok"
    return 0
  fi
  [[ -f "$(full_suite_key "$tree")" ]] \
    || { echo "SHIP-H ABORT: the full suite passed without recording itself on $sha (a gate was skipped?); not merging" >&2; exit 1; }
  hm_farm_release
  echo "full suite passed on $sha"
}

# ---------------------------------------------------------------- step 1
step 1 "inspect and merge PR #$PR"
STATE=$(gh pr view "$PR" --json state --jq .state)
MERGEABLE=$(gh pr view "$PR" --json mergeable --jq .mergeable)
MSTATUS=$(gh pr view "$PR" --json mergeStateStatus --jq .mergeStateStatus)
# GitHub computes mergeability lazily after a push: UNKNOWN is "not computed yet",
# not "not mergeable". Poll briefly (read-only) instead of aborting.
for _ in $(seq 1 ${SHIP_MERGEABLE_WAIT:-12}); do
  [[ "$STATE" == OPEN && ( "$MERGEABLE" == UNKNOWN || "$MSTATUS" == UNKNOWN ) ]] || break
  sleep 5
  MERGEABLE=$(gh pr view "$PR" --json mergeable --jq .mergeable)
  MSTATUS=$(gh pr view "$PR" --json mergeStateStatus --jq .mergeStateStatus)
done
HEAD_REF=$(gh pr view "$PR" --json headRefName --jq .headRefName)
BASE_REF=$(gh pr view "$PR" --json baseRefName --jq .baseRefName)
echo "state=$STATE mergeable=$MERGEABLE mergeStateStatus=$MSTATUS head=$HEAD_REF base=$BASE_REF"

DEFAULT_BRANCH=$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name)
if [[ "$BASE_REF" != "main" && "$BASE_REF" != "$DEFAULT_BRANCH" ]]; then
  echo "SHIP-H ABORT: PR #$PR targets '$BASE_REF', not main/$DEFAULT_BRANCH" >&2
  exit 1
fi

# ci_checks_gate: CI mode's merge gate. Polls the PR head's checks until none is pending
# (SHIP_CI_WAIT seconds, every SHIP_CI_POLL), stops at the first failed one, and requires
# `backend` (the whole Go suite) and `admin-ui` to have run and passed: mergeStateStatus CLEAN
# only reflects the checks that the ruleset lists. A push during the wait aborts (the checks
# would be for another commit). statusCheckRollup is read with `gh pr view` (gh 2.46 has no
# `gh pr checks --json`). Rows: name, status, conclusion; a StatusContext has context/state
# (treated as completed with that state). No override.
ci_checks_gate() {
  local deadline=$((SECONDS + ${SHIP_CI_WAIT:-3600})) rollup cname cstatus cconc pending now_head
  local -A seen
  while :; do
    if ! rollup=$(gh pr view "$PR" --json statusCheckRollup --jq \
      '.statusCheckRollup[] | [(.name // .context), (.status // "COMPLETED"), (.conclusion // .state // "")] | @tsv' 2>&1); then
      echo "$rollup" >&2
      echo "SHIP-H ABORT: cannot read the checks of PR #$PR (gh pr view statusCheckRollup)" >&2
      exit 1
    fi
    pending=(); seen=()
    while IFS=$'\t' read -r cname cstatus cconc; do
      [[ -n "$cname" ]] || continue
      if [[ "$cstatus" != COMPLETED ]]; then pending+=("$cname"); continue; fi
      case "$cconc" in
        SUCCESS|SKIPPED|NEUTRAL) seen[$cname]=$cconc ;;
        *) echo "SHIP-H ABORT: PR #$PR check $cname is '${cconc:-unknown}'" >&2; exit 1 ;;
      esac
    done <<< "$rollup"
    if (( ${#pending[@]} == 0 )); then
      local need
      for need in backend admin-ui; do
        if [[ "${seen[$need]:-}" != SUCCESS ]]; then
          echo "SHIP-H ABORT: PR #$PR check $need is '${seen[$need]:-missing}', not SUCCESS" >&2
          exit 1
        fi
      done
      echo "CI checks on ${HEAD_SHA:0:12}: all green"
      return 0
    fi
    if (( DRY || SECONDS >= deadline )); then
      echo "SHIP-H ABORT: PR #$PR check ${pending[0]} is not completed$( (( DRY )) || echo " after ${SHIP_CI_WAIT:-3600}s") (pending: ${pending[*]})" >&2
      exit 1
    fi
    echo "waiting for CI on ${HEAD_SHA:0:12}: ${#pending[@]} pending (${pending[*]})"
    sleep "${SHIP_CI_POLL:-30}"
    now_head=$(gh pr view "$PR" --json headRefOid --jq .headRefOid 2>/dev/null || true)
    if [[ "$now_head" != "$HEAD_SHA" ]]; then
      echo "SHIP-H ABORT: PR #$PR head moved during the CI wait (${HEAD_SHA:0:12} -> ${now_head:0:12}); run ship again" >&2
      exit 1
    fi
  done
}

case "$STATE" in
  OPEN)
    # CI mode accepts BLOCKED/UNSTABLE here (checks still running) and requires CLEAN after them.
    if [[ "$MERGEABLE" != "MERGEABLE" || ! "$MSTATUS" =~ ^(CLEAN|BLOCKED|UNSTABLE)$ ]]; then
      echo "SHIP-H ABORT: PR #$PR is not mergeable/clean ($MERGEABLE/$MSTATUS)" >&2
      exit 1
    fi
    # The head commit that was checked is the one that merges: pinned below with
    # --match-head-commit, so a push during the gate makes GitHub refuse the merge.
    if ! HEAD_SHA=$(gh pr view "$PR" --json headRefOid --jq .headRefOid 2>&1) || [[ ! "$HEAD_SHA" =~ ^[0-9a-f]{40}$ ]]; then
      echo "$HEAD_SHA" >&2
      echo "SHIP-H ABORT: cannot read the head commit of PR #$PR (gh pr view headRefOid)" >&2
      exit 1
    fi
    # A PR from another repository (a fork) has its code run here (the UI gate and the full suite run its
    # tests, scripts and Makefile as this user). Refuse it, with no override: review the code, push it to a branch
    # of this repository, and ship that branch's PR.
    if ! XREPO=$(gh pr view "$PR" --json isCrossRepository --jq .isCrossRepository 2>&1) || [[ "$XREPO" != true && "$XREPO" != false ]]; then
      echo "$XREPO" >&2
      echo "SHIP-H ABORT: cannot tell whether PR #$PR comes from a fork (gh pr view isCrossRepository)" >&2
      exit 1
    fi
    if [[ "$XREPO" == true ]]; then
      echo "SHIP-H ABORT: PR #$PR comes from another repository ($(gh pr view "$PR" --json headRepositoryOwner --jq .headRepositoryOwner.login 2>/dev/null || echo unknown)); shipping it would run its code here." >&2
      echo "  Review the code (tests, scripts/, .githooks/, Makefile, .gitleaks.toml), then:" >&2
      echo "    git fetch origin pull/$PR/head && git push --no-verify origin FETCH_HEAD:refs/heads/review/pr-$PR" >&2
      echo "  Only after reading the diff: a plain git push would run the fork's tests on this machine through the pre-push" >&2
      echo "  hook (--no-verify skips it; the ship full suite then runs that code with the scrubbed environment)." >&2
      echo "  and open and ship the PR from that branch." >&2
      exit 1
    fi
    # The gate depends on whether the GitHub 'ci' workflow is active. When it is disabled the
    # local UI gate and the local full suite are the gates, so they never skip. No override.
    if ! WF=$(gh workflow list --all --json name,state --jq '.[] | select(.name == "ci") | .state' 2>&1); then
      echo "$WF" >&2
      echo "SHIP-H ABORT: cannot read the state of the 'ci' workflow (gh workflow list)" >&2
      exit 1
    fi
    if [[ "$WF" == active ]]; then
      echo "gate mode: CI (workflow 'ci' is active): waiting for the PR's status checks"
      ci_checks_gate
      # GitHub recomputes the merge state lazily after the last check finishes: for a few
      # seconds it still reads UNKNOWN or BLOCKED (seen on PR #32). Re-read (read-only) for up
      # to SHIP_CLEAN_WAIT tries, SHIP_CLEAN_POLL seconds apart, before calling it not clean.
      MSTATUS=$(gh pr view "$PR" --json mergeStateStatus --jq .mergeStateStatus)
      for _ in $(seq 1 "${SHIP_CLEAN_WAIT:-12}"); do
        [[ "$MSTATUS" =~ ^(UNKNOWN|BLOCKED|UNSTABLE)$ ]] || break
        sleep "${SHIP_CLEAN_POLL:-5}"
        MSTATUS=$(gh pr view "$PR" --json mergeStateStatus --jq .mergeStateStatus)
      done
      if [[ "$MSTATUS" != CLEAN ]]; then
        echo "SHIP-H ABORT: PR #$PR checks are green but it is not clean ($MSTATUS: a required review, an unresolved thread or a conflict)" >&2
        exit 1
      fi
      echo "full suite on ${HEAD_SHA:0:12}: covered by CI (backend, admin-ui and every other check green); not re-run here"
    else
      if [[ "$MSTATUS" != CLEAN ]]; then
        echo "SHIP-H ABORT: PR #$PR is not mergeable/clean ($MERGEABLE/$MSTATUS)" >&2
        exit 1
      fi
      echo "gate mode: LOCAL UI gate (workflow 'ci' is '${WF:-missing}')"
      # The whole suite runs once per tree, here, before the merge (a push runs only the affected
      # gates). A pass recorded by `scripts/pipeline-gate.sh full` or an earlier ship on this tree
      # is reused. It runs in the BACKGROUND while the UI gate runs: the suite is mostly on the
      # build farm, the UI gate is local Chrome, they share nothing, and serially they took ~30
      # min. Both must pass before the merge; either failing stops the other (cleanup kills FS_PID).
      FS_TREE=$(git rev-parse "$HEAD_SHA^{tree}" 2>/dev/null || true)
      FS_KEY=""; [[ -z "$FS_TREE" ]] || FS_KEY=$(full_suite_key "$FS_TREE")
      FS_HIT=0; [[ -n "$FS_KEY" && -f "$FS_KEY" && ${HATAMOTO_GATE_CACHE:-on} != off ]] && FS_HIT=1
      DOCS_ONLY_N=0; FS_MODE=""
      if docs_only "$HEAD_SHA"; then
        if (( DRY )); then
          echo "full suite on ${HEAD_SHA:0:12}: skipped (plans/docs only: $DOCS_ONLY_N files)"
          echo "DRY: would run the content checks on $HEAD_SHA (trusted pre-push hook, affected mode, before the merge)"
        else
          FS_MODE=content
        fi
      elif (( DRY )); then
        echo "DRY: would run full suite on $HEAD_SHA ($( (( FS_HIT )) && echo cached || echo 'not cached')), alongside the UI gate"
      elif (( FS_HIT )); then
        echo "full suite on ${HEAD_SHA:0:12} ... cached ($(cat "$FS_KEY"))"
      else
        FS_MODE=full
      fi
      if [[ -n $FS_MODE ]]; then
        FS_LOG=$(mktemp "${TMPDIR:-/tmp}/ship-$PR-fullsuite.XXXXXX")
        echo "full suite on ${HEAD_SHA:0:12}: started in the background ($FS_MODE; log $FS_LOG)"
        set -m                       # own process group, so cleanup can stop the whole suite
        ( trap - EXIT; full_suite_gate "$HEAD_SHA" "$FS_MODE" ) </dev/null >"$FS_LOG" 2>&1 &
        FS_PID=$!
        set +m
      fi
      if (( DRY )); then
        echo "DRY: would run local UI gate on $HEAD_SHA"
      else
        local_ui_gate "$HEAD_SHA"
      fi
      if [[ -n ${FS_PID:-} ]]; then
        echo "waiting for the full suite on ${HEAD_SHA:0:12} (log $FS_LOG)"
        FS_RC=0; wait "$FS_PID" || FS_RC=$?
        FS_PID=""
        sed 's/^/  [full suite] /' "$FS_LOG"
        (( FS_RC == 0 )) || { echo "SHIP-H ABORT: the full suite failed on $HEAD_SHA (exit $FS_RC); nothing was merged" >&2; exit 1; }
        rm -f "$FS_LOG"
      fi
    fi
    run gh pr merge "$PR" --merge --match-head-commit "$HEAD_SHA"
    ;;
  MERGED) echo "already merged; continuing with deploy" ;;
  *) echo "SHIP-H ABORT: PR #$PR state is $STATE" >&2; exit 1 ;;
esac

if (( DRY )); then
  echo "DRY: git fetch origin $BASE_REF; git fetch --tags origin; git fetch origin $HEAD_REF (best effort)"
  echo "note: dry-run uses the locally known origin/$BASE_REF"
else
  # Base must fetch (a stale origin/main would be a silent downgrade).
  git fetch -q origin "$BASE_REF"
  # Tags give the dev version its base (scripts/version.sh); best effort.
  git fetch -q --tags origin 2>/dev/null || true
  # The head branch is usually deleted after merge; tolerate its absence.
  if git fetch -q origin "$HEAD_REF" 2>/dev/null; then
    EXTRA=$(git log --oneline "origin/$BASE_REF..FETCH_HEAD")
    if [[ -n "$EXTRA" ]]; then
      echo "WARNING: follow-up commits not in $BASE_REF; open a follow-up PR:"
      echo "$EXTRA"
    fi
  else
    echo "note: head branch $HEAD_REF not on origin; skipping follow-up commits check"
  fi
fi
DEPLOY_SHA=$(git rev-parse "origin/$BASE_REF")
MERGE_SHA=$(gh pr view "$PR" --json mergeCommit --jq '.mergeCommit.oid // empty')
(( DRY )) && [[ -z "$MERGE_SHA" ]] && MERGE_SHA=pending
echo "pr merge sha: ${MERGE_SHA:-unknown}"
echo "deploy sha:   $DEPLOY_SHA"

# Run the ship.sh that belongs to DEPLOY_SHA: an invoking checkout that is behind
# origin/main would otherwise drive the new Makefile with an old script (PR #77:
# failed after migrations were applied). SHIP_REEXEC guards against loops.
if [[ -n "${SHIP_DEPLOY_SHA:-}" && "$SHIP_DEPLOY_SHA" != "$DEPLOY_SHA" ]]; then
  echo "SHIP-H ABORT: origin/$BASE_REF moved during re-exec ($SHIP_DEPLOY_SHA -> $DEPLOY_SHA); run ship again" >&2
  exit 1
fi
if [[ -z "${SHIP_REEXEC:-}" ]]; then
  if ! git show "$DEPLOY_SHA:scripts/ship.sh" > /dev/null 2>&1; then
    echo "SHIP-H ABORT: scripts/ship.sh missing at $DEPLOY_SHA" >&2; exit 1
  fi
  if ! git diff --quiet HEAD "$DEPLOY_SHA" -- scripts/ship.sh \
     || ! git show "$DEPLOY_SHA:scripts/ship.sh" | cmp -s - "$0"; then
    SELF=$(mktemp "${TMPDIR:-/tmp}/ship-$PR.XXXXXX")
    git show "$DEPLOY_SHA:scripts/ship.sh" > "$SELF"
    echo "note: ship.sh differs from the one at $DEPLOY_SHA; re-executing the deploy-sha copy"
    SHIP_REEXEC=1 SHIP_DEPLOY_SHA="$DEPLOY_SHA" SHIP_SELF_TMP="$SELF" DEPLOY_HOST="$DEPLOY_HOST" exec bash "$SELF" "$@"
  fi
fi
if [[ "$MERGE_SHA" =~ ^[0-9a-f]{40}$ ]]; then
  if git cat-file -e "$MERGE_SHA^{commit}" 2>/dev/null; then
    git merge-base --is-ancestor "$MERGE_SHA" "$DEPLOY_SHA" \
      || { echo "SHIP-H ABORT: merge commit $MERGE_SHA is not an ancestor of origin/$BASE_REF ($DEPLOY_SHA)" >&2; exit 1; }
  elif (( DRY )); then
    echo "note: merge commit not available locally (dry-run, no fetch); ancestor check skipped"
  else
    echo "SHIP-H ABORT: merge commit $MERGE_SHA not found after fetching origin/$BASE_REF" >&2; exit 1
  fi
elif (( !DRY )); then
  echo "SHIP-H ABORT: PR #$PR has no merge commit" >&2; exit 1
fi

# ---------------------------------------------------------------- step 2
step 2 "temporary worktree at deploy sha"
if (( DRY )); then
  echo "DRY: git worktree add --detach <tmp>/ship-$PR $DEPLOY_SHA"
  TREE="<tmp>/ship-$PR"
else
  TMP="$(mktemp -d)/ship-$PR"
  git worktree add -q --detach "$TMP" "$DEPLOY_SHA"
  [[ "$(git -C "$TMP" rev-parse HEAD)" == "$DEPLOY_SHA" ]] \
    || { echo "SHIP-H ABORT: worktree HEAD != $DEPLOY_SHA" >&2; exit 1; }
  TREE="$TMP"
  echo "worktree at $TMP == $DEPLOY_SHA"
fi

# ---------------------------------------------------------------- step 3
step 3 "classify changed files"
# Everything in DEPLOY_SHA ships, so classify against what the box already has:
# $REMOTE_DIR/DEPLOYED_SHA (written after each healthy ship) when it is a known
# ancestor of DEPLOY_SHA; with no usable file the box state is unknown, so
# everything is deployed. Both box reads below are read-only and also run in --dry-run.
BASE=""; BASE_WHY=""; FULL=0
if (( !NO_DEPLOY )); then
  BOX_SHA=$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$DEPLOY_HOST" "cat $REMOTE_DIR/DEPLOYED_SHA 2>/dev/null" 2>/dev/null || true)
  BOX_SHA=${BOX_SHA//[[:space:]]/}
  if [[ "$BOX_SHA" =~ ^[0-9a-f]{40}$ ]] && git cat-file -e "$BOX_SHA^{commit}" 2>/dev/null \
     && git merge-base --is-ancestor "$BOX_SHA" "$DEPLOY_SHA"; then
    BASE=$BOX_SHA; BASE_WHY="box DEPLOYED_SHA"
  else
    echo "note: no usable DEPLOYED_SHA -> full deploy"
    FULL=1; MIG=1; API=1
  fi
fi
FILES=""
if [[ -n "$BASE" ]]; then
  echo "classifying $BASE_WHY..$DEPLOY_SHA"
  FILES=$(git diff --name-only "$BASE" "$DEPLOY_SHA")
else
  echo "note: no base to diff against; nothing classified"
fi
while IFS= read -r f; do
  [[ -n "$f" ]] || continue
  case "$f" in
    backend/*) API=1 ;;  # migrations are embedded in the binary, so they ship with it
  esac
done <<< "$FILES"
# Migrations are embedded in the binary: they are applied whenever the API is
# deployed (`hatamoto migrate` is idempotent), and never without it.
(( API && !NO_DEPLOY )) && MIG=1
(( ALL )) && { echo "--all: forcing migrations and deploy"; MIG=1; API=1; }
(( MIG )) && API=1
echo "migrations=$MIG deploy=$API"
if (( NO_DEPLOY )); then
  echo "--no-deploy: skipping migrations, deploy and health"
  MIG=0; API=0
elif (( !MIG && !API )); then
  echo "no deploy needed"
fi

# ---------------------------------------------------------------- preflight
# Config errors must fail here, before the backup or any migration.
if (( !NO_DEPLOY && (MIG || API) )); then
  step 3b "preflight"
  if (( DRY )); then
    echo "DRY: preflight: ssh -o BatchMode=yes $DEPLOY_HOST true; sudo, SHIP_DB matches DATABASE_URL, deploy/sql/role-admin-bootstrap.sql as postgres, free disk >= 2.5x $DB (migrations are embedded in the binary)"
  else
    ssh -o BatchMode=yes -o ConnectTimeout=10 "$DEPLOY_HOST" true \
      || { echo "SHIP-H ABORT: preflight: cannot ssh to $DEPLOY_HOST (BatchMode)" >&2; exit 1; }
    if (( MIG )); then
      ssh -o BatchMode=yes "$DEPLOY_HOST" "sudo -n -u postgres true" \
        || { echo "SHIP-H ABORT: preflight: passwordless sudo -u postgres missing on $DEPLOY_HOST" >&2; exit 1; }
      ssh -o BatchMode=yes "$DEPLOY_HOST" "set -a; . $REMOTE_DIR/config/hatamoto.env; u=\${DATABASE_URL%%\\?*}; [ \"\${u##*/}\" = '$DB' ]" \
        || { echo "SHIP-H ABORT: preflight: SHIP_DB=$DB is not the database in $REMOTE_DIR/config/hatamoto.env DATABASE_URL" >&2; exit 1; }
      # Plugins get their own Postgres roles (docs/PLUGIN_API.md "Own data"). The
      # application role must NOT get CREATEROLE (on a shared cluster its role may own
      # other databases): a per-database NOLOGIN hatamoto_role_admin_<id8> holds that privilege and the
      # application reaches it only through two SECURITY DEFINER functions in schema
      # hatamoto_admin. Idempotent; run as postgres in the Hatamoto database only.
      ssh -o BatchMode=yes "$DEPLOY_HOST" "set -a; . $REMOTE_DIR/config/hatamoto.env; u=\${DATABASE_URL#*://}; u=\${u%%[:@]*}; sudo -n -u postgres psql -X -v ON_ERROR_STOP=1 -v app_role=\"\$u\" -d $DB -f -" < "$TREE/deploy/sql/role-admin-bootstrap.sql" \
        || { echo "SHIP-H ABORT: preflight: cannot run deploy/sql/role-admin-bootstrap.sql (plugin roles)" >&2; exit 1; }
      # A rollback restores a full copy next to the live database (plus WAL) on a
      # cluster shared with other stacks: refuse unless there is room for ~2.5x.
      ssh -o BatchMode=yes "$DEPLOY_HOST" "bash -s -- $DB" <<'REMOTE' \
        || { echo "SHIP-H ABORT: preflight: not enough free disk for a rollback restore of $DB" >&2; exit 1; }
set -e
q() { sudo -n -u postgres psql -X -d postgres -Atc "$1"; }
size=$(q "SELECT pg_database_size('$1')")
dir=$(q "SHOW data_directory")
avail=$(sudo -n -u postgres df -B1 --output=avail "$dir" | tail -n1 | tr -d '[:space:]')
echo "preflight: $1 is $size bytes; $avail bytes free in $dir"
[ "$avail" -gt $(( size * 5 / 2 )) ]
REMOTE
    fi
  fi
fi

# ---------------------------------------------------------------- health
# Usage: health <journal-since>. Only logs since that remote timestamp count,
# so a previous (failed) binary's panic cannot fail a later check.
# Prints failures to stderr; returns 0 only when everything is healthy.
health() {
  local since=$1 i ok logs body
  for i in $(seq 1 10); do
    ok=1
    ssh "$DEPLOY_HOST" systemctl is-active hatamoto-api >/dev/null 2>&1 || ok=0
    (( ok )) && { { body=$(curl -fsS "$SITE_URL/health") && { [[ "$body" == ok ]] || grep -q '"status":"healthy"' <<<"$body"; }; } && curl -fsS -o /dev/null "$SITE_URL/"; } 2>/dev/null || ok=0
    # /media is served from MEDIA_UPLOAD_DIR (relative to the unit's WorkingDirectory): a moved
    # upload dir would 404 uploads while the site stays up. Probe a real file if one exists.
    if (( ok )); then
      media=$(ssh "$DEPLOY_HOST" "sudo -u postgres psql -d $DB -Atc \"SELECT public_url FROM hatamoto_media WHERE public_url LIKE '/media/%' ORDER BY created_at DESC LIMIT 1\"" 2>/dev/null | head -n1 | tr -d '[:space:]') || media=""
      if [[ "$media" == /media/* ]]; then
        curl -fsS -o /dev/null "$SITE_URL$media" 2>/dev/null || { echo "health: $SITE_URL$media failed (uploads dir moved?)" >&2; ok=0; }
      else
        code=$(curl -s -o /dev/null -w '%{http_code}' "$SITE_URL/media/" 2>/dev/null || echo 000)
        [[ "$code" =~ ^[1-4][0-9][0-9]$ ]] || { echo "health: $SITE_URL/media/ returned $code" >&2; ok=0; }
      fi
    fi
    if (( ok )); then
      if ! logs=$(ssh "$DEPLOY_HOST" "journalctl -u hatamoto-api --no-pager --since '$since'" 2>/dev/null); then
        echo "health: could not read hatamoto-api logs since $since" >&2
        ok=0
      elif grep -Ei 'panic|FATAL' >/dev/null <<< "$logs"; then
        echo "health: panic/FATAL in hatamoto-api logs since $since" >&2
        ok=0
      fi
    fi
    (( ok )) && return 0
    echo "health attempt $i/10 failed" >&2
    sleep 3
  done
  return 1
}

# Fingerprint of the applied migrations on the box (empty output + rc!=0 if
# unreadable): "<rows>:<max id>" of goose_db_version. Every apply or rollback
# adds a row with a larger serial id, so it changes even when goose applies an
# out-of-order migration (older version) that leaves max(version_id) unchanged.
goose_ver() {
  local v
  v=$(ssh "$DEPLOY_HOST" "sudo -u postgres psql -d $DB -Atc \"SELECT count(*) || ':' || coalesce(max(id), 0) FROM goose_db_version\"" 2>/dev/null) || return 1
  v=${v//[[:space:]]/}
  [[ "$v" =~ ^[0-9]+:[0-9]+$ ]] || return 1
  echo "$v"
}

# Remote UTC timestamp for journalctl --since (taken right before a restart).
remote_now() { ssh "$DEPLOY_HOST" 'date -u "+%Y-%m-%d %H:%M:%S"' 2>/dev/null | sed 's/$/ UTC/' || true; }

RB_SINCE=""; RB_DB_FAILED=0
# Undoes whichever stages started (flags are set BEFORE each mutation).
# The DB is restored by scripts/ship-db-restore.sh (fresh database from the
# dump, renamed into place); the failed database is kept for forensics.
rollback() {
  echo "-- rolling back"
  RB_SINCE=$(remote_now); RB_SINCE=${RB_SINCE:--2min}
  if (( MIG_RAN )); then
    local post restore=1
    if [[ -n "$GOOSE_PRE" ]] && post=$(goose_ver); then
      if [[ "$post" == "$GOOSE_PRE" ]]; then
        restore=0
        echo "applied-migrations fingerprint unchanged ($GOOSE_PRE): database NOT restored"
      else
        echo "applied-migrations fingerprint changed ($GOOSE_PRE -> $post): restoring database"
      fi
    else
      echo "applied-migrations fingerprint unreadable: restoring database (fail safe)"
    fi
    if (( restore )); then
      # The helper comes from the deploy-SHA worktree and is passed to bash as
      # an argument (stdin is drained by cat, so nothing inside can eat the
      # script). It only stops the API; the binary is restored below and the
      # API started once, afterwards.
      ssh "$DEPLOY_HOST" "CONFIG_ENV=$REMOTE_DIR/config/hatamoto.env bash -c \"\$(cat)\" _ $DB $DUMP $TS" \
          < "$TREE/scripts/ship-db-restore.sh" \
        || { RB_DB_FAILED=1
             echo "ROLLBACK INCOMPLETE: database $DB was NOT restored (still at the new schema)." >&2
             echo "  dump: $DUMP on $DEPLOY_HOST; leftovers to inspect/drop: ${DB}_restore_$TS, ${DB}_failed_$TS" >&2; }
    fi
  fi
  if (( DEPLOYED_API )); then
    # Restore each binary from its pre-ship copy; one that did not exist before
    # (first deploy of `hatamoto`) is removed again. Same for the unit file.
    # On the first deploy this brings back the old hatamoto-api binary + old unit.
    ssh "$DEPLOY_HOST" "set -e; cd $REMOTE_DIR/bin
for b in hatamoto hatamoto-api; do
  if [ -f \$b.pre-ship ]; then cp -a \$b.pre-ship \$b.rollback && mv -f \$b.rollback \$b; else rm -f \$b; fi
done
if [ -f hatamoto-api.service.pre-ship ]; then cp -a hatamoto-api.service.pre-ship $UNIT.rollback && mv -f $UNIT.rollback $UNIT; else rm -f $UNIT; fi
systemctl daemon-reload" \
      || echo "rollback: binary/unit restore failed" >&2
  fi
  if (( DEPLOYED_API || MIG_RAN )); then
    ssh "$DEPLOY_HOST" "systemctl restart hatamoto-api" || echo "rollback: API restart failed" >&2
  fi
}

# Any failure (or INT/TERM/HUP) after the point of no return (traps armed below) rolls back
# whatever stages started, re-checks health and exits 2 (or 3).
on_fail() {
  set +e   # nothing here may abort the rollback (e.g. echo to a dead terminal on SIGHUP)
  trap - ERR INT TERM HUP
  echo "SHIP-H: deploy failed" >&2
  rollback
  if (( RB_DB_FAILED )); then
    echo "SHIP-H FAILED (ROLLBACK INCOMPLETE: database not restored; restore $DUMP by hand)" >&2
    exit 3
  fi
  if health "$RB_SINCE"; then
    echo "SHIP-H FAILED (rolled back)" >&2
    exit 2
  fi
  echo "SHIP-H FAILED (rollback unhealthy)" >&2
  exit 3
}

# ---------------------------------------------------------------- build
# Build everything first so a build failure aborts before any prod mutation.
step 4 "build artifacts"
if (( API )); then
  if (( DRY )); then echo "stamping version <computed by scripts/version.sh in the deploy-sha tree>"
  else echo "stamping version $(cd "$TREE" && sh scripts/version.sh)"; fi
  run tree_make build-api
else echo "nothing to build"; fi

# ---------------------------------------------------------------- step 5
step 5 "migrate and deploy"
if (( MIG )); then
  echo "-- recording the applied-migrations fingerprint before migrating"
  GOOSE_PRE=""
  if (( !DRY )); then GOOSE_PRE=$(goose_ver) || GOOSE_PRE=""; fi
  echo "-- backing up $DB and verifying the dump (keeping newest 5)"
  run ssh "$DEPLOY_HOST" "umask 077; mkdir -p $REMOTE_DIR/backups && sudo -u postgres pg_dump -Fc $DB > $DUMP && pg_restore --list < $DUMP >/dev/null && ls -1 $REMOTE_DIR/backups/hcore-*.dump | sort | head -n -5 | xargs -r rm -f"
fi

# Rollback references are taken BEFORE anything is mutated (except the
# pre-ship copy of the binary); any failure here aborts with exit 1.
if (( API )); then
  echo "-- saving pre-ship copies of the binaries (hatamoto, hatamoto-api) and the unit file"
  run ssh "$DEPLOY_HOST" "cd $REMOTE_DIR/bin && for b in hatamoto hatamoto-api; do rm -f \$b.pre-ship; if [ -f \$b ]; then cp -a \$b \$b.pre-ship; fi; done; rm -f hatamoto-api.service.pre-ship; if [ -f $UNIT ]; then cp -a $UNIT hatamoto-api.service.pre-ship; fi"
  echo "-- unit check: diff vs deploy/systemd/hatamoto-api.service; aborts on drop-ins / Environment= lines it would drop (FORCE_UNIT=1 overrides)"
  if (( DRY )); then
    echo "DRY: make check-unit  (ssh $DEPLOY_HOST: diff the unit, refuse on drop-ins or lost Environment= lines)"
  else
    tree_make check-unit \
      || { echo "SHIP-H ABORT: the unit on $DEPLOY_HOST has settings the tracked unit would drop (nothing was changed)" >&2; exit 1; }
  fi
fi
# Point of no return: from here every failure goes through rollback().
if (( !DRY && (MIG || API) )); then
  trap on_fail ERR
  trap on_fail INT TERM HUP
fi
DEPLOY_SINCE=""
if (( !DRY && (MIG || API) )); then DEPLOY_SINCE=$(remote_now); DEPLOY_SINCE=${DEPLOY_SINCE:--5min}; fi

if (( API )); then
  echo "-- uploading the new hatamoto binary (the running service is untouched)"
  DEPLOYED_API=1
  run tree_make -o build-api upload-api
fi
if (( MIG )); then
  echo "-- applying migrations with the new binary: hatamoto migrate (env file sourced)"
  MIG_RAN=1
  run ssh "$DEPLOY_HOST" "cd $REMOTE_DIR && set -a && . config/hatamoto.env && set +a && bin/hatamoto migrate"
fi
if (( API )); then
  echo "-- installing the unit file (daemon-reload) and restarting hatamoto-api"
  run tree_make -o build-api install-unit restart-api
fi
(( API || MIG )) || echo "nothing to deploy"

# ---------------------------------------------------------------- health check
step 6 "health"
if (( API || MIG )); then
  if (( DRY )); then
    echo "DRY: health check x10 (systemctl is-active hatamoto-api; curl $SITE_URL/health, / and /media (newest upload, else /media/ non-5xx); journal scan since deploy restart for panic|FATAL)"
    echo "DRY: on success -> write $DEPLOY_SHA to $REMOTE_DIR/DEPLOYED_SHA"
    echo "DRY: on failure -> rollback (deploy=$DEPLOYED_API migrations=$MIG_RAN), re-check, exit 2 (3 if still unhealthy)"
  else
    health "$DEPLOY_SINCE" || false   # ERR trap -> on_fail
    trap - ERR INT TERM HUP
    echo "healthy"
    if ! ssh "$DEPLOY_HOST" "echo $DEPLOY_SHA > $REMOTE_DIR/DEPLOYED_SHA"; then
      echo "WARNING: could not write $REMOTE_DIR/DEPLOYED_SHA (next ship does a full deploy)" >&2
    fi
  fi
else
  echo "skipped (no deploy)"
fi

DEP=none; (( DEPLOYED_API )) && DEP=binary
MIGS=no; (( MIG )) && MIGS=yes
echo
if (( DRY )); then
  echo "SHIP-H DRY-RUN OK pr=$PR sha=$DEPLOY_SHA merge=$MERGE_SHA"
else
  echo "SHIP-H OK pr=$PR sha=$DEPLOY_SHA merge=$MERGE_SHA deployed=$DEP migrations=$MIGS"
fi
