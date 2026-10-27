#!/usr/bin/env bash
# verify-tag-signature.sh - check that a release tag is an annotated tag signed
# with an SSH key listed in an allowed_signers file.
#
# Usage: scripts/verify-tag-signature.sh [--require] [--allowed-signers FILE] TAG
#
#   --allowed-signers FILE  default .github/allowed_signers (in the current repo)
#   --require               fail when FILE lists no key (default: warn and pass)
#
# Exit 0 when the tag verifies, 1 when it does not, 64 on bad usage.
#
# Enforcement follows the file. allowed_signers now holds one key line (the
# release owner's software key), so enforcement is on: every tag must be signed
# by a listed key. A file with only comments would have nobody to check against,
# so the script would say so and exit 0 (unless --require is given). release.yml calls this before it builds.
# The namespace is "git" (what `git tag -s` signs with gpg.format=ssh).
set -Eeuo pipefail

die() { echo "verify-tag-signature: $*" >&2; exit 1; }
usage() { sed -n '2,8p' "$0" >&2; exit 64; }

require=0 file=.github/allowed_signers tag=""
while [ $# -gt 0 ]; do
  case "$1" in
    --require) require=1; shift ;;
    --allowed-signers) [ $# -ge 2 ] || usage; file=$2; shift 2 ;;
    -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
    -*) usage ;;
    *) [ -z "$tag" ] || usage; tag=$1; shift ;;
  esac
done
[ -n "$tag" ] || usage

git rev-parse -q --verify "refs/tags/$tag" >/dev/null || die "no tag $tag"
if [ -f "$file" ]; then entries=$(grep -cvE '^[[:space:]]*(#|$)' "$file" || true); else entries=0; fi
if [ "$entries" = 0 ]; then
  if [ "$require" = 1 ]; then die "$file lists no signer, so $tag cannot be verified"; fi
  echo "::warning::tag signatures are NOT enforced: $file lists no signer yet (add the release key to turn it on)"
  echo "verify-tag-signature: $tag not checked (no signer in $file)"
  exit 0
fi

[ "$(git cat-file -t "refs/tags/$tag")" = tag ] || die "$tag is a lightweight tag; a signed annotated tag is required"
out=$(git -c gpg.format=ssh -c "gpg.ssh.allowedSignersFile=$file" tag -v "$tag" 2>&1) || {
  echo "$out" >&2
  die "$tag has no valid signature from a signer in $file"
}
echo "$out" | grep -E 'Good "git" signature' || true
echo "verify-tag-signature: $tag is signed by a listed signer"
