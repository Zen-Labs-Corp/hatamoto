#!/usr/bin/env bash
# test-public-snapshot.sh - offline checks of scripts/public-snapshot.sh and of
# the export-ignore rules in .gitattributes. Run by `make check`.
#
#   1. this repository: no export-ignored file is in `git archive HEAD`, and
#      the archive holds every other tracked file of HEAD;
#   2. a throwaway repository with this repo's .gitattributes: an
#      export-ignored file (.pi/, CLAUDE.md, design/) is absent from the
#      snapshot, the snapshot is ONE commit with the given identity;
#   3. refusals: no identity, a free-mail address, a dirty tree, a non-empty
#      target without --force;
#   4. scripts/check-neutral.sh skips export-ignored files and fails on a
#      private name in a shipped one.
set -euo pipefail
export LC_ALL=C   # sort and comm must agree on one collation, whatever the caller's locale
ROOT=$(git rev-parse --show-toplevel)
cd "$ROOT"
S="$ROOT/scripts/public-snapshot.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

# --- 1. this repository ------------------------------------------------------
git ls-tree -r --name-only -z HEAD > "$T/head.lst"
git -c core.quotepath=false archive --format=tar HEAD | tar -t | grep -v '/$' | sort > "$T/archive.lst" || true
git check-attr --source=HEAD -z --stdin export-ignore < "$T/head.lst" 2>/dev/null \
  | awk -v RS='\0' 'NR%3==1{p=$0} NR%3==0 && $0=="set"{print p}' | sort > "$T/ignored.lst"
leaked=$(comm -12 "$T/archive.lst" "$T/ignored.lst")
[[ -z "$leaked" ]] || fail "export-ignored files are in git archive HEAD:"$'\n'"$leaked"
tr '\0' '\n' < "$T/head.lst" | sort | comm -23 - "$T/ignored.lst" > "$T/kept.lst"
missing=$(comm -23 "$T/kept.lst" "$T/archive.lst")
[[ -z "$missing" ]] || fail "tracked files missing from git archive HEAD:"$'\n'"$missing"
echo "ok   archive: $(wc -l < "$T/archive.lst") files shipped, $(wc -l < "$T/ignored.lst") export-ignored, none leaked"

# --- 2. a throwaway repository ----------------------------------------------
R="$T/repo"; mkdir -p "$R/scripts" "$R/.pi/plans" "$R/design" "$R/docs"
cp "$ROOT/.gitattributes" "$R/.gitattributes"
cp "$S" "$R/scripts/public-snapshot.sh"
echo keep > "$R/docs/keep.md"
echo secret-plan > "$R/.pi/plans/plan.md"
echo rules > "$R/CLAUDE.md"
echo "ban secretname" > "$R/.neutral-private"
echo mock > "$R/design/mock.html"
gr() { git -C "$R" -c user.name=t -c user.email=t@example.com -c commit.gpgsign=false -c core.hooksPath=/dev/null "$@"; }
gr init -q && gr add -A && gr commit -qm private1 && echo two >> "$R/docs/keep.md" && gr commit -qam private2
[[ -e "$R/.pi/plans/plan.md" ]] && gr ls-files --error-unmatch .pi/plans/plan.md >/dev/null || fail "fixture: .pi is not tracked"

ID=(--author-name "Example Org" --author-email "release@example.org")
out=$(cd "$R" && bash scripts/public-snapshot.sh "${ID[@]}" --out "$T/snap" 2>&1) || fail "snapshot failed: $out"
[[ -f "$T/snap/docs/keep.md" ]] || fail "kept file missing from the snapshot"
for p in .pi .pi/plans/plan.md CLAUDE.md design .neutral-private; do
  [[ ! -e "$T/snap/$p" ]] || fail "export-ignored $p is in the snapshot"
done
n=$(git -C "$T/snap" rev-list --count HEAD); [[ "$n" == 1 ]] || fail "snapshot has $n commits, want 1"
[[ "$(git -C "$T/snap" log -1 --format=%s)" == "Initial public release" ]] || fail "wrong commit subject"
[[ "$(git -C "$T/snap" log -1 --format='%an <%ae>|%cn <%ce>')" == "Example Org <release@example.org>|Example Org <release@example.org>" ]] || fail "wrong identity"
git -C "$T/snap" log -1 --format=%B | grep -q '^Signed-off-by: Example Org <release@example.org>$' || fail "no sign-off"
[[ -z "$(git -C "$T/snap" remote)" ]] || fail "snapshot has a remote"
grep -q '^files:' <<<"$out" && grep -q '^size:' <<<"$out" || fail "no file count or size printed: $out"
echo "ok   snapshot: export-ignored paths absent, one signed-off commit"

# --- 3. refusals ---------------------------------------------------------------
refuse() { # want-message, args...
  local want=$1; shift
  local o rc=0
  o=$(cd "$R" && bash scripts/public-snapshot.sh "$@" 2>&1) || rc=$?
  [[ $rc -ne 0 ]] || fail "accepted: $*"
  grep -q -- "$want" <<<"$o" || fail "refusal of [$*] does not say '$want': $o"
}
refuse "no default" --out "$T/s1"
refuse "no default" --author-name "Example Org" --out "$T/s1"
refuse "free-mail" --author-name X --author-email someone@gmail.com --out "$T/s1"
refuse "not empty" "${ID[@]}" --out "$T/snap"
echo dirty >> "$R/docs/keep.md"
refuse "dirty" "${ID[@]}" --out "$T/s2"
gr checkout -q -- docs/keep.md
echo new > "$R/untracked.txt"
refuse "dirty" "${ID[@]}" --out "$T/s3"
rm "$R/untracked.txt"
[[ ! -e "$T/s2" && ! -e "$T/s3" ]] || fail "a refused run wrote its target"
out=$(cd "$R" && bash scripts/public-snapshot.sh "${ID[@]}" --out "$T/snap" --force 2>&1) || fail "--force failed: $out"
echo "ok   refusals: identity, free-mail, dirty tree, non-empty target"
# --- 4. check-neutral scans exactly the shipped set ----------------------------
cp "$ROOT/scripts/check-neutral.sh" "$R/scripts/check-neutral.sh"
# Built at run time so this file does not trip check-neutral itself.
word=$(printf 'over%s' lay); home=$(printf '/ho%s/someone' me)
echo "notes about the $word" >> "$R/.pi/plans/plan.md"
echo "see $home/x" >> "$R/CLAUDE.md"
gr add -A && gr commit -qm "private words in export-ignored files"
o=$(cd "$R" && bash scripts/check-neutral.sh 2>&1) || fail "check-neutral failed on export-ignored files only: $o"
echo "a fork $word" > "$R/docs/leak.md"
gr add -A && gr commit -qm leak
o=$(cd "$R" && bash scripts/check-neutral.sh 2>&1) && fail "check-neutral passed a shipped file with a banned word"
grep -q 'docs/leak.md:1:' <<<"$o" || fail "check-neutral did not name the leaking file: $o"
echo "path $home/src" > "$R/docs/leak.md"
gr commit -qam leak2
o=$(cd "$R" && bash scripts/check-neutral.sh 2>&1) && fail "check-neutral passed a home-directory path"
# Each later-added ban is planted separately. Built at run time so this file does not trip itself.
for term in "$(printf 'Goland%s' Projects)" "$(printf '%%h%s' /)" "$(printf '.pi%s' /plans)"; do
  echo "x $term/y" > "$R/docs/leak.md"
  gr commit -qam "plant $term"
  o=$(cd "$R" && bash scripts/check-neutral.sh 2>&1) && fail "check-neutral passed a shipped file with $term"
  grep -q 'docs/leak.md:1:' <<<"$o" || fail "check-neutral did not name the file planting $term: $o"
done
# An allowed line stays allowed only in its own file: the same text elsewhere is caught.
printf 'ExecStart=%s/.local/bin/hatamoto-check-channel-freshness --min-days 7\n' '%h' > "$R/docs/leak.md"
gr commit -qam "allowed text in another file"
o=$(cd "$R" && bash scripts/check-neutral.sh 2>&1) && fail "check-neutral allowed a pinned line in another file"
echo "ok   check-neutral: export-ignored files skipped, shipped leaks caught"
echo "public snapshot tests ok"
