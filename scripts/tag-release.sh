#!/usr/bin/env bash
# tag-release.sh - create the signed release tags for a Hatamoto core release.
#
# Usage: scripts/tag-release.sh [--push] [--remote NAME] [--allowed-signers FILE] vX.Y.Z [REV]
#
#   vX.Y.Z   the product version (vMAJOR.MINOR.PATCH[-prerelease], what release.yml accepts)
#   REV      the commit to tag (default HEAD)
#   --push   push both tags in one atomic push (default: local tags only)
#
# Creates two SIGNED, annotated tags on REV:
#   vX.Y.Z          the product tag: pushing it starts .github/workflows/release.yml
#   backend/vX.Y.Z  the core Go module tag: hatamoto.dev/core lives in backend/, so
#                   `go get hatamoto.dev/core@vX.Y.Z` resolves the tag backend/vX.Y.Z
#                   (the go-import line in deploy/caddy/hatamoto.dev.caddy names the
#                   directory "backend"). It does not match v* and starts nothing.
# The SDK is NOT tagged here. It is exported to github.com/Zen-Labs-Corp/hatamoto-sdk
# by scripts/publish-sdk.sh, which tags v<Version> there (plain vA.B.C, root module).
#
# The tagger is always the public release identity, never the local git user:
# HATAMOTO_PUBLIC_NAME / HATAMOTO_PUBLIC_EMAIL, default "Zen Labs Corporation" /
# "release@hatamoto.dev" (the identity publish-sdk.sh uses). The key is the
# release tag key: HATAMOTO_TAG_SIGNING_KEY (path to its .pub, the private key
# next to it or in ssh-agent), else git config user.signingkey. Tags are always
# SSH-signed (gpg.format=ssh is set for these tags only).
#
# Refuses to run without a signing key, refuses existing tags and a REV
# that is not on origin/main, and checks every tag it made: a tag git did not
# sign, or one that fails against .github/allowed_signers (when that lists a
# signer), is deleted again and the script fails.
set -Eeuo pipefail

die() { echo "tag-release: $*" >&2; exit 1; }
usage() { sed -n '2,5p' "$0" >&2; exit 64; }

push=0 remote=origin signers=.github/allowed_signers ver='' rev=HEAD
while [ $# -gt 0 ]; do
  case "$1" in
    --push) push=1; shift ;;
    --remote) [ $# -ge 2 ] || usage; remote=$2; shift 2 ;;
    --allowed-signers) [ $# -ge 2 ] || usage; signers=$2; shift 2 ;;
    -h|--help) sed -n '2,31p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; usage ;;
    *) if [ -z "$ver" ]; then ver=$1; elif [ "$rev" = HEAD ]; then rev=$1; else usage; fi; shift ;;
  esac
done
[ -n "$ver" ] || usage
[[ "$ver" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]] || die "bad version: $ver (want vMAJOR.MINOR.PATCH[-prerelease])"

# A relative --allowed-signers is relative to where the command was run, not to
# the checkout root we move to next.
signers=$(realpath -m "$signers")
top=$(git rev-parse --show-toplevel) || die "run inside the core checkout"
cd "$top"
verify="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/verify-tag-signature.sh"

key=${HATAMOTO_TAG_SIGNING_KEY:-$(git config --get user.signingkey || true)}
[ -n "$key" ] || die "no signing key: set HATAMOTO_TAG_SIGNING_KEY (or git config user.signingkey); tags are never created unsigned"
name=${HATAMOTO_PUBLIC_NAME:-Zen Labs Corporation} email=${HATAMOTO_PUBLIC_EMAIL:-release@hatamoto.dev}
[ -n "$name" ] && [ -n "$email" ] || die "HATAMOTO_PUBLIC_NAME and HATAMOTO_PUBLIC_EMAIL must not be empty"
# The tagger is the committer identity; set it for these tags only.
export GIT_COMMITTER_NAME=$name GIT_COMMITTER_EMAIL=$email
signtag() { git -c gpg.format=ssh -c user.signingkey="$key" tag -s "$@"; }

commit=$(git rev-parse --verify -q "$rev^{commit}") || die "unknown revision: $rev"
# main is fetched first (a stale remote-tracking ref would judge against old
# history) and a missing ref is an error: the on-main rule is never skipped.
git fetch --quiet "$remote" "+refs/heads/main:refs/remotes/$remote/main" || die "cannot fetch main from $remote; release only what main has"
git rev-parse -q --verify "refs/remotes/$remote/main" >/dev/null || die "no $remote/main to compare with"
git merge-base --is-ancestor "$commit" "refs/remotes/$remote/main" || die "$rev is not on $remote/main; release only what main has"

tags=("$ver" "backend/$ver")
for t in "${tags[@]}"; do
  git rev-parse -q --verify "refs/tags/$t" >/dev/null && die "tag $t already exists"
done

made=()
undo() { for t in "${made[@]}"; do git tag -d "$t" >/dev/null; done; }
for t in "${tags[@]}"; do
  signtag "$t" -m "$t" "$commit" || { undo; die "git tag -s failed for $t; nothing was kept"; }
  made+=("$t")
  # git tag -s signs; this proves the object carries a signature and that a
  # listed signer made it. The allowed_signers check is a warning-only pass
  # while the file is still the placeholder.
  git cat-file tag "$t" | grep -q 'BEGIN SSH SIGNATURE' || { undo; die "$t is not signed; nothing was kept"; }
  bash "$verify" --allowed-signers "$signers" "$t" || { undo; die "$t failed verification; nothing was kept"; }
done
echo "tagged $(git log -1 --format='%h %s' "$commit")"
printf '  %s\n' "${tags[@]}"

if [ "$push" = 1 ]; then
  git push --atomic "$remote" "refs/tags/${tags[0]}" "refs/tags/${tags[1]}"
  echo "pushed ${tags[*]} to $remote (release.yml starts now)"
else
  echo "local only. Push both at once: git push --atomic $remote refs/tags/${tags[0]} refs/tags/${tags[1]}"
fi
