#!/usr/bin/env bash
# test-publish-sdk.sh - tests scripts/publish-sdk.sh against a throwaway core
# repository (a fake backend/pkg/sdk) and file:// bare remotes. No network, no real
# remote. Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
SCRIPT=$PWD/scripts/publish-sdk.sh
bash -n "$SCRIPT"

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }

# Identity and isolation: the temp repos see only this config.
export GIT_CONFIG_GLOBAL=$W/gitconfig GIT_CONFIG_NOSYSTEM=1
git config --global user.name "Private Person"
git config --global user.email "private@example.com"
git config --global init.defaultBranch main

core=$W/core
mkdir -p "$core/backend/pkg/sdk/guest" "$core/backend/internal"
git init -q "$core"
write_version() { printf 'package sdk\n\nconst Version = "%s"\n' "$1" >"$core/backend/pkg/sdk/version.go"; }
release_commit() { git -C "$core" add -A && git -C "$core" commit -q -m "$1"; }
write_version 0.1.0
printf 'module hatamoto.dev/sdk\n\ngo 1.27\n' >"$core/backend/pkg/sdk/go.mod"
printf 'package guest\n' >"$core/backend/pkg/sdk/guest/guest.go"
printf '#!/bin/sh\n' >"$core/backend/pkg/sdk/run.sh"; chmod +x "$core/backend/pkg/sdk/run.sh"
echo "private" >"$core/backend/internal/secret.go"
release_commit "core: sdk 0.1.0"

bare() { git init -q --bare "$1"; }
publish() { (cd "$core" && "$SCRIPT" "$@"); } # publish <args>
rtree() { git --git-dir="$1" rev-parse "$2^{tree}"; }
sdktree() { git -C "$core" rev-parse "${1:-HEAD}:backend/pkg/sdk"; }

# 1. Dry run reads the remote and changes nothing.
r1=$W/r1.git; bare "$r1"
out=$(publish --remote "file://$r1") || fail "dry run failed: $out"
grep -q "dry run" <<<"$out" || fail "dry run: no notice: $out"
[ -z "$(git --git-dir="$r1" for-each-ref)" ] || fail "dry run changed the remote"
pass "dry run"

# 2. First release: one parentless commit, tree equal to backend/pkg/sdk, signed off, tag v0.1.0.
publish --remote "file://$r1" --push >/dev/null || fail "first release failed"
c1=$(git --git-dir="$r1" rev-parse refs/heads/main)
[ "$(git --git-dir="$r1" rev-list --count main)" = 1 ] || fail "first release: not a single commit"
[ "$(git --git-dir="$r1" rev-list --parents -n1 main | awk '{print NF - 1}')" = 0 ] || fail "first release: has a parent"
[ "$(rtree "$r1" main)" = "$(sdktree)" ] || fail "first release: tree differs from backend/pkg/sdk"
[ "$(git --git-dir="$r1" rev-parse refs/tags/v0.1.0)" = "$c1" ] || fail "first release: tag v0.1.0 missing"
git --git-dir="$r1" log -1 --format=%B main | grep -q '^Signed-off-by: Zen Labs Corporation <release@hatamoto.dev>$' || fail "first release: no public Signed-off-by"
[ "$(git --git-dir="$r1" log -1 --format='%an <%ae>|%cn <%ce>' main)" = "Zen Labs Corporation <release@hatamoto.dev>|Zen Labs Corporation <release@hatamoto.dev>" ] || fail "first release: author/committer is not the public identity"
git --git-dir="$r1" log -1 --format='%an %ae %cn %ce %B' main | grep -qi 'private' && fail "first release leaked the local git identity"
git --git-dir="$r1" ls-tree -r --name-only main | grep -q secret && fail "first release leaked a core file"
[ "$(git --git-dir="$r1" ls-tree main run.sh | cut -c1-6)" = 100755 ] || fail "first release: executable bit lost"
pass "first release is parentless, tree equal, signed off, tagged"

# 3. Re-running the same version is a no-op.
before=$(git --git-dir="$r1" for-each-ref)
publish --remote "file://$r1" --push >"$W/rerun.log" || fail "re-run failed"
grep -q "nothing to do" "$W/rerun.log" || fail "re-run: no no-op notice: $(cat "$W/rerun.log")"
[ "$(git --git-dir="$r1" for-each-ref)" = "$before" ] || fail "re-run changed the remote"
pass "re-run is a no-op"

# 4. Second release: one commit on top, tree equal.
write_version 0.2.0; echo "// more" >>"$core/backend/pkg/sdk/guest/guest.go"; release_commit "core: sdk 0.2.0"
publish --remote "file://$r1" --push >/dev/null || fail "second release failed"
c2=$(git --git-dir="$r1" rev-parse refs/heads/main)
[ "$(git --git-dir="$r1" rev-parse "$c2^")" = "$c1" ] || fail "second release: parent is not the first release"
[ "$(git --git-dir="$r1" rev-list --count main)" = 2 ] || fail "second release: unexpected history"
[ "$(rtree "$r1" main)" = "$(sdktree)" ] || fail "second release: tree differs"
[ "$(git --git-dir="$r1" rev-parse refs/tags/v0.2.0)" = "$c2" ] && [ "$(git --git-dir="$r1" rev-parse refs/tags/v0.1.0)" = "$c1" ] || fail "second release: tags wrong"
pass "second release adds one commit on top"

# 5. The same tag with a different tree is refused and nothing is pushed.
echo "// changed without a version bump" >>"$core/backend/pkg/sdk/guest/guest.go"; release_commit "core: sdk edit"
before=$(git --git-dir="$r1" for-each-ref)
if out=$(publish --remote "file://$r1" --push 2>&1); then fail "changed tree under an existing tag accepted"; fi
grep -q "refusing: v0.2.0 exists with a different tree" <<<"$out" || fail "tag mismatch: wrong error: $out"
[ "$(git --git-dir="$r1" for-each-ref)" = "$before" ] || fail "refusal changed the remote"
pass "same tag, different tree is refused"

# 6. A remote that moved after the fetch is refused by the plain push (non-fast-forward).
write_version 0.3.0; release_commit "core: sdk 0.3.0"
r6=$W/r6.git; bare "$r6"
publish --remote "file://$r6" --push >/dev/null || fail "r6 first release"
write_version 0.4.0; release_commit "core: sdk 0.4.0"
mkdir -p "$W/shim"
real_git=$(command -v git)
cat >"$W/shim/git" <<STUB
#!/usr/bin/env bash
# Before the script pushes, someone else lands a commit on the remote main.
for a in "\$@"; do
  if [ "\$a" = push ]; then
    t=\$($real_git --git-dir="$r6" rev-parse main^{tree})
    c=\$($real_git --git-dir="$r6" commit-tree "\$t" -p main -m intruder)
    $real_git --git-dir="$r6" update-ref refs/heads/main "\$c"
    break
  fi
done
exec $real_git "\$@"
STUB
chmod +x "$W/shim/git"
before=$(git --git-dir="$r6" rev-parse main)
if out=$(PATH="$W/shim:$PATH" publish --remote "file://$r6" --push 2>&1); then fail "non-fast-forward push accepted"; fi
grep -q "refused" <<<"$out" || fail "non-ff: wrong error: $out"
git --git-dir="$r6" rev-parse -q --verify refs/tags/v0.4.0 >/dev/null && fail "non-ff: tag was pushed without main"
[ "$(git --git-dir="$r6" log -1 --format=%s main)" = intruder ] || fail "non-ff: the intruder commit was overwritten"
pass "moved remote is refused, nothing overwritten"

# 7. Main already has the tree but lacks the tag: only the tag is added, no new commit.
r7=$W/r7.git; bare "$r7"
publish --remote "file://$r7" --push >/dev/null || fail "r7 seed"
git --git-dir="$r7" tag -d v0.4.0 >/dev/null
publish --remote "file://$r7" --push >"$W/tagonly.log" || fail "tag-only run failed: $(cat "$W/tagonly.log")"
[ "$(git --git-dir="$r7" rev-parse v0.4.0)" = "$(git --git-dir="$r7" rev-parse main)" ] || fail "tag-only: tag not on main"
[ "$(git --git-dir="$r7" rev-list --count main)" = 1 ] || fail "tag-only: created a commit"
pass "tag-only when main already has the tree"

# 8. --out materializes a checkout without any network (the remote does not exist).
o=$W/out
publish --remote "file://$W/does-not-exist.git" --out "$o" >/dev/null || fail "--out first run failed"
[ "$(git -C "$o" rev-parse HEAD^{tree})" = "$(sdktree)" ] || fail "--out: tree differs"
[ "$(git -C "$o" rev-list --count HEAD)" = 1 ] || fail "--out: not a single commit"
git -C "$o" rev-parse -q --verify refs/tags/v0.4.0 >/dev/null || fail "--out: tag missing"
[ "$(git -C "$o" remote get-url origin)" = "file://$W/does-not-exist.git" ] || fail "--out: origin not set"
[ -f "$o/go.mod" ] && [ -f "$o/version.go" ] && [ -f "$o/guest/guest.go" ] && [ -z "$(git -C "$o" status --porcelain)" ] || fail "--out: working copy wrong"
[ -z "$(ls -A "$o" | grep -v -x -e .git -e go.mod -e version.go -e guest -e run.sh)" ] || fail "--out: unexpected files"
write_version 0.5.0; release_commit "core: sdk 0.5.0"
publish --out "$o" >/dev/null || fail "--out second run failed"
[ "$(git -C "$o" rev-list --count HEAD)" = 2 ] && [ "$(git -C "$o" rev-parse HEAD^{tree})" = "$(sdktree)" ] || fail "--out: second release wrong"
echo dirt >>"$o/go.mod"
if out=$(publish --out "$o" 2>&1); then fail "--out accepted a dirty checkout"; fi
grep -q "uncommitted" <<<"$out" || fail "--out dirty: wrong error: $out"
git -C "$o" checkout -q -- go.mod
pass "--out materializes a checkout offline"

# 9. A dirty tree and a worktree do not matter: the export reads REV.
wt=$W/wt
git -C "$core" worktree add -q "$wt" HEAD
echo "// dirty" >>"$wt/backend/pkg/sdk/version.go"
o2=$W/out2
(cd "$wt" && "$SCRIPT" --out "$o2" >/dev/null) || fail "worktree run failed"
[ "$(git -C "$o2" rev-parse HEAD^{tree})" = "$(sdktree)" ] || fail "worktree: dirty copy leaked into the export"
pass "works from a dirty worktree"

# 10. --rev exports an older commit; bad input dies.
o3=$W/out3
publish --rev HEAD~1 --out "$o3" >/dev/null || fail "--rev run failed"
git -C "$o3" rev-parse -q --verify refs/tags/v0.4.0 >/dev/null && [ "$(git -C "$o3" rev-parse HEAD^{tree})" = "$(sdktree HEAD~1)" ] || fail "--rev: wrong export"
refused() { # refused <message fragment> <args>: fails, and for that reason
  local want=$1 msg; shift
  if msg=$(publish "$@" 2>&1); then fail "accepted: $*"; fi
  case $msg in *"$want"*) ;; *) fail "refused for another reason (wanted '$want'): $msg" ;; esac
}
refused "unknown revision nosuchrev" --rev nosuchrev --out "$W/x"
refused "--out and --push are exclusive" --out "$W/x" --push
refused "cannot read file://$W/missing.git" --remote "file://$W/missing.git"
pass "bad input is refused"

# 11. The identity is overridable by env, and refused when a ban line of .neutral-private matches.
o4=$W/out4
HATAMOTO_PUBLIC_NAME="Acme Bot" HATAMOTO_PUBLIC_EMAIL="bot@acme.example" publish --out "$o4" >/dev/null || fail "identity override failed"
[ "$(git -C "$o4" log -1 --format='%an <%ae>|%cn <%ce>')" = "Acme Bot <bot@acme.example>|Acme Bot <bot@acme.example>" ] || fail "identity override not used"
printf 'ban acme\nallow x y\n' >"$core/.neutral-private"
before_o=$W/out5
if out=$(HATAMOTO_PUBLIC_NAME="Acme Bot" HATAMOTO_PUBLIC_EMAIL="bot@acme.example" publish --out "$before_o" 2>&1); then fail "banned identity accepted"; fi
grep -q "matches a ban line of .neutral-private" <<<"$out" || fail "banned identity: wrong error: $out"
[ ! -e "$before_o" ] || fail "banned identity: output created"
publish --out "$before_o" >/dev/null || fail "default identity refused by an unrelated ban"
rm "$core/.neutral-private"
pass "public identity: default, override, ban refusal"

# 11b. Public commits carry UTC dates, whatever the local time zone.
o6=$W/out6
TZ=Etc/GMT-11 publish --out "$o6" >/dev/null || fail "export in a +1100 time zone failed"
[ "$(git -C "$o6" log -1 --format='%ad|%cd' --date=format:%z)" = "+0000|+0000" ] || fail "commit dates are not UTC: $(git -C "$o6" log -1 --format='%ad|%cd' --date=raw)"
pass "commit dates are UTC"

# 11c. The ban check fails closed: an invalid pattern refuses, and a last line without a newline is read.
printf 'ban [unclosed\n' >"$core/.neutral-private"
if out=$(publish --out "$W/out7" 2>&1); then fail "invalid ban regex: publish went ahead"; fi
grep -q "invalid ban pattern" <<<"$out" || fail "invalid ban regex: wrong error: $out"
[ ! -e "$W/out7" ] || fail "invalid ban regex: output created"
printf 'ban zen labs' >"$core/.neutral-private"   # no trailing newline
if out=$(publish --out "$W/out8" 2>&1); then fail "last ban line without newline was skipped"; fi
grep -q "matches a ban line" <<<"$out" || fail "unterminated ban line: wrong error: $out"
rm "$core/.neutral-private"
pass "ban check fails closed"

# 11d. --help prints the whole header, identity paragraph included.
publish --help | grep -q 'matches a "ban" line of .neutral-private' || fail "--help cuts the header short"
pass "--help prints the identity paragraph"

# 11e. HATAMOTO_SDK_REMOTE sets the default target; --remote still wins; unset keeps today's URL.
re=$W/env.git; bare "$re"
HATAMOTO_SDK_REMOTE="file://$re" publish --push >/dev/null || fail "HATAMOTO_SDK_REMOTE push failed"
git --git-dir="$re" rev-parse -q --verify refs/heads/main >/dev/null || fail "HATAMOTO_SDK_REMOTE was not the target"
o9=$W/out9
HATAMOTO_SDK_REMOTE="file://$W/nowhere.git" publish --remote "file://$re" --out "$o9" >/dev/null || fail "--remote with HATAMOTO_SDK_REMOTE set failed"
[ "$(git -C "$o9" remote get-url origin)" = "file://$re" ] || fail "--remote did not override HATAMOTO_SDK_REMOTE"
o10=$W/out10
(unset HATAMOTO_SDK_REMOTE; publish --out "$o10" >/dev/null) || fail "default remote export failed"
[ "$(git -C "$o10" remote get-url origin)" = "https://github.com/Zen-Labs-Corp/hatamoto-sdk.git" ] || fail "default remote changed: $(git -C "$o10" remote get-url origin)"
pass "HATAMOTO_SDK_REMOTE sets the target, --remote overrides, default unchanged"

# 12. The script has no force path.
if grep -n -e '--force' -e '+refs' -e '--mirror' "$SCRIPT"; then fail "publish-sdk.sh contains a force or mirror push"; fi
pass "no force push path"
echo "PASS"
