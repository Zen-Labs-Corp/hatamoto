#!/usr/bin/env bash
# test-pipeline-gate-full.sh - `pipeline-gate.sh full` against a stub pre-push hook in a scratch clone:
# it passes the zero sha (the branch may already be pushed), and a pass that records no `full-suite`
# is a failure (unless HATAMOTO_SKIP_INTEGRATION=1, which it reports).
set -euo pipefail
# the knobs this test sets per call must not leak in from the caller (a sanctioned
# HATAMOTO_SKIP_INTEGRATION=1 push runs make check, and with it this test)
unset HATAMOTO_CHECK_SUITES HATAMOTO_FLAKES_FILE HATAMOTO_GATE_CACHE HATAMOTO_SKIP_INTEGRATION
top=$(git rev-parse --show-toplevel)
if [[ -L "$top/scripts/pipeline-gate.sh" && ! -e "$top/scripts/pipeline-gate.sh" ]]; then
  echo "test-pipeline-gate-full: FAIL: scripts/pipeline-gate.sh is a dangling link (run .pi/bin/notes-link.sh)" >&2
  exit 1
fi
if [[ ! -e "$top/scripts/pipeline-gate.sh" ]]; then
  echo "test-pipeline-gate-full: SKIP (scripts/pipeline-gate.sh absent: notes-clone tooling, not in the public tree)"
  exit 0
fi
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=T GIT_AUTHOR_EMAIL=t@example.com GIT_COMMITTER_NAME=T GIT_COMMITTER_EMAIL=t@example.com
git init -q --bare -b main "$W/origin.git"
git init -q -b main "$W/r"; cd "$W/r"
mkdir -p scripts .githooks/lib
cp "$top/scripts/pipeline-gate.sh" scripts/; cp "$top/.githooks/lib/gate-cache.sh" .githooks/lib/
printf '#!/usr/bin/env bash\nexit 0\n' >scripts/check-neutral.sh
git config core.hooksPath .githooks
GC=$(git rev-parse --git-common-dir)/hatamoto-gate-cache
cat >.githooks/pre-push <<HOOK
#!/usr/bin/env bash
[[ \${1:-} != --hook-api ]] || { echo \${HOOK_API:-3}; exit 0; }
cat >"$W/stdin.log"
env | grep -E '^HATAMOTO_(CHECK_SUITES|FLAKES_FILE|GATE_CACHE|SKIP_INTEGRATION)=' >"$W/env.log" || true
echo "\${HATAMOTO_PREPUSH_SINCE:-unset}" >"$W/since.log"
[[ \${HOOK_MODE:-green} == green ]] || exit 0
mkdir -p "$GC/\$(git rev-parse HEAD^{tree})" && : >"$GC/\$(git rev-parse HEAD^{tree})/full-suite"
HOOK
chmod +x .githooks/pre-push
git add -A; git commit -qm base; git remote add origin "$W/origin.git"; git push -q origin main
git symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main
git checkout -q -b feat; echo x >x.txt; git add -A; git commit -qm change; git push -q origin feat   # already pushed
fail() { cat "$W/log" 2>/dev/null; echo "FAIL: pipeline-gate full: $1"; exit 1; }
full() { bash scripts/pipeline-gate.sh full >"$W/log" 2>&1; }

full || fail "a green full run failed"
zero=$(printf '0%.0s' {1..40})
[[ $(awk '{print $4}' "$W/stdin.log") == "$zero" ]] || fail "the remote sha must be the zero sha, got: $(cat "$W/stdin.log")"
grep -q 'full-suite recorded' "$W/log" || fail "no 'recorded' line"
[[ $(cat "$W/since.log") == origin/main ]] || fail "HATAMOTO_PREPUSH_SINCE must be origin/main, got: $(cat "$W/since.log")"
# a hook-api 2 hook would check zero commits of an already pushed branch: refused
rm -f "$W/stdin.log"; rm -rf "$GC"
HOOK_API=2 full && fail "a hook-api 2 hook was accepted"
grep -q 'run make hooks' "$W/log" && [[ ! -e $W/stdin.log ]] || fail "hook-api 2 not refused with 'run make hooks'"
rm -rf "$GC"

rm -rf "$GC"
HATAMOTO_CHECK_SUITES=/x HATAMOTO_FLAKES_FILE=/y HATAMOTO_GATE_CACHE=off full || fail "knob run failed"
[[ ! -s $W/env.log ]] || fail "an env knob reached the full run: $(cat "$W/env.log")"
rm -rf "$GC"
HOOK_MODE=silent full && fail "a pass that recorded nothing was accepted"
grep -q 'recorded no full-suite' "$W/log" || fail "the failure does not say why"

HATAMOTO_SKIP_INTEGRATION=1 HOOK_MODE=silent full || fail "HATAMOTO_SKIP_INTEGRATION=1 must not fail the gate"
grep -q 'HATAMOTO_SKIP_INTEGRATION=1 skipped' "$W/log" || fail "the skip is not reported"
echo "test-pipeline-gate-full: ok"
