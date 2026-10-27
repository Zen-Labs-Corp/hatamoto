#!/usr/bin/env bash
# test-ship-dryrun.sh - runs `scripts/ship.sh --dry-run` against stubbed gh/ssh
# and checks that the single-binary deploy steps are shown: migrations via the
# new binary before the restart, pre-ship copies of binary and unit, unit
# install with daemon-reload, and no goose CLI anywhere.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
bash -n scripts/ship.sh

STUBS=$(mktemp -d); trap 'rm -rf "$STUBS"' EXIT
# ship.sh refuses a clone whose origin is not the core repository; these runs
# expect whatever this clone's origin is, and the gh stubs resolve it unchanged.
slug_of() { local u=${1%/}; u=${u%.git}; u=${u//:/\/}; awk -F/ 'NF >= 2 {print $(NF-1) "/" $NF}' <<< "$u"; }
export SHIP_EXPECTED_ORIGIN STUB_NWO
SHIP_EXPECTED_ORIGIN=$(slug_of "$(git remote get-url origin)")
STUB_NWO=$SHIP_EXPECTED_ORIGIN
cat > "$STUBS/gh" <<'GH'
#!/usr/bin/env bash
# gh {pr|repo} view ... --json <field> --jq <expr>
field=""; prev=""
for a in "$@"; do [[ "$prev" == --json ]] && field=$a; prev=$a; done
case "$field" in
  state) echo MERGED ;;
  mergeable) echo MERGEABLE ;;
  mergeStateStatus) echo CLEAN ;;
  headRefName) echo feat/x ;;
  baseRefName) echo main ;;
  mergeCommit) echo "" ;;
  defaultBranchRef) echo main ;;
  nameWithOwner) echo "$STUB_NWO" ;;
  *) echo main ;;
esac
GH
printf '#!/usr/bin/env bash\nexit 0\n' > "$STUBS/ssh"
chmod +x "$STUBS/gh" "$STUBS/ssh"

# No origin/main in a fresh clone? fall back to HEAD for the deploy sha.
if ! git rev-parse -q --verify origin/main >/dev/null; then git update-ref refs/remotes/origin/main HEAD; fi

OUT=$(PATH="$STUBS:$PATH" DEPLOY_HOST=stub SHIP_REEXEC=1 bash scripts/ship.sh --dry-run 1 2>&1) || { echo "$OUT"; echo "FAIL: dry-run exited non-zero"; exit 1; }

must() { grep -qF -- "$1" <<< "$OUT" || { echo "$OUT"; echo "FAIL: dry-run output lacks: $1"; exit 1; }; }
must "make"   # tree_make is printed as DRY: env ... make
must "stamping version"
must "upload-api"
must "bin/hatamoto migrate"
must "install-unit restart-api"
must "hatamoto-api.service.pre-ship"
must "for b in hatamoto hatamoto-api"
must "check-unit"
must "/media"
must "SHIP-H DRY-RUN OK"
if grep -qi "goose " <<< "$(grep '^DRY:' <<< "$OUT")"; then echo "FAIL: dry-run still invokes the goose CLI"; exit 1; fi
# migrations must run after the upload and before the unit install/restart
up=$(grep -n "upload-api" <<< "$OUT" | head -1 | cut -d: -f1)
mg=$(grep -n "bin/hatamoto migrate" <<< "$OUT" | head -1 | cut -d: -f1)
ins=$(grep -n "install-unit restart-api" <<< "$OUT" | head -1 | cut -d: -f1)
(( up < mg && mg < ins )) || { echo "FAIL: step order upload($up) < migrate($mg) < install+restart($ins)"; exit 1; }
echo "ship dry-run ok"

# --- origin assertion: a clone of another repository, or one GitHub resolves to
# another name (a renamed repository), stops before any PR is read or merged.
dry() { PATH="$STUBS:$PATH" DEPLOY_HOST=stub SHIP_REEXEC=1 bash scripts/ship.sh --dry-run 1 2>&1; }
if O=$(SHIP_EXPECTED_ORIGIN="Zen-Labs-Corp/hatamoto" dry); then echo "$O"; echo "FAIL: ship accepted an unexpected origin"; exit 1; fi
grep -q "SHIP-H ABORT: origin is" <<< "$O" || { echo "$O"; echo "FAIL: wrong origin not reported"; exit 1; }
grep -q "== step" <<< "$O" && { echo "$O"; echo "FAIL: wrong origin reached step 1"; exit 1; }
if O=$(STUB_NWO=owner/renamed dry); then echo "$O"; echo "FAIL: ship accepted an origin that resolves to another repository"; exit 1; fi
grep -q "resolves to owner/renamed" <<< "$O" || { echo "$O"; echo "FAIL: renamed origin not reported"; exit 1; }
O=$(SHIP_EXPECTED_ORIGIN="other/x ${SHIP_EXPECTED_ORIGIN^^}" dry) || { echo "$O"; echo "FAIL: case-insensitive list match refused"; exit 1; }
[[ "$(slug_of git@github.com:Zen-Labs-Corp/hatamoto.git) $(slug_of https://github.com/example/hatamoto) $(slug_of ssh://git@github.com/a/b.git/)" == "Zen-Labs-Corp/hatamoto example/hatamoto a/b" ]] \
  || { echo "FAIL: slug parsing"; exit 1; }
grep -q "SHIP_EXPECTED_ORIGIN:-Zen-Labs-Corp/hatamoto zenlabscorp/hatamoto}" scripts/ship.sh || { echo "FAIL: default expected origin changed"; exit 1; }
echo "ship origin assertion ok"

# --- OPEN PR: ship refuses failing, pending or missing admin-ui checks and
# only reaches the (dry) merge when everything passed and admin-ui is SUCCESS.
CK=$(mktemp -d); trap 'rm -rf "$STUBS" "$CK"' EXIT
command -v jq >/dev/null || { echo "FAIL: jq is required for the gh stub"; exit 1; }
cat > "$CK/gh" <<'GH'
#!/usr/bin/env bash
# A strict gh 2.46 stand-in: unknown subcommands and flags fail like the real one
# ("gh pr checks" has no --json). --jq is applied with jq to the JSON below.
# The rollup depends on CHECKS_MODE.
[[ "$1 $2" == "pr checks" ]] && { for a in "$@"; do [[ "$a" == --json ]] && { echo "unknown flag: --json" >&2; exit 1; }; done; }
[[ "$1 $2" == "repo view" && " $* " == *" nameWithOwner "* ]] && { echo "$STUB_NWO"; exit 0; }
[[ "$1 $2" == "repo view" ]] && { echo main; exit 0; }
if [[ "$1 $2" == "workflow list" ]]; then
  # Only `--all --json name,state` (optionally --jq) is accepted.
  jqx=""; seen=""
  for ((i = 3; i <= $#; i++)); do
    case "${!i}" in
      --all) seen="$seen all" ;;
      --json) i=$((i + 1)); [[ "${!i}" == name,state ]] || { echo "unexpected --json ${!i}" >&2; exit 1; }; seen="$seen json" ;;
      --jq) i=$((i + 1)); jqx=${!i} ;;
      *) echo "unknown flag: ${!i}" >&2; exit 1 ;;
    esac
  done
  [[ "$seen" == " all json" || "$seen" == " json all" ]] || { echo "need --all --json name,state" >&2; exit 1; }
  [[ "$CI_STATE" == wfError ]] && { echo "HTTP 502: bad gateway" >&2; exit 1; }
  json="[{\"name\":\"channels\",\"state\":\"active\"},{\"name\":\"ci\",\"state\":\"$CI_STATE\"}]"
  if [[ -n "$jqx" ]]; then jq -r "$jqx" <<< "$json"; else echo "$json"; fi
  exit 0
fi
if [[ "$1 $2" == "pr merge" ]]; then
  # The merge must be pinned to the checked head commit.
  [[ " $* " == *" --match-head-commit 0123456789abcdef0123456789abcdef01234567 "* ]] || { echo "pr merge without --match-head-commit <sha>" >&2; exit 1; }
  exit 0
fi
[[ "$1" == pr && "$2" == view ]] || { echo "unexpected gh call: $*" >&2; exit 1; }
field=""; jqx=""; i=0; args=("$@")
for ((i = 3; i < ${#args[@]}; i++)); do
  case "${args[i]}" in
    --json) field=${args[++i]} ;;
    --jq) jqx=${args[++i]} ;;
    [0-9]*) ;;
    *) echo "unknown flag: ${args[i]}" >&2; exit 1 ;;
  esac
done
case "$field" in
  state) json='{"state":"OPEN"}' ;;
  mergeable) json='{"mergeable":"MERGEABLE"}' ;;
  mergeStateStatus) json='{"mergeStateStatus":"CLEAN"}' ;;
  headRefName) json='{"headRefName":"feat/x"}' ;;
  baseRefName) json='{"baseRefName":"main"}' ;;
  mergeCommit) json='{"mergeCommit":null}' ;;
  headRefOid) json='{"headRefOid":"0123456789abcdef0123456789abcdef01234567"}' ;;
  isCrossRepository) json='{"isCrossRepository":false}' ;;
  statusCheckRollup)
    ok='{"__typename":"CheckRun","name":"backend","status":"COMPLETED","conclusion":"SUCCESS"}'
    ui='{"__typename":"CheckRun","name":"admin-ui","status":"COMPLETED","conclusion":"SUCCESS"}'
    skip='{"__typename":"CheckRun","name":"deploy-docs","status":"COMPLETED","conclusion":"SKIPPED"}'
    ctx='{"__typename":"StatusContext","context":"ci/legacy","state":"SUCCESS"}'
    case "$CHECKS_MODE" in
      pass)       r="$ok,$ui,$skip,$ctx" ;;
      fail)       r="${ok/SUCCESS/FAILURE},$ui" ;;
      pending)    r="$ok,${ui/COMPLETED/IN_PROGRESS}" ;;
      missing)    r="$ok" ;;
      uiskipped)  r="$ok,${ui/SUCCESS/SKIPPED}" ;;
      ctxfail)    r="$ok,$ui,${ctx/SUCCESS/FAILURE}" ;;
      ghError)    echo "HTTP 502: bad gateway" >&2; exit 1 ;;
    esac
    json="{\"statusCheckRollup\":[$r]}" ;;
  *) json='{}' ;;
esac
if [[ -n "$jqx" ]]; then jq -r "$jqx" <<< "$json"; else echo "$json"; fi
GH
cp "$STUBS/ssh" "$CK/ssh"; chmod +x "$CK/gh" "$CK/ssh"
open_pr() { PATH="$CK:$PATH" CI_STATE=${CI_STATE:-active} CHECKS_MODE=$1 DEPLOY_HOST=stub SHIP_REEXEC=1 SHIP_MERGEABLE_WAIT=0 bash scripts/ship.sh --dry-run 1 2>&1; }
if O=$(open_pr pass); then
  grep -qF "DRY: gh pr merge 1 --merge --match-head-commit 0123456789abcdef0123456789abcdef01234567" <<< "$O" || { echo "$O"; echo "FAIL: passing checks did not reach the pinned merge"; exit 1; }
else echo "$O"; echo "FAIL: passing checks were refused"; exit 1; fi
for mode in fail pending missing uiskipped ctxfail ghError; do
  if O=$(open_pr $mode); then echo "$O"; echo "FAIL: ship accepted checks mode=$mode"; exit 1; fi
  grep -q "SHIP-H ABORT" <<< "$O" || { echo "$O"; echo "FAIL: mode=$mode did not abort with SHIP-H ABORT"; exit 1; }
  if grep -qF "gh pr merge" <<< "$O"; then echo "$O"; echo "FAIL: mode=$mode reached the merge"; exit 1; fi
done
# the full suite runs before the merge in CI mode too: named in the dry run, ahead of the merge line
O=$(open_pr pass)
fs=$(grep -nF "DRY: would run full suite on 0123456789abcdef0123456789abcdef01234567 (not cached)" <<< "$O" | head -1 | cut -d: -f1)
mg=$(grep -nF "DRY: gh pr merge" <<< "$O" | head -1 | cut -d: -f1)
[[ -n $fs && -n $mg ]] && (( fs < mg )) || { echo "$O"; echo "FAIL: the full-suite step is not before the merge in CI mode ($fs, $mg)"; exit 1; }
grep -qF "gate mode: CI" <<< "$(open_pr pass)" || { echo "FAIL: ci active did not use the CI gate"; exit 1; }
echo "ship checks gate ok"

# --- 'ci' workflow disabled: the local UI gate replaces the status checks. The
# dry run names the head sha, runs nothing, merges only in the dry-run sense, and
# does not need the rollup (ghError would abort in CI mode).
for st in disabled_manually disabled_inactivity; do
  O=$(CI_STATE=$st open_pr ghError) || { echo "$O"; echo "FAIL: ci=$st dry run aborted"; exit 1; }
  grep -qF "gate mode: LOCAL UI gate" <<< "$O" || { echo "$O"; echo "FAIL: ci=$st did not pick the local gate"; exit 1; }
  grep -qF "DRY: would run local UI gate on 0123456789abcdef0123456789abcdef01234567" <<< "$O" || { echo "$O"; echo "FAIL: ci=$st lacks the local gate line"; exit 1; }
  grep -qF "DRY: gh pr merge 1 --merge --match-head-commit 0123456789abcdef0123456789abcdef01234567" <<< "$O" || { echo "$O"; echo "FAIL: ci=$st did not reach the pinned merge"; exit 1; }
  fs=$(grep -nF "DRY: would run full suite on 0123456789abcdef0123456789abcdef01234567" <<< "$O" | head -1 | cut -d: -f1)
  mg=$(grep -nF "DRY: gh pr merge" <<< "$O" | head -1 | cut -d: -f1)
  [[ -n $fs && -n $mg ]] && (( fs < mg )) || { echo "$O"; echo "FAIL: ci=$st: the full-suite step is not before the merge"; exit 1; }
done
# gh cannot list workflows: abort before any merge
if O=$(CI_STATE=wfError open_pr pass); then echo "$O"; echo "FAIL: ship accepted a workflow list error"; exit 1; fi
grep -q "SHIP-H ABORT" <<< "$O" && ! grep -qF "gh pr merge" <<< "$O" || { echo "$O"; echo "FAIL: workflow list error did not abort cleanly"; exit 1; }
echo "ship local gate ok"

# --- every repo file comes from the deploy-SHA tree: the invoking (main)
# checkout is one commit behind and lacks the preflight bootstrap SQL.
(
  W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
  git init -q --bare "$W/origin.git"
  git init -q -b main "$W/main"
  cd "$W/main"
  git config user.email t@t; git config user.name t
  mkdir scripts deploy deploy/sql
  cp "$OLDPWD/scripts/ship.sh" scripts/ship.sh
  printf '#!/bin/sh\n' > scripts/ship-db-restore.sh
  printf 'all:\n\t@true\n' > Makefile
  git add -A && git commit -qm behind
  git remote add origin "$W/origin.git"; git push -q origin main
  echo '-- bootstrap at the deploy sha' > deploy/sql/role-admin-bootstrap.sql
  git add -A && git commit -qm ahead && git push -q origin main
  DEPLOY=$(git rev-parse HEAD)
  git reset -q --hard HEAD~1          # the main checkout is behind origin/main
  [[ ! -e deploy/sql/role-admin-bootstrap.sql ]] || { echo "FAIL: setup: file present in the behind checkout"; exit 1; }
  mkdir "$W/bin"
  cat > "$W/bin/gh" <<GH
#!/usr/bin/env bash
field=""; prev=""
for a in "\$@"; do [[ "\$prev" == --json ]] && field=\$a; prev=\$a; done
case "\$field" in
  state) echo MERGED ;; mergeable) echo MERGEABLE ;; mergeStateStatus) echo CLEAN ;;
  headRefName) echo feat/x ;; mergeCommit) echo $DEPLOY ;; nameWithOwner) echo "\$STUB_NWO" ;; *) echo main ;;
esac
GH
  cat > "$W/bin/ssh" <<'SSH'
#!/usr/bin/env bash
case "$*" in
  *"-f -"*) cat > "$SHIP_TEST_OUT/bootstrap.sql" ;;
  *pg_dump*) exit 1 ;;   # stop the ship after the preflight
esac
exit 0
SSH
  chmod +x "$W/bin/gh" "$W/bin/ssh"
  mkdir "$W/out"
  # SHIP_REEXEC=1: this run drives the ship.sh under test; the deploy-sha copy is identical.
  SHIP_EXPECTED_ORIGIN=$(slug_of "$W/origin.git") STUB_NWO=$(slug_of "$W/origin.git") \
  PATH="$W/bin:$PATH" SHIP_TEST_OUT="$W/out" DEPLOY_HOST=stub SHIP_REEXEC=1 SHIP_MERGEABLE_WAIT=0 \
    bash scripts/ship.sh 1 > "$W/log" 2>&1 || true
  grep -q 'preflight' "$W/log" || { cat "$W/log"; echo "FAIL: ship never reached the preflight"; exit 1; }
  grep -q 'cannot run deploy/sql/role-admin-bootstrap.sql' "$W/log" && { cat "$W/log"; echo "FAIL: preflight aborted: bootstrap SQL read from the invoking checkout"; exit 1; }
  grep -qx -- '-- bootstrap at the deploy sha' "$W/out/bootstrap.sql" 2>/dev/null \
    || { cat "$W/log"; echo "FAIL: preflight did not send the deploy-sha bootstrap SQL"; exit 1; }
)
# --- the full-suite merge gate (open PR, real run against a scratch clone and a stub snapshot hook):
# no record: the hook runs in full mode and a red run stops before the merge; a record: `cached`, no hook run;
# a stale snapshot: abort; a pass that recorded nothing (a skipped gate): abort.
(
  W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
  git init -q --bare "$W/origin.git"
  git init -q -b main "$W/main"
  cd "$W/main"
  git config user.email t@t; git config user.name t
  mkdir scripts; cp "$OLDPWD/scripts/ship.sh" scripts/ship.sh
  cp "$OLDPWD/scripts/affected-readers.txt" "$OLDPWD/scripts/check-suites.txt" scripts/
  printf 'all:\n\t@true\n' > Makefile
  git add -A && git commit -qm base
  git remote add origin "$W/origin.git"; git push -q origin main
  echo change > change.txt; git add -A && git commit -qm change
  SHA=$(git rev-parse HEAD); TREE=$(git rev-parse HEAD^{tree})
  git push -q origin HEAD:refs/pull/1/head
  git fetch -q origin
  SNAP=$(git rev-parse --git-common-dir)/hatamoto-hooks/snapshot/.githooks
  mkdir -p "$SNAP/lib" "$W/bin"
  cp "$OLDPWD/.githooks/lib/farm.sh" "$SNAP/lib/farm.sh"
  # stub snapshot hook: api 3; a full run records the tree unless HOOK_MODE says otherwise
  cat > "$SNAP/pre-push" <<HOOK
#!/usr/bin/env bash
if [[ \${1:-} == --hook-api ]]; then
  [[ -z \${HOOK_OLD:-} ]] || { read -r -t 15 _; exit 0; }   # a pre-change hook: reads stdin, knows no --hook-api
  echo \${HOOK_API:-3}; exit 0
fi
read -r _ lsha _ || true
cat >/dev/null
T=\$(git rev-parse "\$lsha^{tree}" 2>/dev/null || echo $TREE)
echo "hook ran full=\${HATAMOTO_PREPUSH_FULL:-} held=\${HATAMOTO_FARM_SLOT_HELD:-} skip=\${HATAMOTO_SKIP_INTEGRATION:-unset} suites=\${HATAMOTO_CHECK_SUITES:-unset} flakes=\${HATAMOTO_FLAKES_FILE:-unset} cache=\${HATAMOTO_GATE_CACHE:-unset} since=\${HATAMOTO_PREPUSH_SINCE:-unset}" >>"$W/hook.log"
env | grep -E '^(GH_TOKEN|GITHUB_TOKEN|DEPLOY_HOST|HATAMOTO_POST_SHIP_HOOK|SSH_AUTH_SOCK|PGPASSWORD|DATABASE_URL|AWS_[A-Z_]*|[A-Z_]*_(TOKEN|SECRET|PASSWORD|API_KEY|KEY_ID|ACCESS_KEY|DSN|CREDENTIALS))=' >>"$W/hook.env" || true
case \${HOOK_MODE:-green} in
  red) exit 1 ;;
  green) [[ \${HATAMOTO_PREPUSH_FULL:-} == 1 ]] || exit 0   # an affected run records nothing
         mkdir -p "$(git rev-parse --git-common-dir)/hatamoto-gate-cache/\$T" && date >"$(git rev-parse --git-common-dir)/hatamoto-gate-cache/\$T/full-suite" ;;
esac
HOOK
  chmod +x "$SNAP/pre-push"
  cat > "$W/bin/gh" <<GH
#!/usr/bin/env bash
field=""; prev=""
for a in "\$@"; do [[ "\$prev" == --json ]] && field=\$a; prev=\$a; done
[[ "\$1 \$2" != "pr merge" ]] || { echo merged >"$W/merged"; exit 1; }
[[ "\$1 \$2" != "workflow list" ]] || { echo disabled_manually; exit 0; }
case "\$field" in
  isCrossRepository) echo "\${STUB_CROSS:-false}" ;; headRepositoryOwner) echo someone ;;
  state) echo OPEN ;; mergeable) echo MERGEABLE ;; mergeStateStatus) echo CLEAN ;;
  headRefName) echo feat/x ;; baseRefName) echo main ;; headRefOid) echo \${STUB_SHA:-$SHA} ;;
  defaultBranchRef) echo main ;; nameWithOwner) echo "\$STUB_NWO" ;; *) echo main ;;
esac
GH
  chmod +x "$W/bin/gh"
  ship() { SHIP_EXPECTED_ORIGIN=$(slug_of "$W/origin.git") STUB_NWO=$(slug_of "$W/origin.git") HATAMOTO_FARM_DIR="$W/farm" \
    PATH="$W/bin:$PATH" DEPLOY_HOST=stub SHIP_REEXEC=1 SHIP_MERGEABLE_WAIT=0 "$@" bash scripts/ship.sh --no-deploy 1 > "$W/log" 2>&1; }
  fail() { cat "$W/log"; echo "FAIL: full-suite gate: $1"; exit 1; }
  KEY=$(git rev-parse --git-common-dir)/hatamoto-gate-cache/$TREE/full-suite
  rm -f "$KEY" "$W/hook.log" "$W/merged"
  # A4: red
  ! ship env HOOK_MODE=red || fail "a red full suite did not stop ship"
  [[ ! -e $W/merged ]] && grep -q 'full=1 held=1 skip=unset' "$W/hook.log" && grep -q 'the full suite failed' "$W/log" || fail "red run: merge attempted or hook called wrongly"
  # skipped gate: passes but records nothing
  ! ship env HOOK_MODE=silent || fail "a pass that recorded nothing was accepted"
  grep -q 'without recording itself' "$W/log" && [[ ! -e $W/merged ]] || fail "unrecorded pass reached the merge"
  # stale snapshot
  ! ship env HOOK_API=1 || fail "a stale snapshot was used"
  grep -q 'run make hooks' "$W/log" && [[ ! -e $W/merged ]] || fail "stale snapshot not refused cleanly"
  # api 2 predates HATAMOTO_PREPUSH_SINCE (it would check zero commits of a fetched head): refused too
  ! ship env HOOK_API=2 || fail "an api-2 snapshot was used"
  grep -q 'run make hooks' "$W/log" && [[ ! -e $W/merged ]] || fail "api-2 snapshot not refused"
  # the knobs that weaken a "full" run never reach the hook
  rm -f "$W/hook.log" "$W/merged"
  ! ship env HOOK_MODE=silent HATAMOTO_CHECK_SUITES=/x HATAMOTO_FLAKES_FILE=/y HATAMOTO_SKIP_INTEGRATION=1 || fail "silent hook accepted"
  grep -q 'skip=unset suites=unset flakes=unset cache=unset since=origin/main' "$W/hook.log" || fail "an env knob reached the full run: $(cat "$W/hook.log")"
  # the run is scrubbed of credentials; the build-host offload keeps working without the agent
  rm -f "$W/hook.log" "$W/hook.env" "$W/merged"
  ! ship env HOOK_MODE=silent GH_TOKEN=a GITHUB_TOKEN=b MY_API_TOKEN=c DB_PASSWORD=d APP_SECRET=e SSH_AUTH_SOCK=/s HATAMOTO_POST_SHIP_HOOK=/h || fail "silent hook accepted"
  [[ -f $W/hook.log && ! -s $W/hook.env ]] || fail "a secret reached the full run: $(cat "$W/hook.env")"
  rm -f "$W/hook.log" "$W/hook.env" "$W/merged"
  ! ship env HOOK_MODE=silent AWS_SECRET_ACCESS_KEY=a AWS_PROFILE=b STRIPE_API_KEY=c AWS_ACCESS_KEY_ID=d SIGNING_KEY_ID=e S3_ACCESS_KEY=f PGPASSWORD=g DATABASE_URL=h SENTRY_DSN=i GCP_CREDENTIALS=j || fail "silent hook accepted"
  [[ -f $W/hook.log && ! -s $W/hook.env ]] || fail "a secret reached the full run: $(cat "$W/hook.env")"
  # a PR from another repository is refused before anything runs or merges, with the way forward
  rm -f "$W/hook.log" "$W/merged" "$KEY"
  ! ship env STUB_CROSS=true HOOK_MODE=green || fail "a cross-repository PR was shipped"
  grep -q 'comes from another repository (someone)' "$W/log" && grep -q 'git push --no-verify origin FETCH_HEAD:refs/heads/review/pr-1' "$W/log" && grep -q 'would run the fork.s tests on this machine through the pre-push' "$W/log" \
    && [[ ! -e $W/hook.log && ! -e $W/merged && ! -e $KEY ]] || fail "cross-repository PR: no clean refusal"
  # an old hook that reads stdin must not hang the probe, even with stdin left open
  t0=$SECONDS; ! ship env HOOK_OLD=1 < <(sleep 20) || fail "an old hook was accepted"
  (( SECONDS - t0 < 10 )) && grep -q 'run make hooks' "$W/log" && [[ ! -e $W/merged ]] || fail "the --hook-api probe waited on stdin ($((SECONDS - t0)) s)"
  # green: the merge is reached (the stub merge fails so ship stops there)
  rm -f "$W/hook.log"; ! ship env HOOK_MODE=green || fail "stub merge should have stopped ship"
  grep -q 'hook ran full=1 held=1 .* since=origin/main' "$W/hook.log" || fail "the full run was not given HATAMOTO_PREPUSH_SINCE=origin/main: $(cat "$W/hook.log")"
  [[ -f $KEY && -e $W/merged ]] && grep -q 'full suite passed' "$W/log" || fail "green run did not reach the merge with a record"
  # A5: recorded -> cached, the hook is not run again (nor asked for a pass)
  rm -f "$W/hook.log" "$W/merged"; ! ship env HOOK_MODE=red || fail "stub merge should have stopped ship"
  grep -q 'full suite on .* cached' "$W/log" && [[ ! -e $W/hook.log && -e $W/merged ]] || fail "a recorded pass was not reused"
  # HATAMOTO_GATE_CACHE=off ignores the record
  rm -f "$W/hook.log" "$W/merged"; ! ship env HOOK_MODE=red HATAMOTO_GATE_CACHE=off || fail "cache=off should rerun"
  [[ -e $W/hook.log && ! -e $W/merged ]] || fail "HATAMOTO_GATE_CACHE=off did not re-run the suite"
  # plans/docs-only PRs skip the full suite and still merge; anything a test or suite may read, or any
  # doubt (symlink, no snapshot tables), keeps it
  pr_with() { # <file>...: a PR head (from main) adding those files; prints its sha
    git checkout -q -B prx origin/main; local f
    for f in "$@"; do
      mkdir -p "$(dirname "$f")"
      if [[ $f == link.md ]]; then ln -s change.txt "$f"; else echo "$RANDOM" > "$f"; fi
    done
    git add -A && git commit -qm pr && git push -q -f origin HEAD:refs/pull/1/head && git fetch -q origin && git rev-parse HEAD
  }
  docs_case() { # <want skipped|ran> <file>...
    local want=$1; shift; local sha; sha=$(pr_with "$@")
    rm -f "$W/hook.log" "$W/merged"
    ! ship env STUB_SHA="$sha" HOOK_MODE=green || fail "stub merge should have stopped ship ($*)"
    [[ -e $W/merged ]] || fail "docs case ($*): never reached the merge"
    if [[ $want == skipped ]]; then
      grep -qE "full suite on ${sha:0:12}: skipped \(plans/docs only: $# files\); content checks: ok" "$W/log" || fail "docs case ($*): not skipped"
      # the content checks ran: the same hook, affected mode (no full flag), farm slot held, scrubbed knobs, no record
      grep -q 'hook ran full= held=1 skip=unset suites=unset flakes=unset cache=unset since=origin/main' "$W/hook.log" && [[ $(wc -l <"$W/hook.log") == 1 ]] \
        && [[ ! -e $(git rev-parse --git-common-dir)/hatamoto-gate-cache/$(git rev-parse "$sha^{tree}")/full-suite ]] || fail "docs case ($*): content checks did not run in affected mode: $(cat "$W/hook.log" 2>&1)"
    else
      ! grep -q 'skipped (plans/docs only' "$W/log" && [[ -e $W/hook.log ]] || fail "docs case ($*): the full suite did not run"
    fi
  }
  docs_case skipped .pi/notes/x/plan.md
  # a red content check aborts before the merge; the dry run names the step
  sha=$(pr_with docs/notes.md); rm -f "$W/hook.log" "$W/merged"
  ! ship env STUB_SHA="$sha" HOOK_MODE=red HATAMOTO_CHECK_SUITES=/x HATAMOTO_SKIP_INTEGRATION=1 || fail "a red content check did not stop a docs-only ship"
  [[ ! -e $W/merged ]] && grep -q 'the content checks failed' "$W/log" && ! grep -q 'content checks: ok' "$W/log" && grep -q 'hook ran full= held=1 skip=unset suites=unset' "$W/hook.log" || fail "docs-only red content check: merge attempted or knobs reached the hook"
  # fail closed: a stale snapshot hook aborts a docs-only ship too
  ! ship env STUB_SHA="$sha" HOOK_API=1 || fail "a docs-only ship used a stale hook"
  [[ ! -e $W/merged ]] && grep -q 'run make hooks' "$W/log" || fail "docs-only stale hook not refused"
  rm -f "$W/merged"
  SHIP_EXPECTED_ORIGIN=$(slug_of "$W/origin.git") STUB_NWO=$(slug_of "$W/origin.git") PATH="$W/bin:$PATH" STUB_SHA="$sha" DEPLOY_HOST=stub SHIP_REEXEC=1 SHIP_MERGEABLE_WAIT=0 \
    bash scripts/ship.sh --dry-run --no-deploy 1 > "$W/log" 2>&1 || true
  cl=$(grep -nF "DRY: would run the content checks on $sha" "$W/log" | head -1 | cut -d: -f1); mg=$(grep -nF "DRY: gh pr merge" "$W/log" | head -1 | cut -d: -f1)
  [[ -n $cl && -n $mg ]] && (( cl < mg )) || fail "dry run: content-check line missing or not before the merge ($cl, $mg)"
  docs_case skipped docs/notes.md .pi/notes/y.md
  docs_case ran docs/ADMIN_UI_RULES.md
  docs_case ran docs/UPGRADES.md docs/notes.md
  docs_case ran README.md
  docs_case ran docs/site/a.md
  docs_case ran backend/internal/x/readme.md
  docs_case ran scripts/readme.md
  docs_case ran docs/notes.md one.go
  docs_case ran link.md
  docs_case ran notes.txt
  # the tables come from origin/main, not the hooks snapshot: a row merged to main after the snapshot
  # was taken (the snapshot has no tables at all here) keeps the suite, and an unreadable table does too
  # a docs PR branched before origin/main moved would be checked with stale rules: the full suite runs
  git checkout -q -B oldbase origin/main; old=$(git rev-parse HEAD)
  git checkout -q -B mainx origin/main; echo moved >moved.txt; git add -A; git commit -qm 'main moves on' && git push -q origin HEAD:main
  git checkout -q -B prold "$old"; mkdir -p docs; echo "$RANDOM" >docs/stale.md; git add -A; git commit -qm 'stale docs pr'
  git push -q -f origin HEAD:refs/pull/1/head; git fetch -q origin; sha=$(git rev-parse HEAD); rm -f "$W/hook.log" "$W/merged"
  ! ship env STUB_SHA="$sha" HOOK_MODE=green || fail "stub merge should have stopped ship (stale docs PR)"
  ! grep -q 'skipped (plans/docs only' "$W/log" && grep -q 'running the full pre-push suite' "$W/log" && grep -q 'hook ran full=1' "$W/hook.log" \
    || fail "a docs PR behind origin/main skipped the full suite"
  git checkout -q -B mainx origin/main
  echo 'docs/newrow.md  backend  ./internal/x' >> scripts/affected-readers.txt
  git commit -qam 'reader row' && git push -q origin HEAD:main
  docs_case ran docs/newrow.md
  docs_case skipped docs/notes.md
  git checkout -q -B mainx origin/main
  git rm -q scripts/check-suites.txt && git commit -qm 'drop table' && git push -q origin HEAD:main
  docs_case ran docs/notes.md
  git checkout -q main
)
echo "ship full-suite gate ok"
# ship.sh may read only .deploy-host from the invoking checkout
if grep -n '\$REPO' scripts/ship.sh | grep -vE 'cd "\$REPO"|\.deploy-host|worktree remove'; then
  echo "FAIL: ship.sh reads a repo file from \$REPO instead of the deploy-sha tree"; exit 1
fi
echo "ship deploy-sha tree ok"

# --- deploy/install-unit.sh against a stubbed systemctl
T=$(mktemp -d); trap 'rm -rf "$STUBS" "$T"' EXIT
mkdir "$T/bin"
cat > "$T/bin/systemctl" <<'SC'
#!/bin/sh
case "$*" in
  "show -p User --value hatamoto-api") echo svcuser ;;
  "show -p Group --value hatamoto-api") echo svcuser ;;
  "show -p FragmentPath --value hatamoto-api") echo "$UNIT" ;;
  "show -p DropInPaths --value hatamoto-api") echo "$DROPINS" ;;
  "daemon-reload") echo reloaded >> "$UNIT.log" ;;
esac
SC
chmod +x "$T/bin/systemctl"
inst() { cp deploy/systemd/hatamoto-api.service "$T/box.service.new"; PATH="$T/bin:$PATH" UNIT="$T/box.service" "$@" sh deploy/install-unit.sh; }
rm -f "$T/box.service.log"
# clean box: keeps User/Group, installs, reloads
printf '[Service]\nUser=svcuser\nGroup=svcuser\nExecStart=/old\n' > "$T/box.service"
DROPINS="" inst env >/dev/null
grep -qx 'User=svcuser' "$T/box.service" && grep -qx 'Group=svcuser' "$T/box.service" && grep -qx 'Restart=always' "$T/box.service" || { echo "FAIL: install-unit did not keep User/Group or install"; exit 1; }
grep -qx reloaded "$T/box.service.log" || { echo "FAIL: no daemon-reload"; exit 1; }
# Environment= on the box that the tracked unit lacks: refuse, nothing changed
printf '[Service]\nUser=svcuser\nEnvironment=FOO=bar\nExecStart=/old\n' > "$T/box.service"; cp "$T/box.service" "$T/before"
if DROPINS="" inst env >/dev/null 2>&1; then echo "FAIL: install-unit accepted a lost Environment= line"; exit 1; fi
cmp -s "$T/box.service" "$T/before" || { echo "FAIL: unit changed despite refusal"; exit 1; }
# drop-ins: refuse; FORCE_UNIT=1 overrides
if DROPINS="/etc/systemd/system/x.d/o.conf" inst env >/dev/null 2>&1; then echo "FAIL: install-unit accepted drop-ins"; exit 1; fi
DROPINS="/etc/systemd/system/x.d/o.conf" inst env FORCE_UNIT=1 >/dev/null || { echo "FAIL: FORCE_UNIT=1 did not override"; exit 1; }
echo "install-unit ok"
