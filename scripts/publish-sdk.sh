#!/usr/bin/env bash
# publish-sdk.sh - export backend/pkg/sdk to the public SDK repository.
#
# Usage:
#     scripts/publish-sdk.sh [--rev REV] [--remote URL] [--out DIR | --push]
#
#   --rev REV     core commit to export (default HEAD). The export reads REV, never the
#                 working copy, so it works from any worktree and with a dirty tree.
#   --remote URL  the SDK repository (default $HATAMOTO_SDK_REMOTE, else
#                 https://github.com/Zen-Labs-Corp/hatamoto-sdk.git; the public cut-over moves it)
#   --out DIR     materialize the result as a local git checkout in DIR (created or reused;
#                 branch main, tag v<Version>, origin = URL). No network: the previous state
#                 is whatever DIR already holds.
#   --push        push main and the tag to URL. Plain fast-forward pushes only: a remote that
#                 moved is refused, nothing is ever overwritten.
#   (neither)     dry run: read the remote and print what would happen.
#
# core stays the source of truth: the SDK repository is a one-way export whose tree is
# exactly backend/pkg/sdk at REV. The tag is v<Version> with Version taken from
# backend/pkg/sdk/version.go. The first release is a single parentless commit (no private
# history); later releases add one commit on top of the repository's main. A tag that already
# exists must point at the same tree (then nothing happens), else the run is refused.
# Commits in the public repository carry a fixed public identity as author, committer and
# Signed-off-by, never the local git identity: HATAMOTO_PUBLIC_NAME / HATAMOTO_PUBLIC_EMAIL,
# default "Zen Labs Corporation" / "release@hatamoto.dev". The run is refused if that identity
# matches a "ban" line of .neutral-private (when the file exists).
set -Eeuo pipefail

die() { echo "publish-sdk: $*" >&2; exit 1; }

rev=HEAD remote=${HATAMOTO_SDK_REMOTE:-https://github.com/Zen-Labs-Corp/hatamoto-sdk.git} out= push=0
while [ $# -gt 0 ]; do
  case "$1" in
    --rev) [ $# -ge 2 ] || die "--rev needs a value"; rev=$2; shift 2 ;;
    --remote) [ $# -ge 2 ] || die "--remote needs a value"; remote=$2; shift 2 ;;
    --out) [ $# -ge 2 ] || die "--out needs a value"; out=$2; shift 2 ;;
    --push) push=1; shift ;;
    -h|--help) sed -n '2,/^set /{/^set /!p}' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; sed -n '4p' "$0" >&2; exit 64 ;;
  esac
done
[ -z "$out" ] || [ "$push" = 0 ] || die "--out and --push are exclusive"

core=$(git rev-parse --show-toplevel) || die "run inside the core checkout"
cd "$core"
git rev-parse --verify -q "$rev^{commit}" >/dev/null || die "unknown revision $rev"
version=$(git show "$rev:backend/pkg/sdk/version.go" 2>/dev/null | sed -n 's/^const Version = "\(.*\)"$/\1/p') || true
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "cannot read a X.Y.Z const Version from backend/pkg/sdk/version.go at $rev"
tag=v$version
tree=$(git rev-parse "$rev:backend/pkg/sdk") || die "no backend/pkg/sdk at $rev"
name=${HATAMOTO_PUBLIC_NAME:-Zen Labs Corporation} email=${HATAMOTO_PUBLIC_EMAIL:-release@hatamoto.dev}
[ -n "$name" ] && [ -n "$email" ] || die "HATAMOTO_PUBLIC_NAME and HATAMOTO_PUBLIC_EMAIL must not be empty"
if [ -f "$core/.neutral-private" ]; then
  # Fails closed: an invalid pattern (grep status 2) refuses the run, and a last line without a newline still counts.
  while read -r kw pattern || [ -n "$kw" ]; do
    [ "$kw" = ban ] && [ -n "$pattern" ] || continue
    rc=0; grep -Eqi -e "$pattern" <<<"$name <$email>" || rc=$?
    [ "$rc" -ne 2 ] || die "refusing: invalid ban pattern '$pattern' in .neutral-private"
    [ "$rc" -ne 0 ] || die "refusing: the public identity '$name <$email>' matches a ban line of .neutral-private"
  done <"$core/.neutral-private"
fi

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

# G is the git dir the new objects and refs go to: the checkout in --out mode, else a
# scratch bare repository fed from the remote.
if [ -n "$out" ]; then
  mkdir -p "$out"
  out=$(cd "$out" && pwd)
  if [ ! -e "$out/.git" ]; then
    [ -z "$(ls -A "$out")" ] || die "$out exists, is not empty and is not a git checkout"
    git init -q -b main "$out"
  else
    [ -z "$(git -C "$out" status --porcelain)" ] || die "$out has uncommitted changes"
  fi
  G=$out/.git
  git --git-dir="$G" remote get-url origin >/dev/null 2>&1 \
    && git --git-dir="$G" remote set-url origin "$remote" \
    || git --git-dir="$G" remote add origin "$remote"
  tip_ref=refs/heads/main
else
  G=$scratch/repo.git
  git init -q --bare "$G"
  remote_refs=$(git ls-remote "$remote" refs/heads/main "refs/tags/$tag") || die "cannot read $remote"
  if grep -q "	refs/heads/main$" <<<"$remote_refs"; then
    git --git-dir="$G" fetch -q "$remote" refs/heads/main:refs/remotes/origin/main || die "cannot fetch main from $remote"
  fi
  if grep -q "	refs/tags/$tag$" <<<"$remote_refs"; then
    git --git-dir="$G" fetch -q "$remote" "refs/tags/$tag:refs/tags/$tag" || die "cannot fetch $tag from $remote"
  fi
  tip_ref=refs/remotes/origin/main
fi
g() { git --git-dir="$G" "$@"; }

# A tag that exists must carry the same tree: then there is nothing to do.
if existing=$(g rev-parse -q --verify "refs/tags/$tag^{tree}"); then
  [ "$existing" = "$tree" ] || die "refusing: $tag exists with a different tree (bump Version in backend/pkg/sdk/version.go before publishing a change)"
  echo "publish-sdk: $tag is already published with this tree; nothing to do"
  exit 0
fi

# Rebuild the tree from the plain files of REV (never from the core object store, so no
# core history reaches the SDK repository) and check it is byte for byte the same tree.
mkdir "$scratch/files"
git archive "$rev:backend/pkg/sdk" | tar -x -C "$scratch/files"
GIT_INDEX_FILE=$scratch/index g --work-tree="$scratch/files" add -A -f
built=$(GIT_INDEX_FILE=$scratch/index g write-tree)
[ "$built" = "$tree" ] || die "exported tree $built differs from backend/pkg/sdk at $rev ($tree)"

tip=$(g rev-parse -q --verify "$tip_ref^{commit}" || true)
if [ -n "$tip" ] && [ "$(g rev-parse "$tip^{tree}")" = "$tree" ]; then
  commit=$tip
  echo "publish-sdk: main already has this tree ($tip); tagging it $tag"
else
  msg=$(printf 'Release SDK %s\n\nExport of backend/pkg/sdk from the Hatamoto core repository.\n\nSigned-off-by: %s <%s>\n' "$tag" "$name" "$email")
  export GIT_AUTHOR_NAME=$name GIT_AUTHOR_EMAIL=$email GIT_COMMITTER_NAME=$name GIT_COMMITTER_EMAIL=$email
  when="$(date -u +%s) +0000"   # UTC: the local time zone offset would be a location signal in the public history
  export GIT_AUTHOR_DATE=$when GIT_COMMITTER_DATE=$when
  if [ -n "$tip" ]; then commit=$(g commit-tree "$tree" -p "$tip" -m "$msg")
  else commit=$(g commit-tree "$tree" -m "$msg"); fi
  echo "publish-sdk: new commit $commit ($([ -n "$tip" ] && echo "on top of $tip" || echo "first release, no parent")) for $tag"
fi

if [ -n "$out" ]; then
  g update-ref refs/heads/main "$commit"
  g update-ref "refs/tags/$tag" "$commit"
  g symbolic-ref HEAD refs/heads/main
  git -C "$out" reset -q --hard
  echo "publish-sdk: materialized $tag in $out (origin $remote)"
elif [ "$push" = 1 ]; then
  g update-ref "refs/tags/$tag" "$commit"
  # Plain pushes (fast-forward only), atomic so the tag never lands without main.
  g push --atomic "$remote" "$commit:refs/heads/main" "refs/tags/$tag:refs/tags/$tag" || die "push to $remote was refused or failed (see git's message above; if the remote moved, run again)"
  echo "publish-sdk: pushed $tag to $remote"
else
  echo "publish-sdk: dry run: would push $commit to main and tag $tag on $remote (use --push, or --out DIR for a local checkout)"
fi
