#!/usr/bin/env bash
# publish-install.sh - publish the attested install.sh of a release to the
# server that serves https://hatamoto.dev/install.sh.
#
# Usage: scripts/publish-install.sh vX.Y.Z [--dry-run]
#
# Run from a checkout of exactly that tag (HEAD must be the tag, tree clean).
# Order: build install.sh from the tree (deploy/install/build-install.sh),
# download the install.sh asset of the GitHub release, abort unless both have
# the same sha256 (the hosted copy is the attested one), copy to a temporary
# name on the server, re-hash it there, then rename over install.sh (atomic: a
# half-written script is never served to `curl | sh`). Any failure before the
# rename leaves the served file untouched. --dry-run prints the steps only.
#
# Environment:
#   DEPLOY_HOST      ssh host alias; else .deploy-host (see scripts/deploy-host.sh)
#   REMOTE_PUBLIC    server directory (default /opt/hatamoto/public)
set -Eeuo pipefail

usage() { sed -n '5p' "$0" >&2; exit 64; }
die() { echo "publish-install: $*" >&2; exit 1; }

DRY=0; TAG=""
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    -h|--help) sed -n '2,16p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $a" >&2; usage ;;
    *) if [[ -z "$TAG" ]]; then TAG=$a; else usage; fi ;;
  esac
done
[[ -n "$TAG" ]] || usage
[[ "$TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]] || die "bad tag: $TAG"

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(git rev-parse --show-toplevel)
REMOTE=${REMOTE_PUBLIC:-/opt/hatamoto/public}
[[ "$REMOTE" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "REMOTE_PUBLIC must be a plain absolute path"

# 1. The tree must be the tag, unmodified.
[[ "$(git -C "$REPO" describe --exact-match --tags 2>/dev/null || true)" == "$TAG" ]] || die "HEAD is not tagged $TAG; check out the tag first"
[[ -z "$(git -C "$REPO" status --porcelain)" ]] || die "working tree is not clean"

# 2. Host (never printed, never stored in the repo).
HOST=$("$HERE/deploy-host.sh") || die "no deploy host"

if (( DRY )); then
  echo "DRY: bash deploy/install/build-install.sh --out <tmp>/built"
  echo "DRY: gh release download $TAG --pattern install.sh --dir <tmp>/asset"
  echo "DRY: compare sha256 of the built file and the release asset"
  echo "DRY: scp install.sh <host>:$REMOTE/.install.sh.new"
  echo "DRY: ssh <host> sha256sum $REMOTE/.install.sh.new (must match)"
  echo "DRY: ssh <host> mv -f $REMOTE/.install.sh.new $REMOTE/install.sh"
  exit 0
fi

T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
sha() { sha256sum "$1" | awk '{print $1}'; }

# 3. Build at the tag, fetch the attested asset, compare.
( cd "$REPO" && bash deploy/install/build-install.sh --out "$T/built" ) >/dev/null
[[ -f "$T/built/install.sh" ]] || die "build-install.sh did not produce install.sh"
gh release download "$TAG" --pattern install.sh --dir "$T/asset"
[[ -f "$T/asset/install.sh" ]] || die "release $TAG has no install.sh asset"
want=$(sha "$T/built/install.sh")
[[ "$(sha "$T/asset/install.sh")" == "$want" ]] || die "built install.sh differs from the $TAG release asset; nothing was uploaded"

# 4. Upload under a temporary name, verify there, then rename.
scp -q "$T/asset/install.sh" "$HOST:$REMOTE/.install.sh.new"
got=$(ssh "$HOST" "sha256sum -- '$REMOTE/.install.sh.new'" | awk '{print $1}')
if [[ "$got" != "$want" ]]; then
  ssh "$HOST" "rm -f -- '$REMOTE/.install.sh.new'" || true
  die "after upload, the server copy does not match; install.sh was not replaced"
fi
ssh "$HOST" "chmod 0644 -- '$REMOTE/.install.sh.new' && mv -f -- '$REMOTE/.install.sh.new' '$REMOTE/install.sh'"
echo "published install.sh from $TAG (sha256 $want)"
