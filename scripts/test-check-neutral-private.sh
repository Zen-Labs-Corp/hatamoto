#!/usr/bin/env bash
# test-check-neutral-private.sh - scripts/check-neutral.sh with .neutral-private absent (public
# CI), a regular file, and a symlink into the notes clone (.neutral-private ->
# .pi/neutral-private, made by the notes repo's notes-link.sh after the public cut-over). Runs
# the real check-neutral.sh in a throwaway repository; the banned word is made up here.
set -Eeuo pipefail
unset HATAMOTO_NEUTRAL_NO_PRIVATE CI
cd "$(git rev-parse --show-toplevel)"
SCRIPT=$PWD/scripts/check-neutral.sh
bash -n "$SCRIPT"
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
export GIT_CONFIG_GLOBAL=$W/gitconfig GIT_CONFIG_NOSYSTEM=1
git config --global user.name T; git config --global user.email t@example.com
git config --global init.defaultBranch main
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }

r=$W/repo; mkdir -p "$r/scripts" "$r/.pi"
cp "$SCRIPT" "$r/scripts/check-neutral.sh"
printf '/.neutral-private export-ignore\n/.pi export-ignore\n/.pi/** export-ignore\n' >"$r/.gitattributes"
word=zq""xprivname   # split so this file never contains the word itself
echo "hello" >"$r/README.md"
git init -q "$r"; git -C "$r" add -A; git -C "$r" commit -qm init
cn() { (cd "$r" && bash scripts/check-neutral.sh) >"$W/out" 2>&1; }

cn || { cat "$W/out"; fail "absent: a clean tree fails"; }
grep -q 'check-neutral: PASSED' "$W/out" || fail "absent: no PASSED line"
pass "absent .neutral-private: runs and passes (public CI)"

echo "see $word here" >"$r/README.md"; git -C "$r" commit -qam "name it"
cn || { cat "$W/out"; fail "absent: the word must pass when no list bans it"; }
printf 'ban %s\n' "$word" >"$r/.neutral-private"
if cn; then cat "$W/out"; fail "regular file: a banned word passed"; fi
grep -q "README.md" "$W/out" || fail "regular file: the hit is not reported"
pass "regular .neutral-private: the private ban is enforced"

# The opt-out never skips a readable file, and CI refuses it.
rc=0; (cd "$r" && HATAMOTO_NEUTRAL_NO_PRIVATE=1 bash scripts/check-neutral.sh) >"$W/out" 2>&1 || rc=$?
[[ $rc -eq 1 ]] || { cat "$W/out"; fail "opt-out with a regular file: exit $rc, want 1"; }
grep -q "README.md" "$W/out" || fail "opt-out with a regular file: the hit is not reported"
pass "HATAMOTO_NEUTRAL_NO_PRIVATE=1 with a regular file: the private ban is still enforced"
rc=0; (cd "$r" && CI=true HATAMOTO_NEUTRAL_NO_PRIVATE=1 bash scripts/check-neutral.sh) >"$W/out" 2>&1 || rc=$?
[[ $rc -eq 2 ]] || { cat "$W/out"; fail "opt-out under CI: exit $rc, want 2"; }
grep -q "CI is set" "$W/out" || { cat "$W/out"; fail "opt-out under CI: message missing"; }
pass "HATAMOTO_NEUTRAL_NO_PRIVATE=1 with CI set: refused with exit 2"

rm "$r/.neutral-private"
printf 'ban %s\n' "$word" >"$r/.pi/neutral-private"
ln -s .pi/neutral-private "$r/.neutral-private"
if cn; then cat "$W/out"; fail "symlink: a banned word passed"; fi
grep -q "README.md" "$W/out" || fail "symlink: the hit is not reported"
echo "hello" >"$r/README.md"; git -C "$r" commit -qam clean
cn || { cat "$W/out"; fail "symlink: a clean tree fails"; }
pass "symlinked .neutral-private (notes clone): the private ban is enforced, clean tree passes"

# A dangling link (notes clone missing) fails with exit 2 instead of silently dropping the private bans.
rm -rf "$r/.pi"
rc=0; cn || rc=$?
[[ $rc -eq 2 ]] || { cat "$W/out"; fail "dangling link: exit $rc, want 2"; }
grep -q "dangling link" "$W/out" || { cat "$W/out"; fail "dangling link: message missing"; }
pass "dangling .neutral-private link: exit 2 with a clear message"

# HATAMOTO_NEUTRAL_NO_PRIVATE=1 is the explicit opt-out: a dangling link then passes with a notice.
rc=0; (cd "$r" && HATAMOTO_NEUTRAL_NO_PRIVATE=1 bash scripts/check-neutral.sh) >"$W/out" 2>&1 || rc=$?
[[ $rc -eq 0 ]] || { cat "$W/out"; fail "opt-out env: exit $rc, want 0"; }
grep -q "HATAMOTO_NEUTRAL_NO_PRIVATE=1" "$W/out" || { cat "$W/out"; fail "opt-out env: notice missing"; }
pass "HATAMOTO_NEUTRAL_NO_PRIVATE=1: a dangling link is skipped with a notice"
echo "test-check-neutral-private: all passed"
