#!/usr/bin/env bash
# publish-release.sh - upload one signed release to the server that serves
# /releases (see docs/site/migrate-contribute/release-process.md).
#
# Usage: scripts/publish-release.sh [--dry-run] vX.Y.Z DIR
#
# DIR holds exactly release.json, release.json.sig and the four
# hatamoto-<os>-<arch> files; anything else (a stray *.key included) aborts.
# Order: validate, verify the signed record and all four artifacts with
# tools/release (no network), compare with what the server already has (a
# different file aborts before any upload; an identical one is skipped), upload
# artifacts, then the signature, then release.json last (a present record means
# a complete release), then re-hash on the server. Files are never overwritten.
# It never reads a key: the signature was made offline beforehand.
#
# Environment:
#   DEPLOY_HOST       ssh host alias; else .deploy-host (this checkout, else the
#                     main checkout). There is no default.
#   REMOTE_RELEASES   server directory (default /opt/hatamoto/public/releases)
#   RELEASE_TOOL      verifier command, run from backend/ (default: go run ./tools/release)
set -Eeuo pipefail

usage() { sed -n '5,6p' "$0" >&2; exit 64; }
die() { echo "publish-release: $*" >&2; exit 1; }

DRY=0; VER=""; DIR=""
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $a" >&2; usage ;;
    *) if [[ -z "$VER" ]]; then VER=$a; elif [[ -z "$DIR" ]]; then DIR=$a; else usage; fi ;;
  esac
done
[[ -n "$VER" && -n "$DIR" ]] || usage
[[ "$VER" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]] || die "bad version: $VER"
[[ -d "$DIR" ]] || die "not a directory: $DIR"
DIR=$(cd "$DIR" && pwd)

ARTIFACTS=(hatamoto-linux-amd64 hatamoto-linux-arm64 hatamoto-darwin-amd64 hatamoto-darwin-arm64)
FILES=("${ARTIFACTS[@]}" release.json.sig release.json)   # upload order

# 1. DIR holds exactly FILES, all regular files.
want=$(printf '%s\n' "${FILES[@]}" | LC_ALL=C sort)
have=$(ls -A "$DIR" | LC_ALL=C sort)
[[ "$want" == "$have" ]] || die "$DIR must hold exactly: ${FILES[*]} (found: $(tr '\n' ' ' <<<"$have"))"
for f in "${FILES[@]}"; do
  [[ -f "$DIR/$f" && ! -L "$DIR/$f" ]] || die "$f is not a regular file"
done

# 2. Host.
REPO=$(git rev-parse --show-toplevel)
if [[ -z "${DEPLOY_HOST:-}" ]]; then
  MAIN_CHECKOUT=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
  for f in "$REPO/.deploy-host" "$MAIN_CHECKOUT/.deploy-host"; do
    if [[ -r "$f" ]]; then DEPLOY_HOST=$(tr -d '[:space:]' < "$f"); break; fi
  done
fi
DEPLOY_HOST=${DEPLOY_HOST:-}
[[ -n "$DEPLOY_HOST" ]] || die "set DEPLOY_HOST (ssh host alias) or write it to .deploy-host"
REMOTE=${REMOTE_RELEASES:-/opt/hatamoto/public/releases}
[[ "$REMOTE" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "REMOTE_RELEASES must be a plain absolute path"
RDIR="$REMOTE/$VER"

# 3. Verify offline: signature, version, size and hash of all four artifacts.
RELEASE_TOOL=${RELEASE_TOOL:-"go run ./tools/release"}
( cd "$REPO/backend" && $RELEASE_TOOL verify --chan edge --record "$DIR/release.json" \
    --sig "$DIR/release.json.sig" --dir "$DIR" --version "$VER" )

# 4. Compare with the server before touching it.
remote_hash() { ssh "$DEPLOY_HOST" "sha256sum -- '$RDIR/$1' 2>/dev/null" | awk '{print $1}' || true; }
todo=()
for f in "${FILES[@]}"; do
  local_h=$(sha256sum "$DIR/$f" | awk '{print $1}')
  remote_h=$(remote_hash "$f")
  if [[ -z "$remote_h" ]]; then todo+=("$f")
  elif [[ "$remote_h" == "$local_h" ]]; then echo "same:    $f"
  else die "$f differs from the published file on the server; releases are immutable, nothing was uploaded"
  fi
done

# 5. Upload in order: artifacts, signature, record last. rsync sorts one
# invocation's file list, so each group is its own call.
upload() { # upload <file>...
  if (( DRY )); then echo "DRY: rsync -t --no-links --ignore-existing $* $DEPLOY_HOST:$RDIR/"; return; fi
  ( cd "$DIR" && rsync -t --no-links --ignore-existing "$@" "$DEPLOY_HOST:$RDIR/" )
}
if (( ${#todo[@]} == 0 )); then
  echo "already published: $VER (nothing to upload)"
else
  (( DRY )) && echo "DRY: ssh $DEPLOY_HOST mkdir -p $RDIR" || ssh "$DEPLOY_HOST" "mkdir -p -- '$RDIR'"
  group=()
  for f in "${todo[@]}"; do
    if [[ "$f" == hatamoto-* ]]; then group+=("$f"); fi
  done
  if (( ${#group[@]} )); then upload "${group[@]}"; fi
  for f in release.json.sig release.json; do
    for t in "${todo[@]}"; do if [[ "$t" == "$f" ]]; then upload "$f"; fi; done
  done
fi

# 6. Re-hash on the server.
if (( DRY )); then echo "DRY: would re-hash ${#FILES[@]} files on the server"; exit 0; fi
for f in "${FILES[@]}"; do
  [[ "$(remote_hash "$f")" == "$(sha256sum "$DIR/$f" | awk '{print $1}')" ]] || die "after upload, $f on the server does not match"
done
echo "published $VER (${#FILES[@]} files)"
