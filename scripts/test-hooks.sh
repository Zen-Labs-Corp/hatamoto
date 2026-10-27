#!/usr/bin/env bash
# test-hooks.sh - proves the local git hooks (.githooks) in a scratch repository:
# each refusal and the pass path, and that pre-push checks the pushed commit in
# a temporary worktree (a dirty working tree changes nothing). Run by `make test-hooks`.
set -uo pipefail
# The outer full suite (ship.sh, pipeline-gate.sh full) exports HATAMOTO_PREPUSH_FULL=1 and
# HATAMOTO_PREPUSH_SINCE=origin/<base>. The pushes below test the hook's own mode and range choice, so they
# must not inherit either; the tests that want them set them inline.
unset HATAMOTO_PREPUSH_FULL HATAMOTO_PREPUSH_SINCE
src=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/test-hooks.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
export TMPDIR=$tmp
export PATH="$PATH:$HOME/go/bin"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=Tester GIT_AUTHOR_EMAIL=tester@example.com GIT_COMMITTER_NAME=Tester GIT_COMMITTER_EMAIL=tester@example.com
export HATAMOTO_SKIP_INTEGRATION=1
export HATAMOTO_GATE_CACHE=off          # every push below is checked for real; the cache has its own section
export HATAMOTO_FARM_DIR=$tmp/farm      # never the real farm slots
unset HATAMOTO_FARM_SLOT_HELD           # a farm holder exports it; the private farm must not inherit the parent's slot
unset HATAMOTO_PREPUSH_SINCE HATAMOTO_PREPUSH_FULL   # pipeline-gate full / ship export them to the real hook; the throwaway repos here must not inherit them

ok()  { echo "ok   $1"; }
bad() { echo "FAIL $1"; echo >>"$tmp/fails"; }   # a file: bad() also runs in subshells
# expect <pass|refuse> <label> <command...>
expect() {
  local want=$1 label=$2; shift 2
  if "$@" >"$tmp/out" 2>&1; then got=pass; else got=refuse; fi
  if [[ $got == "$want" ]]; then ok "$label"; else bad "$label (wanted $want)"; sed 's/^/    | /' "$tmp/out"; fi
}

command -v gitleaks >/dev/null || [[ -x $HOME/go/bin/gitleaks ]] \
  || { echo "test-hooks: gitleaks missing (go install github.com/zricethezav/gitleaks/v8@v8.30.1)" >&2; exit 1; }

sc=$(command -v staticcheck || true); [[ -n $sc || ! -x $HOME/go/bin/staticcheck ]] || sc=$HOME/go/bin/staticcheck
[[ -n $sc ]] && "$sc" -version 2>/dev/null | grep -q "(0.8.2)" \
  || { echo "test-hooks: staticcheck v0.8.2 missing (go install honnef.co/go/tools/cmd/staticcheck@v0.8.2)" >&2; exit 1; }

# `make test-hooks` must run this script (it was once an empty target that reported success).
mk=$(make -n -f "$src/Makefile" test-hooks 2>&1)
grep -q 'scripts/test-hooks.sh' <<<"$mk" \
  && ok "make test-hooks runs scripts/test-hooks.sh" || bad "make test-hooks runs scripts/test-hooks.sh"

# --- scratch repository with a bare remote ---
git init -q --bare "$tmp/remote.git"
r=$tmp/repo
git init -q -b main "$r"
cd "$r"
mkdir -p scripts backend/internal/admin
cp -r "$src/.githooks" .
cp "$src/.githooks/../Makefile" "$tmp/real.mk"
cp "$src"/scripts/{check-neutral.sh,check-dco.sh,go-test-flaky.sh,integration-gate.sh,known-flakes.txt,check-suites.sh,affected-readers.txt} scripts/
printf 'stub * make check\nwebonly backend/web/** true\n' >scripts/check-suites.txt   # the hook runs `make check` through the suites runner
cp "$src/.gitleaks.toml" .
printf 'check:\n\tgrep -q ok state.txt\n' >Makefile
echo bad >state.txt
printf 'module example.com/scratch\n\ngo 1.21\n' >backend/go.mod
printf 'package admin\n\nimport "testing"\n\nfunc TestUILintScratch(t *testing.T) {}\n' >backend/internal/admin/admin_test.go
printf 'package admin\n' >backend/internal/admin/admin.go
git add -A
git commit -q -s -m "chore: scratch base"
git remote add origin "$tmp/remote.git"
git push -q origin main
git fetch -q origin
make -f "$tmp/real.mk" hooks >"$tmp/hooks.out" 2>&1
[[ $(git config core.hooksPath) == "$r/.git/hatamoto-hooks" && -x $r/.git/hatamoto-hooks/snapshot/.githooks/pre-push ]] \
  && ok "make hooks points core.hooksPath at the git common dir, with a snapshot" || { bad "make hooks points core.hooksPath at the git common dir"; cat "$tmp/hooks.out"; }
git checkout -q -b topic

commit() { git commit -q -m "$1"; }

# --- pre-commit ---
printf 'package admin\nfunc  Bad( ){}\n' >backend/internal/admin/fmt.go
git add backend/internal/admin/fmt.go
expect refuse "pre-commit refuses unformatted Go" git commit -q -s -m "feat: x"
gofmt -w backend/internal/admin/fmt.go          # formatted in the working tree, but NOT re-added
expect refuse "pre-commit checks the staged blob, not the working file" git commit -q -s -m "feat: x"
git add backend/internal/admin/fmt.go
expect pass "pre-commit accepts formatted Go" git commit -q -s -m "feat: formatted"

printf 'package admin\nfunc {\n' >backend/internal/admin/syntax.go; git add backend/internal/admin/syntax.go
expect refuse "pre-commit refuses a staged Go file that does not parse" git commit -q -s -m "feat: broken"
git reset -q backend/internal/admin/syntax.go; rm backend/internal/admin/syntax.go

tok="ghp_$(head -c 400 /dev/urandom | base64 | tr -dc A-Za-z0-9 | head -c 36)"
echo "token = \"$tok\"" >notes.txt
git add notes.txt
expect refuse "pre-commit refuses a planted token" git commit -q -s -m "chore: notes"
git reset -q notes.txt; rm notes.txt

# --- commit-msg ---
echo one >a.txt; git add a.txt
expect refuse "commit-msg refuses a missing sign-off" git commit -q -m "feat: unsigned"
expect refuse "commit-msg refuses another person's sign-off" git commit -q -m "feat: other

Signed-off-by: Someone Else <someone@example.com>"
expect refuse "commit-msg refuses a bad subject" git commit -q -s -m "added a thing"
expect pass "commit-msg accepts a scoped, signed commit" git commit -q -s -m "feat(scope)!: good"
echo three >>a.txt
expect pass "commit-msg accepts git commit -v -s (diff after the scissors line)" env GIT_EDITOR='sed -i "1i feat: verbose"' git commit -q -v -s -a
echo four >>a.txt
expect pass "commit-msg honours core.commentChar" env GIT_EDITOR='sed -i "1i feat: semicolon"' git -c core.commentChar=';' commit -q -v -s -a
echo two >>a.txt
expect pass "commit-msg lets a fixup! subject through" git commit -q -a -s -m "fixup! feat(scope)!: good"

git checkout -q -b mergesrc origin/main; echo m >merge-src.txt; git add merge-src.txt; git commit -q -s -m "feat: merge source"
git checkout -q topic; git commit -q -s --allow-empty -m "feat: topic side"
expect pass "commit-msg lets a plain git merge through without a sign-off" git merge -q --no-ff -m "Merge branch mergesrc" mergesrc
git reset -q --hard HEAD~1

# --- pre-push ---
echo ok >state.txt
commit_state() { git add state.txt; git commit -q -s -m "$1"; }
git stash -q -u 2>/dev/null || true
git reset -q --hard origin/main 2>/dev/null
git checkout -q -b pushme
echo worse >state.txt; commit_state "fix: committed state is bad"
echo ok >state.txt                                    # uncommitted fix must not fake a pass
expect refuse "pre-push refuses bad committed state even if the working tree is fixed" git push -q origin pushme
echo ok >state.txt; commit_state "fix: committed state is good"
echo junk >dirty.txt                                  # untracked noise must not matter either
echo "dirty edit" >>a.txt
expect pass "pre-push passes a good commit with a dirty working tree" git push -q origin pushme
# --- gate cache: passes are recorded per tree; the same tree again skips them, another tree does not ---
git checkout -q -b cached
git commit -q -s --allow-empty -m "fix: same tree as pushme"
expect pass "pre-push records passes and reuses them for the same tree" env HATAMOTO_GATE_CACHE=on git push -q origin cached
grep -qE "make-check +cached" "$tmp/out" && ok "a re-push of an already checked tree says cached" || { bad "a re-push of an already checked tree says cached"; cat "$tmp/out"; }
echo worse >state.txt; commit_state "fix: a new tree"
expect refuse "a changed tree is checked again" env HATAMOTO_GATE_CACHE=on git push -q origin cached
echo ok >state.txt; commit_state "fix: good again"
git checkout -q pushme
expect pass "pre-push ignores a branch delete" git push -q origin :pushme
git tag v0 origin/main
expect pass "pre-push ignores a tag with no new commits" git push -q origin v0

# --- integration gate needs Postgres; the skip is explicit ---
git checkout -q -b gochange origin/main
echo ok >state.txt; printf "\n// Changed is new.\nfunc Changed() {}\n" >>backend/internal/admin/admin.go
git add -A; git commit -q -s -m "fix: change a Go package"
expect refuse "pre-push fails when Postgres tools are missing" env -u HATAMOTO_SKIP_INTEGRATION PG_BIN=/nonexistent git push -q origin gochange
expect pass "pre-push passes with HATAMOTO_SKIP_INTEGRATION=1 (printed loudly)" git push -q origin gochange

# --- the must-run names always reach the integration gate, even when the test file lacks them ---
# A stub gate stands in for Postgres: it refuses unless both names arrive. (A guard that dropped the
# names when the test was renamed or moved would let the gate pass without running them.)
git checkout -q -b mustrun origin/main
printf '#!/usr/bin/env bash\n[[ $INTEGRATION_MUST_RUN == *TestHostilePluginJS_NotLinkedWithoutGrant*TestHostilePluginJS_SectionsAndPartialsNeedTheGrant* ]]\n' >scripts/integration-gate.sh
echo ok >state.txt; printf "\n// Mustrun is new.\nfunc Mustrun() {}\n" >>backend/internal/admin/admin.go
git add -A; git commit -q -s -m "fix: change a Go package under a stub gate"
expect pass "pre-push names the must-run tests even when the test file does not hold them" env -u HATAMOTO_SKIP_INTEGRATION git push -q origin mustrun

# --- DCO range: only this push's own commits (rebase / merge of an unsigned main) ---
git checkout -q -b advance origin/main
echo m >main-only.txt; git add main-only.txt; git commit -q --no-verify -m "release: unsigned commit on main"
git push -q --no-verify origin advance:main; git fetch -q origin
git checkout -q -b rebased origin/main~1                # forked before that main commit
echo ok >state.txt; echo r >r.txt; git add -A; git commit -q -s -m "feat: rebased work"
git push -q origin rebased
git rebase -q origin/main
expect pass "pre-push passes a force-push after rebasing onto a main with unsigned commits" git push -q -f origin rebased
git checkout -q -b merged origin/main~1
echo ok >state.txt; echo m2 >m2.txt; git add -A; git commit -q -s -m "feat: merged work"
git push -q origin merged
git merge -q --no-edit --no-verify origin/main
expect pass "pre-push passes a push after merging a main with unsigned commits" git push -q origin merged
echo own >own.txt; git add own.txt; git commit -q --no-verify -m "feat: my own unsigned commit"
expect refuse "pre-push still refuses an unsigned commit of its own" git push -q origin merged

# --- DCO/gitleaks range: every remote-tracking ref counts as published, not only origin ---
# up.git = the main repo (its main has an unsigned commit); fork.git = a fork that lags behind it.
git init -q -b main --bare "$tmp/up.git"; git init -q -b main --bare "$tmp/fork.git"
git push -q --no-verify "$tmp/up.git" origin/main:refs/heads/main
git push -q --no-verify "$tmp/fork.git" origin/main~1:refs/heads/main
git clone -q "$tmp/up.git" "$tmp/fork-clone" && ( cd "$tmp/fork-clone"
  git remote rename origin upstream; git remote add origin "$tmp/fork.git"; git fetch -q origin
  make -f "$tmp/real.mk" hooks >/dev/null 2>&1
  git checkout -q -b topic origin/main; echo ok >state.txt; echo f >f.txt; git add -A; git commit -q -s -m "feat: fork work"
  git push -q origin topic
  git rebase -q upstream/main
  expect pass "fork layout: pre-push passes after rebasing on upstream/main and pushing to origin (the fork)" git push -q -f origin topic )
git clone -q -o up "$tmp/up.git" "$tmp/up-clone" && ( cd "$tmp/up-clone"
  make -f "$tmp/real.mk" hooks >/dev/null 2>&1
  git checkout -q -b nb; echo ok >state.txt; git add -A; git commit -q -s -m "feat: first work"
  expect pass "a remote not named origin: pre-push checks only the push's own commits" git push -q up HEAD:nb )
# A root commit (orphan branch) is checked directly: no "unknown base <sha>^".
git clone -q "$tmp/up.git" "$tmp/orphan-clone" && ( cd "$tmp/orphan-clone"
  make -f "$tmp/real.mk" hooks >/dev/null 2>&1
  git checkout -q --orphan pages; git rm -rfq --cached . ; echo pg >index.html; echo ok >state.txt; git add -A; git commit -q -s -m "docs: orphan"
  expect pass "a signed root commit (orphan branch) pushes" git push -q origin pages
  git commit -q --amend --no-verify -m "docs: orphan unsigned" --reset-author
  expect refuse "an unsigned root commit is refused, not skipped" git push -q -f origin pages )

# --- the hooks do not depend on any checkout: a snapshot made by `make hooks` is what runs ---
unsigned_refused() { # label: stage a file and try an unsigned commit in the current directory
  echo x >"f.$RANDOM"; git add -A; expect refuse "$1" git commit -q -m "garbage unsigned"; git reset -q
}
git clone -q "$tmp/up.git" "$tmp/mc" && ( cd "$tmp/mc"
  make -f "$tmp/real.mk" hooks >/dev/null 2>&1
  sed -i '2i exit 0' .githooks/commit-msg       # an uncommitted edit of the main checkout's hook
  unsigned_refused "uncommitted edits to .githooks in the main checkout change nothing"
  git checkout -q -- .githooks
  old=$(git commit-tree "$(git hash-object -t tree /dev/null)" -p HEAD -m "chore: old checkout")
  git checkout -q --detach "$old"               # the main checkout is on a commit without .githooks
  unsigned_refused "a main checkout on an old commit (no .githooks) still runs the hooks"
  git checkout -q main
  rm -rf .git/hatamoto-hooks/snapshot
  git commit -q --allow-empty -s -m "feat: x" >"$tmp/out" 2>&1; rc=$?
  [[ $rc != 0 ]] && grep -q "hooks missing: run make hooks" "$tmp/out" && ok "a missing snapshot is loud: exit 1, 'hooks missing: run make hooks'" || { bad "a missing snapshot is loud"; cat "$tmp/out"; }
  make -f "$tmp/real.mk" hooks >/dev/null 2>&1
  expect pass "make hooks again restores the snapshot" git commit -q --allow-empty -s -m "feat: x"
  make -f "$tmp/real.mk" hooks-off >/dev/null 2>&1
  [[ -z $(git config core.hooksPath) && ! -e .git/hatamoto-hooks ]] && ok "make hooks-off removes the dir and the config" || bad "make hooks-off removes the dir and the config" )
git clone -q --separate-git-dir="$tmp/sep.git" "$tmp/up.git" "$tmp/mc2" && ( cd "$tmp/mc2"
  make -f "$tmp/real.mk" hooks >/dev/null 2>&1
  git worktree add -q --detach "$tmp/mc2-wt"
  cd "$tmp" && mv mc2 mc2-moved && git --git-dir="$tmp/sep.git" worktree repair "$tmp/mc2-moved" "$tmp/mc2-wt" >/dev/null 2>&1
  cd "$tmp/mc2-wt" && unsigned_refused "a moved main checkout (worktree repair) still runs the hooks in its worktree" )

# --- changed files map to the packages of the right module; testdata is not a package ---
git checkout -q -b mods origin/main
mkdir -p backend/pkg/sdk backend/internal/admin/testdata/guest
printf 'module example.com/sdk\n\ngo 1.21\n' >backend/pkg/sdk/go.mod
printf 'package sdk\n' >backend/pkg/sdk/sdk.go
printf 'package sdk\n\nimport "testing"\n\nfunc TestSDK(t *testing.T) {}\n' >backend/pkg/sdk/sdk_test.go
printf 'package guest\n' >backend/internal/admin/testdata/guest/g.go
echo ok >state.txt; git add -A; git commit -q -s -m "feat: sdk module and testdata"
. "$src/.githooks/lib/go-targets.sh"
got=$(hm_go_targets "$r" "origin/main...HEAD" | tr '\t\n' ' ')
[[ $got == "backend/pkg/sdk . " ]] && ok "changed files map to their own module, testdata dropped" || bad "target mapping: got '$got'"
expect pass "pre-push of an SDK-module and testdata change needs no Postgres" env -u HATAMOTO_SKIP_INTEGRATION PG_BIN=/nonexistent git push -q origin mods
printf "\\n// more\\n" >>backend/pkg/sdk/sdk.go; git commit -q -a -s -m "fix: break nothing"
printf 'package sdk\n\nimport "testing"\n\nfunc TestSDKBroken(t *testing.T) { t.Fatal("x") }\n' >backend/pkg/sdk/b_test.go
git add -A; git commit -q -s -m "test: failing sdk test"
expect refuse "pre-push runs a changed nested module's tests from its own directory" env -u HATAMOTO_SKIP_INTEGRATION PG_BIN=/nonexistent git push -q origin mods

# --- commits that skipped the commit hooks are still checked at push time ---
git checkout -q -b skipped origin/main
printf 'package admin\nfunc  Bad( ){}\n' >backend/internal/admin/ugly.go
echo ok >state.txt; git add -A; git commit -q --no-verify -s -m "fix: unformatted, hooks skipped"
expect refuse "pre-push refuses unformatted Go that skipped pre-commit" git push -q origin skipped
git reset -q --hard origin/main; echo ok >state.txt
echo "token = \"$tok\"" >leak.txt; git add -A; git commit -q --no-verify -s -m "fix: leak, hooks skipped"
expect refuse "pre-push refuses a secret that skipped pre-commit" git push -q origin skipped:leaked
git reset -q --hard origin/main
# a red quick gate does not cancel the other quick gates (they take seconds): every red one is reported
# (a stub gitleaks is slower than gofmt and red, so a cancel would catch it still running)
mkdir -p "$tmp/slow-gl"; printf '#!/usr/bin/env bash\nsleep 2; echo "stub gitleaks: red" >&2; exit 1\n' >"$tmp/slow-gl/gitleaks"; chmod +x "$tmp/slow-gl/gitleaks"
printf 'package admin\nfunc  Bad( ){}\n' >backend/internal/admin/ugly.go
echo ok >state.txt; git add -A; git commit -q --no-verify -s -m "fix: unformatted, hooks skipped"
PATH="$tmp/slow-gl:$PATH" git push -q origin skipped:both >"$tmp/out" 2>&1 && bad "T6 two red quick gates were pushed"
grep -qE 'FAIL +gofmt' "$tmp/out" && grep -qE 'FAIL +gitleaks' "$tmp/out" && ! grep -q 'cancelled' "$tmp/out" \
  && ok "T6 two red quick gates are both reported FAIL, none cancelled" || { bad "T6 quick gates cancelled each other"; cat "$tmp/out"; }
git reset -q --hard origin/main

# a secret added while concluding a merge (--no-verify) is caught: gitleaks diffs a merge against its first parent
git checkout -q -b evilside origin/main; echo e >evil-side.txt; git add -A; git commit -q -s -m "feat: evil side"
git checkout -q -b evilmerge origin/main; echo ok >state.txt; git add -A; git commit -q -s -m "feat: evil main side"
git merge -q --no-commit --no-ff evilside
echo "token = \"$tok\"" >evil-leak.txt; git add -A; git commit -q --no-verify -m "Merge branch evilside"
expect refuse "pre-push refuses a secret added in a merge commit" git push -q origin evilmerge
git reset -q --hard origin/main

# --- HATAMOTO_PREPUSH_SINCE: a head whose branch is already fetched is still checked (ship / pipeline-gate full) ---
# A plain push skips commits any remote-tracking ref holds; with the knob exactly <rev>..<sha> is checked.
ph=$r/.git/hatamoto-hooks/snapshot/.githooks/pre-push
since_push() { # <branch> [env...]: feed the hook the branch head as ship does (zero remote sha)
  local b=$1; shift
  printf 'refs/heads/%s %s refs/heads/%s %s\n' "$b" "$(git rev-parse "$b")" "$b" "$(printf '0%.0s' {1..40})" \
    | env GO_QUEUE_FORCE_LOCAL=1 "$@" "$ph" origin "$(git remote get-url origin)" >"$tmp/out" 2>&1
}
git checkout -q -b sincedco origin/main; echo s1 >docs/guide.md; echo ok >state.txt; git commit -q -a --no-verify -m "docs: unsigned, already fetched"
git push -q --no-verify origin sincedco
git checkout -q -b sincegl origin/main; echo ok >state.txt; echo "token = \"$tok\"" >since-leak.txt; git add -A; git commit -q --no-verify -s -m "docs: leak, already fetched"
git push -q --no-verify origin sincegl
since_push sincedco && ! grep -q 'FAIL' "$tmp/out" && ok "no knob: an unsigned commit already on a remote branch is skipped (unchanged)" \
  || { bad "no knob: plain behaviour changed"; sed 's/^/    | /' "$tmp/out"; }
since_push sincedco HATAMOTO_PREPUSH_SINCE=origin/main && bad "SINCE: an unsigned fetched commit passed" \
  || { grep -qE 'FAIL +dco' "$tmp/out" && ok "SINCE=origin/main: the dco gate checks the fetched unsigned commit" || { bad "SINCE: dco not failed for the right reason"; sed 's/^/    | /' "$tmp/out"; }; }
since_push sincegl && ! grep -q 'FAIL' "$tmp/out" && ok "no knob: a secret in an already fetched commit is skipped (unchanged)" \
  || { bad "no knob: gitleaks behaviour changed"; sed 's/^/    | /' "$tmp/out"; }
since_push sincegl HATAMOTO_PREPUSH_SINCE=origin/main && bad "SINCE: a fetched secret passed" \
  || { grep -qE 'FAIL +gitleaks' "$tmp/out" && ok "SINCE=origin/main: gitleaks catches the secret in the fetched commit" || { bad "SINCE: gitleaks not failed for the right reason"; sed 's/^/    | /' "$tmp/out"; }; }
since_push sincedco HATAMOTO_PREPUSH_SINCE=origin/no-such-branch && bad "SINCE: an unresolvable rev passed" \
  || { grep -q 'does not resolve' "$tmp/out" && ok "SINCE: an unresolvable rev fails closed" || { bad "SINCE: unresolvable rev message"; sed 's/^/    | /' "$tmp/out"; }; }
git checkout -q main 2>/dev/null || git checkout -q --detach origin/main; git reset -q --hard origin/main

# --- vet (checks `go test` does not run) is a gate ---
git checkout -q -b vetbad origin/main
printf 'package admin\n\nimport "sync"\n\nfunc Copy() {\n\tvar mu sync.Mutex\n\tc := mu\n\t_ = c\n}\n' >backend/internal/admin/vet.go
echo ok >state.txt; git add -A; git commit -q -s -m "fix: copies a lock"
expect refuse "pre-push runs go vet" git push -q origin vetbad

# --- staticcheck is blocking: an unused function (U1000) refuses the push ---
git checkout -q -b scbad origin/main
printf 'package admin\n\nfunc unusedByEveryone() {}\n' >backend/internal/admin/dead.go
echo ok >state.txt; git add -A; git commit -q -s -m "fix: adds dead code"
expect refuse "pre-push runs staticcheck as a blocking gate" git push -q origin scbad
grep -q "unusedByEveryone" "$tmp/out" && ok "the refusal names the staticcheck finding" || { bad "the refusal names the staticcheck finding"; cat "$tmp/out"; }

# --- a worktree of an old checkout (no .githooks, no scripts) still runs the installed hooks ---
old=$(git commit-tree "$(git hash-object -t tree /dev/null)" -p origin/main -m "chore: old checkout")
git worktree add -q --detach "$tmp/oldwt" "$old"
( cd "$tmp/oldwt"
  echo x >f.txt; git add f.txt
  expect refuse "an old worktree still refuses an unsigned commit" git commit -q -m "garbage"
  expect pass "an old worktree commits when the message is good (installed neutral/gitleaks copies)" git commit -q -s -m "feat: old but good"
  expect refuse "pre-push of a commit that predates the gates fails loudly" git push -q origin HEAD:refs/heads/oldbranch
  grep -q "predates the local gates" "$tmp/out" && ok "the refusal says why" || { bad "the refusal says why"; cat "$tmp/out"; } )
git worktree remove --force "$tmp/oldwt"

# --- known flakes: a listed test is re-run alone; an unlisted one fails ---
mkdir -p backend/flaky
printf 'package flaky\n' >backend/flaky/f.go
cat >backend/flaky/f_test.go <<'GO'
package flaky

import (
	"os"
	"testing"
)

func TestFlaky(t *testing.T) {
	if _, err := os.Stat(os.Getenv("FLAKE_MARK")); err != nil {
		os.WriteFile(os.Getenv("FLAKE_MARK"), nil, 0o600)
		t.Fatal("first run fails")
	}
}

func TestPanicFlaky(t *testing.T) {
	if _, err := os.Stat(os.Getenv("FLAKE_MARK") + ".p"); err != nil {
		os.WriteFile(os.Getenv("FLAKE_MARK")+".p", nil, 0o600)
		panic("first run panics")
	}
}

func TestOK(t *testing.T) {}

func TestAlwaysFails(t *testing.T) { t.Fatal("no") }

// Sorts after the panicking test, which kills the binary before it runs.
func TestZRealBug(t *testing.T) { t.Fatal("real bug") }
GO
# The marker file is local: a go wrapper that sends tests to a build host (GO_QUEUE_*) must not.
flake() { (cd backend && GO_QUEUE_FORCE_LOCAL=1 FLAKE_MARK="$tmp/mark.$RANDOM" HATAMOTO_FLAKES_FILE="$tmp/flakes" bash ../scripts/go-test-flaky.sh "-run $1" ./flaky); }
echo "flaky TestFlaky" >"$tmp/flakes"
expect pass "a listed flaky test that passes alone, with the rest of the package passing, does not fail the run" flake '^(TestFlaky|TestOK)$'
expect refuse "an unlisted failing test fails the run" flake '^TestAlwaysFails$'
echo "flaky TestFlak.*" >"$tmp/flakes"
expect refuse "a broad regex in the flakes list excuses nothing" flake '^TestFlaky$'
printf 'flaky TestFlaky\nflaky TestPanicFlaky\n' >"$tmp/flakes"
expect refuse "a listed test that panics fails the package" flake '^TestPanicFlaky$'
expect refuse "a listed panic cannot hide a real failure in the rest of the package" flake '^(TestPanicFlaky|TestZRealBug)$'
echo "flaky *" >"$tmp/flakes"
expect pass "a load-sensitive package (listed as '*') that passes on a whole re-run does not fail the run" flake '^(TestFlaky|TestOK)$'
expect refuse "a load-sensitive package that fails again on the re-run fails the run" flake '^TestAlwaysFails$'
expect refuse "a load-sensitive package that panics fails the run" flake '^TestPanicFlaky$'

# --- affected-only pushes (A1, A2) and the force-full triggers (A3) ---
# Enough packages that two affected ones stay under the 60% threshold: a imports b; c, d, e, f and admin stand alone.
git checkout -q -b affbase origin/main
rm -rf backend/flaky   # its failing tests are for the flake section above; a full run here must pass
mkdir -p backend/a backend/b backend/c backend/d backend/e backend/f backend/web/admin docs
printf 'package b\n\nfunc B() int { return 1 }\n' >backend/b/b.go
printf 'package a\n\nimport "example.com/scratch/b"\n\nfunc A() int { return b.B() }\n' >backend/a/a.go
printf 'package c\n' >backend/c/c.go
printf 'package d\n' >backend/d/d.go; printf 'package e\n' >backend/e/e.go; printf 'package f\n' >backend/f/f.go
echo base >backend/web/admin/page.html; echo base >docs/guide.md
echo ok >state.txt; git add -A; git commit -q -s -m "feat: scratch packages"
git push -q --no-verify origin affbase:main >"$tmp/out" 2>&1 || { bad "scratch packages push"; cat "$tmp/out"; }   # only a base: nothing to check yet
git fetch -q origin
push_out() { env GO_QUEUE_FORCE_LOCAL=1 git push origin "$1" >"$tmp/out" 2>&1; }   # the hook output lands in $tmp/out
git checkout -q -b a1 origin/main
printf '\nfunc B2() int { return 2 }\n' >>backend/b/b.go; git commit -q -a -s -m "fix: change b"
push_out a1 && grep -qE 'affected: backend \./b .*\+1 dependents' "$tmp/out" && ! grep -q 'mode: FULL' "$tmp/out" \
  && grep -qE 'go-test +[0-9]+s' "$tmp/out" && grep -qE 'check suites: skipped webonly' "$tmp/out" \
  && ok "A1 a Go change runs its package and dependents only, and skips the suites no path asks for" || { bad "A1 affected-only push"; sed 's/^/    | /' "$tmp/out"; }
git checkout -q -b a1web origin/main
echo changed >backend/web/admin/page.html; git commit -q -a -s -m "fix: admin template"
push_out a1web && grep -qE 'affected: backend \./internal/admin' "$tmp/out" && ! grep -q 'mode: FULL' "$tmp/out" \
  && ! grep -q 'skipped webonly' "$tmp/out" \
  && ok "A1 a template maps to the package that reads it (affected-readers.txt), not to a full run" || { bad "A1 template push"; sed 's/^/    | /' "$tmp/out"; }
git checkout -q -b a2 origin/main
echo changed >docs/guide.md; git commit -q -a -s -m "docs: guide"
push_out a2 && grep -q 'no Go package is touched' "$tmp/out" && ! grep -qE 'go-test +[0-9]+s|vet +[0-9]+s|staticcheck +[0-9]+s' "$tmp/out" \
  && grep -q 'check suites: skipped webonly' "$tmp/out" && ! grep -q 'mode: FULL' "$tmp/out" \
  && ok "A2 a docs push no test reads runs no Go gate and names the skipped suites" || { bad "A2 docs-only push"; sed 's/^/    | /' "$tmp/out"; }
git checkout -q -b a3 origin/main
printf '// touched\n' >>backend/go.mod; git commit -q -a -s -m "build: touch go.mod"
push_out a3 && grep -q 'mode: FULL (module file backend/go.mod)' "$tmp/out" && grep -q 'go test ./\.\.\., vet ./\.\.\., staticcheck ./\.\.\.' "$tmp/out" \
  && ! grep -q 'check suites: skipped' "$tmp/out" && grep -qE 'go-test +[0-9]+s' "$tmp/out" \
  && ok "A3 a go.mod change forces the full gates" || { bad "A3 go.mod push"; sed 's/^/    | /' "$tmp/out"; }
git checkout -q -b a3sdk origin/main
printf 'package b\n' >backend/b/b_linux.go; git add -A; git commit -q -s -m "fix: a GOOS-suffixed file"
push_out a3sdk && grep -q 'mode: FULL (GOOS/GOARCH file backend/b/b_linux.go)' "$tmp/out" \
  && ok "A3 a GOOS-suffixed file forces the full gates" || { bad "A3 GOOS file push"; sed 's/^/    | /' "$tmp/out"; }
git checkout -q -b a3main origin/main
echo "to the default branch" >docs/guide.md; git commit -q -a -s -m "docs: land on main"
git init -q --bare "$tmp/remote2.git"   # a second remote, so origin/main stays where the later sections expect it
env GO_QUEUE_FORCE_LOCAL=1 git push "$tmp/remote2.git" a3main:refs/heads/main >"$tmp/out" 2>&1 \
  && grep -q 'mode: FULL (push to the default branch)' "$tmp/out" \
  && ok "A3 a push to refs/heads/main forces the full gates" || { bad "A3 push to main"; sed 's/^/    | /' "$tmp/out"; }
env GO_QUEUE_FORCE_LOCAL=1 git push "$tmp/remote2.git" a3main:refs/heads/other >"$tmp/out" 2>&1 \
  && ! grep -q 'mode: FULL' "$tmp/out" && grep -q 'no Go package is touched' "$tmp/out" \
  && ok "A3 the same commit pushed to another branch stays affected-only" || { bad "A3 push to another branch"; sed 's/^/    | /' "$tmp/out"; }

# --- visible skips (A8): the summary counts skipped tests and names a log that outlives the hook's worktree ---
git checkout -q -b a8 origin/main
mkdir -p backend/skp
printf 'package skp\n' >backend/skp/skp.go
printf 'package skp\n\nimport "testing"\n\nfunc TestSkipped(t *testing.T) { t.Skip("not here") }\n\nfunc TestRuns(t *testing.T) {}\n' >backend/skp/skp_test.go
git add -A; git commit -q -s -m "test: a skipped test"
push_out a8 && grep -qE 'go-test +[0-9]+s +skipped: 1 \(log ' "$tmp/out" \
  && ok "A8 the push summary counts a skipped test" || { bad "A8 skipped count"; sed 's/^/    | /' "$tmp/out"; }
a8log=$(sed -n 's/.*go-test .*skipped: 1 (log \(.*\))$/\1/p' "$tmp/out" | head -1)
[[ -n $a8log && -f $a8log && $a8log == "$r/.git/hatamoto-gate-logs/"* ]] && grep -q -- '--- SKIP: TestSkipped' "$a8log" \
  && ok "A8 the gate log outlives the hook and holds the skip" || bad "A8 gate log '$a8log' missing"
git checkout -q -b a8b origin/main
echo again >docs/guide.md; git commit -q -a -s -m "docs: guide again"
HATAMOTO_GATE_CACHE=on push_out a8b; HATAMOTO_GATE_CACHE=on push_out a8b:a8c   # the same tree under another name
grep -q 'make-check .*cached.*skipped: -' "$tmp/out" && ok "A8 a cached gate prints skipped: -" || { bad "A8 cached gate"; sed 's/^/    | /' "$tmp/out"; }

# --- the full-suite sentinel (A4/A5 precondition): recorded only by a clean FULL run that skipped nothing ---
hook=$r/.git/hatamoto-hooks/snapshot/.githooks/pre-push
[[ $("$hook" --hook-api) == 3 ]] && ok "the hook advertises its api (--hook-api)" || bad "hook --hook-api"
fs_key() { echo "$r/.git/hatamoto-gate-cache/$(git rev-parse "$1^{tree}")/full-suite"; }
git checkout -q -b fs1 origin/main; echo fs1 >docs/guide.md; git commit -q -a -s -m "docs: fs1"
push_out fs1 && [[ ! -e $(fs_key fs1) ]] && ok "an affected push does not record full-suite" || { bad "affected push recorded full-suite"; sed 's/^/    | /' "$tmp/out"; }
git checkout -q -b fs2 origin/main; echo fs2 >docs/guide.md; git commit -q -a -s -m "docs: fs2"
env -u HATAMOTO_SKIP_INTEGRATION HATAMOTO_PREPUSH_FULL=1 GO_QUEUE_FORCE_LOCAL=1 git push origin fs2 >"$tmp/out" 2>&1
grep -q 'mode: FULL (forced)' "$tmp/out" && [[ -f $(fs_key fs2) ]] \
  && ok "HATAMOTO_PREPUSH_FULL=1 runs everything and records full-suite (no context suffix)" || { bad "forced full run did not record"; sed 's/^/    | /' "$tmp/out"; }
git checkout -q -b fs3 origin/main; echo fs3 >docs/guide.md; git commit -q -a -s -m "docs: fs3"
HATAMOTO_PREPUSH_FULL=1 GO_QUEUE_FORCE_LOCAL=1 git push origin fs3 >"$tmp/out" 2>&1   # HATAMOTO_SKIP_INTEGRATION=1 is exported
grep -q 'mode: FULL (forced)' "$tmp/out" && [[ ! -e $(fs_key fs3) ]] \
  && ok "a full run with the integration tests skipped does not record full-suite" || { bad "env-skipped full run recorded"; sed 's/^/    | /' "$tmp/out"; }
git checkout -q -b fs4 origin/main; echo fs4 >docs/guide.md; git commit -q -a -s -m "docs: fs4"
printf 'red * false\n' >"$tmp/suites-red.txt"
env -u HATAMOTO_SKIP_INTEGRATION HATAMOTO_PREPUSH_FULL=1 GO_QUEUE_FORCE_LOCAL=1 HATAMOTO_CHECK_SUITES="$tmp/suites-red.txt" git push origin fs4 >"$tmp/out" 2>&1; rc=$?
((rc != 0)) && [[ ! -e $(fs_key fs4) ]] && ok "a red full run does not record full-suite" || bad "red full run recorded or passed"

# A swapped suite list or flake list runs something else than the full suite: nothing is recorded.
printf 'green * true\n' >"$tmp/suites-green.txt"; : >"$tmp/flakes-empty.txt"
for knob in HATAMOTO_CHECK_SUITES=$tmp/suites-green.txt HATAMOTO_FLAKES_FILE=$tmp/flakes-empty.txt; do
  b=fs-${knob%%=*}; git checkout -q -b "$b" origin/main; echo "$b" >docs/guide.md; git commit -q -a -s -m "docs: $b"
  env -u HATAMOTO_SKIP_INTEGRATION HATAMOTO_PREPUSH_FULL=1 GO_QUEUE_FORCE_LOCAL=1 "$knob" git push origin "$b" >"$tmp/out" 2>&1; rc=$?
  ((rc == 0)) && grep -q 'mode: FULL (forced)' "$tmp/out" && [[ ! -e $(fs_key "$b") ]] \
    && ok "a full run with ${knob%%=*} set records no full-suite" || { bad "full run with ${knob%%=*} recorded or failed (rc=$rc)"; sed 's/^/    | /' "$tmp/out"; }
done

# --- farm slots (A6) and cancelling the siblings of a failed gate (A7) ---
# The suites runner takes its suites from $HATAMOTO_CHECK_SUITES, so a stub suite stands in for `make check`
# without touching scripts/check-suites.txt (a change there would force a full run).
printf '#!/usr/bin/env bash\necho "start $$" >>"$EVENTS"; sleep 2; echo "end $$" >>"$EVENTS"\n' >"$tmp/slow-check.sh"
printf 'slow * bash %s\n' "$tmp/slow-check.sh" >"$tmp/suites-slow.txt"
for n in 1 2 3; do
  git checkout -q -b farm$n origin/main; echo "farm $n" >docs/guide.md; git commit -q -a -s -m "docs: farm $n"
done
export EVENTS=$tmp/events; : >"$EVENTS"
for n in 1 2 3; do
  ( env GO_QUEUE_FORCE_LOCAL=1 HATAMOTO_FARM_SLOTS=2 HATAMOTO_FARM_POLL=0.3 HATAMOTO_CHECK_SUITES="$tmp/suites-slow.txt" \
      git push origin farm$n >"$tmp/farm$n.out" 2>&1; echo $? >"$tmp/farm$n.rc" ) &
done
wait
peak=$(awk '/^start/ { n++; if (n > m) m = n } /^end/ { n-- } END { print m + 0 }' "$EVENTS")
waits=$(grep -l 'waiting behind' "$tmp"/farm[123].out | wc -l)
[[ $(cat "$tmp"/farm[123].rc | tr -d '\n') == 000 && $(grep -c '^start' "$EVENTS") == 3 ]] && ok "A6 three pushes with two slots all pass" || { bad "A6 three pushes with two slots all pass"; cat "$tmp"/farm[123].out; }
((peak == 2)) && ok "A6 never more than two heavy phases at once" || bad "A6 peak concurrency was $peak, wanted 2"
((waits == 1)) && grep -h 'waiting behind' "$tmp"/farm[123].out | grep -q 'waiting behind farm' \
  && ok "A6 exactly one push waits, and names the branch it waits behind" || bad "A6 $waits pushes waited"
# A hook nested in a heavy phase (make check -> test-hooks) holds the marker and never waits for the slot its parent holds.
git checkout -q -b farmnest origin/main; echo nest >docs/guide.md; git commit -q -a -s -m "docs: nested"
( . "$src/.githooks/lib/farm.sh"; export HATAMOTO_FARM_SLOTS=1; hm_farm_acquire "holder" 2>/dev/null; touch "$tmp/holder.up"; sleep 30 ) &
holder=$!
while [[ ! -e $tmp/holder.up ]]; do sleep 0.1; done
expect pass "A6 a nested hook with HATAMOTO_FARM_SLOT_HELD=1 never waits" \
  env GO_QUEUE_FORCE_LOCAL=1 HATAMOTO_FARM_SLOTS=1 HATAMOTO_FARM_SLOT_HELD=1 timeout 60 git push origin farmnest
! grep -q 'waiting behind' "$tmp/out" && ok "A6 the nested hook printed no wait" || bad "A6 the nested hook waited"
kill "$holder" 2>/dev/null; wait "$holder" 2>/dev/null

# A7: a red gate cancels the running ones. The stub suite sleeps for a minute; a stub integration gate fails after a second.
printf '#!/usr/bin/env bash\necho $$ >"$PIDFILE"; sleep 60 & echo $! >"$PIDFILE.child"; wait\n' >"$tmp/hang-check.sh"
printf 'hang * bash %s\n' "$tmp/hang-check.sh" >"$tmp/suites-hang.txt"
git checkout -q -b cancel origin/main
printf '#!/usr/bin/env bash\nsleep 1; echo "stub integration gate: red" >&2; exit 1\n' >scripts/integration-gate.sh
printf '\n// Cancel is new.\nfunc Cancel() {}\n' >>backend/c/c.go
echo ok >state.txt; git add -A; git commit -q -s -m "fix: change a package, red integration gate"
export PIDFILE=$tmp/hang.pid
t0=$SECONDS
env -u HATAMOTO_SKIP_INTEGRATION GO_QUEUE_FORCE_LOCAL=1 HATAMOTO_CHECK_SUITES="$tmp/suites-hang.txt" git push origin cancel >"$tmp/out" 2>&1; rc=$?
took=$((SECONDS - t0))
((rc != 0)) && ((took < 15)) && ok "A7 the push is refused within 15 s ($took s) although a sibling gate sleeps for a minute" || { bad "A7 refused in time (rc=$rc, $took s)"; cat "$tmp/out"; }
sleep 0.5
dead=1; for f in "$PIDFILE" "$PIDFILE.child"; do [[ -s $f ]] && kill -0 "$(cat "$f")" 2>/dev/null && dead=0; done
[[ -s $PIDFILE ]] && ((dead)) && ok "A7 the cancelled gate's processes are gone" || bad "A7 the cancelled gate left a process behind"
grep -qE 'cancelled +make-check' "$tmp/out" && ! grep -qE 'FAIL +make-check' "$tmp/out" && grep -qE 'FAIL +integration' "$tmp/out" \
  && ok "A7 the cancelled gate says cancelled, the failing one FAIL" || { bad "A7 cancelled/FAIL labels"; cat "$tmp/out"; }
[[ -z $(ls -d "$tmp"/pre-push.* 2>/dev/null) ]] && ok "A7 no hook temp directory is left" || bad "A7 hook temp directory left behind"
# T6: a gate that failed before the cancel is FAIL, not cancelled (two stub gates fail at the same time)
printf '#!/usr/bin/env bash\nsleep 1; echo "stub suite: red" >&2; exit 1\n' >"$tmp/red-check.sh"
printf 'red * bash %s\n' "$tmp/red-check.sh" >"$tmp/suites-red2.txt"
env -u HATAMOTO_SKIP_INTEGRATION GO_QUEUE_FORCE_LOCAL=1 HATAMOTO_CHECK_SUITES="$tmp/suites-red2.txt" git push origin cancel:cancel2 >"$tmp/out" 2>&1
grep -qE 'FAIL +make-check' "$tmp/out" && grep -qE 'FAIL +integration' "$tmp/out" \
  && ! grep -qE 'cancelled +(make-check|integration)' "$tmp/out" \
  && ok "T6 two gates that both failed are both FAIL, none cancelled" || { bad "T6 failed gate labelled cancelled"; cat "$tmp/out"; }
# Ctrl-C: a terminal's ^C must reach the hook, which cancels its gates and refuses the push. It needs a
# pty: with job control on for the hook's own foreground commands, ^C reached only those and the push went through.
if script -qec true /dev/null >/dev/null 2>&1; then
  git checkout -q -b intr origin/main; echo intr >docs/guide.md; git commit -q -a -s -m "docs: interrupt"
  rm -f "$PIDFILE" "$PIDFILE.child"
  { for _ in $(seq 150); do [[ -s $PIDFILE.child ]] && break; sleep 0.2; done; printf '\003'; sleep 20; } \
    | GO_QUEUE_FORCE_LOCAL=1 HATAMOTO_CHECK_SUITES="$tmp/suites-hang.txt" timeout 60 script -qec "git push origin intr" /dev/null >"$tmp/out" 2>&1
  sleep 0.5
  dead=1; for f in "$PIDFILE" "$PIDFILE.child"; do [[ -s $f ]] && kill -0 "$(cat "$f")" 2>/dev/null && dead=0; done
  ! git -C "$tmp/remote.git" rev-parse -q --verify refs/heads/intr >/dev/null && ! grep -q 'all gates passed' "$tmp/out" \
    && ok "A9 ^C on a terminal refuses the push" || { bad "A9 ^C on a terminal refuses the push"; cat "$tmp/out"; }
  [[ -s $PIDFILE ]] && ((dead)) && ok "A9 ^C cancels the running gate" || bad "A9 ^C left a gate running"
  [[ -z $(ls -d "$tmp"/pre-push.* 2>/dev/null) ]] && ok "A9 no hook temp directory is left" || bad "A9 hook temp directory left behind"
else
  echo "skip A9 ^C on a pty: no util-linux script"
fi
# The real integration gate stops its Postgres and removes its directories on TERM.
if [[ -x ${PG_BIN:-}/initdb ]] || command -v initdb >/dev/null; then
  mkdir "$tmp/gt"
  GO_QUEUE_FORCE_LOCAL=1 TMPDIR="$tmp/gt" bash "$src/scripts/integration-gate.sh" "$src" ./internal/budgetfile >"$tmp/gt.out" 2>&1 &
  gpid=$!
  for _ in $(seq 100); do compgen -G "$tmp/gt/int-gate.*/data/postmaster.pid" >/dev/null && break; sleep 0.3; done
  pgpid=$(head -1 "$tmp"/gt/int-gate.*/data/postmaster.pid 2>/dev/null)
  kill -TERM "$gpid"; wait "$gpid" 2>/dev/null; sleep 1
  [[ -n $pgpid ]] && ! kill -0 "$pgpid" 2>/dev/null && [[ -z $(ls -A "$tmp/gt") ]] \
    && ok "A7 integration-gate stops Postgres and removes its directories on SIGTERM" || bad "A7 integration-gate left Postgres or directories behind (pg pid '$pgpid')"
else
  echo "skip integration-gate SIGTERM: no PostgreSQL tools (set PG_BIN)"
fi

# --- the integration gate works with a space in TMPDIR (needs the real repo and Postgres tools) ---
if [[ -x ${PG_BIN:-}/initdb ]] || command -v initdb >/dev/null; then
  mkdir "$tmp/has space"; socks=$(ls -d /tmp/hmpg.* 2>/dev/null | wc -l)
  expect pass "integration-gate works with a space in TMPDIR" \
    env TMPDIR="$tmp/has space" bash "$src/scripts/integration-gate.sh" "$src" ./internal/budgetfile
  [[ -z $(ls -A "$tmp/has space") && $(ls -d /tmp/hmpg.* 2>/dev/null | wc -l) == "$socks" ]] && ok "integration-gate removes its directories" || bad "integration-gate leaves directories behind"
else
  echo "skip integration-gate with a space in TMPDIR: no PostgreSQL tools (set PG_BIN)"
fi

echo
fails=0; [[ ! -e $tmp/fails ]] || fails=$(wc -l <"$tmp/fails")
((fails == 0)) && echo "test-hooks: PASSED" || { echo "test-hooks: $fails FAILED"; exit 1; }
