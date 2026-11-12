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
#   DEPLOY_TARGET     box (default; empty = box) or hmctl:<slug>; else .deploy-target
#                     (this checkout, else the main checkout). Cell mode uploads to
#                     staging and publishes with `hmctl site static --path releases/vX`
#                     (never --delete); the immutability checks read the published copy.
#   DEPLOY_STAGING    cell mode: host staging dir (default /root/hatamoto-deploy; a private
#                     0700 dir owned by the ssh user, one run.XXXXXX dir per run)
#   DEPLOY_STATIC_ROOT cell mode: published static root (default /srv/sites/<slug>/static)
#   DEPLOY_HMCTL      cell mode: hmctl command on the host (default hmctl)
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

# Cell target (resolved inline: this script must work with no sibling files).
CELL=0; SLUG=""; STAGE=""; HMCTL=hmctl
if [[ -z "${DEPLOY_TARGET+x}" ]]; then
  DEPLOY_TARGET=""
  MAIN_CHECKOUT=${MAIN_CHECKOUT:-$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")}
  for f in "$REPO/.deploy-target" "$MAIN_CHECKOUT/.deploy-target"; do
    if [[ -r "$f" ]]; then DEPLOY_TARGET=$(tr -d '[:space:]' < "$f"); break; fi
  done
fi
# Plain absolute paths only (no '..', '//', trailing slash); nested = equal, inside or above.
plain_path() { [[ "$1" =~ ^/([A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+$ && "$1/" != *"/../"* && "$1/" != *"/./"* ]]; }
nested() { [[ "$1/" == "$2/"* || "$2/" == "$1/"* ]]; }   # equal, inside or above
if [[ -n "$DEPLOY_TARGET" && "$DEPLOY_TARGET" != box ]]; then
  SLUG=${DEPLOY_TARGET#hmctl:}
  [[ "$DEPLOY_TARGET" == "hmctl:$SLUG" && "$SLUG" =~ ^[a-z][a-z0-9-]{1,28}$ && "$SLUG" != *- && "$SLUG" != *--* ]] \
    || die "bad DEPLOY_TARGET (want box or hmctl:<slug>)"
  CELL=1
  STAGING=${DEPLOY_STAGING:-/root/hatamoto-deploy}
  plain_path "$STAGING" || die "DEPLOY_STAGING must be a plain absolute path (no '..', no trailing slash)"
  STATIC_ROOT=${DEPLOY_STATIC_ROOT:-/srv/sites/$SLUG/static}
  plain_path "$STATIC_ROOT" || die "DEPLOY_STATIC_ROOT must be a plain absolute path (no '..', no trailing slash)"
  ! nested "$STAGING" "$STATIC_ROOT" || die "DEPLOY_STAGING must not equal, contain or sit inside DEPLOY_STATIC_ROOT"
  ! nested "$STAGING" /srv/sites || die "DEPLOY_STAGING must be outside /srv/sites"
  HMCTL=${DEPLOY_HMCTL:-hmctl}
  [[ "$HMCTL" =~ ^[A-Za-z0-9._/\ -]+$ ]] || die "bad DEPLOY_HMCTL"
  RDIR="$STATIC_ROOT/releases/$VER"   # published copy: what the immutability checks read
  STAGE=""                            # per-run dir under $STAGING, created right before the upload
fi
# Per-run private staging dir on the host (prints its path). The base must be a real directory
# owned by the ssh user (root, for hmctl) with mode 0700, so no other account can swap staged
# files; every run gets a fresh mktemp dir below it.
make_run_dir() {
  local out
  out=$(ssh "$DEPLOY_HOST" "umask 077; b='$STAGING'; mkdir -p -m 0700 -- \"\$b\" && [ -d \"\$b\" ] && [ ! -L \"\$b\" ] && [ -O \"\$b\" ] && [ \"\$(stat -c %a \"\$b\")\" = 700 ] && mktemp -d -- \"\$b/run.XXXXXX\"") \
    && [[ "$out" =~ ^"$STAGING"/run\.[A-Za-z0-9]+$ ]] || die "staging dir $STAGING on the host is not a private directory"
  echo "$out"
}

# 3. Verify offline: signature, version, size and hash of all four artifacts.
RELEASE_TOOL=${RELEASE_TOOL:-"go run ./tools/release"}
( cd "$REPO/backend" && $RELEASE_TOOL verify --chan edge --record "$DIR/release.json" \
    --sig "$DIR/release.json.sig" --dir "$DIR" --version "$VER" )

# 4. Compare with the server before touching it.
remote_hash() { ssh "$DEPLOY_HOST" "sha256sum -- '$RDIR/$1' 2>/dev/null" | awk '{print $1}' || true; }
if (( CELL )); then
  # hmctl overwrites what it publishes, so an unreadable published copy must never look absent:
  # prints the hash, or ABSENT for a missing file; any ssh or read error dies.
  # Fail closed first: under a missing or unlistable static root every file would look absent.
  ssh "$DEPLOY_HOST" "[ -d '$STATIC_ROOT' ] && [ -r '$STATIC_ROOT' ] && [ -x '$STATIC_ROOT' ] && ls -- '$STATIC_ROOT' >/dev/null" \
    || die "static root $STATIC_ROOT on the host is missing, not a directory or not readable by the ssh user; nothing was published"
  # Prints the hash, or ABSENT for a file that is not there; an unreadable path or any ssh error dies.
  # Absent means the path is missing under a directory we can search, never merely hidden by permissions.
  remote_hash() {
    local out
    out=$(ssh "$DEPLOY_HOST" "
      if [ -e '$RDIR/$1' ]; then sha256sum -- '$RDIR/$1'
      elif [ -d '$RDIR' ]; then [ -r '$RDIR' ] && [ -x '$RDIR' ] && echo ABSENT
      elif [ -e '$RDIR' ] || [ -L '$RDIR' ]; then exit 1
      elif [ -d '$STATIC_ROOT/releases' ]; then [ -r '$STATIC_ROOT/releases' ] && [ -x '$STATIC_ROOT/releases' ] && echo ABSENT
      elif [ -e '$STATIC_ROOT/releases' ] || [ -L '$STATIC_ROOT/releases' ]; then exit 1
      else echo ABSENT; fi" | awk '{print $1}') \
      && [[ "$out" =~ ^([0-9a-f]{64}|ABSENT)$ ]] || die "cannot read the published copy of $1 on the host; nothing was published"
    echo "$out"
  }
fi
todo=()
for f in "${FILES[@]}"; do
  local_h=$(sha256sum "$DIR/$f" | awk '{print $1}')
  remote_h=$(remote_hash "$f")
  if [[ -z "$remote_h" || "$remote_h" == ABSENT ]]; then todo+=("$f")
  elif [[ "$remote_h" == "$local_h" ]]; then echo "same:    $f"
  else die "$f differs from the published file on the server; releases are immutable, nothing was uploaded"
  fi
done

# 5. Upload in order: artifacts, signature, record last. rsync sorts one
# invocation's file list, so each group is its own call.
upload() { # upload <file>...
  local dest=$RDIR; if (( CELL )); then dest=$STAGE; fi
  if (( DRY )); then echo "DRY: rsync -t --no-links --ignore-existing $* $DEPLOY_HOST:$dest/"; return; fi
  ( cd "$DIR" && rsync -t --no-links --ignore-existing "$@" "$DEPLOY_HOST:$dest/" )
}
if (( ${#todo[@]} == 0 )); then
  echo "already published: $VER (nothing to upload)"
else
  if (( CELL )); then
    # fresh private dir each run: leftovers of a failed run must not be published
    if (( DRY )); then STAGE=$STAGING/run.XXXXXX; echo "DRY: ssh $DEPLOY_HOST mkdir -m 0700 $STAGE"
    else STAGE=$(make_run_dir); fi
  else
    (( DRY )) && echo "DRY: ssh $DEPLOY_HOST mkdir -p $RDIR" || ssh "$DEPLOY_HOST" "mkdir -p -- '$RDIR'"
  fi
  group=()
  for f in "${todo[@]}"; do
    if [[ "$f" == hatamoto-* ]]; then group+=("$f"); fi
  done
  # Cell: hmctl rsyncs the whole staging dir in one sorted pass, so publish in the box order with
  # one `site static` per stage: artifacts and signature first, release.json last.
  publish_cell() { # publish_cell <file>...: re-check them, then hmctl publishes the staging dir
    if (( DRY )); then echo "DRY: hmctl site static ... --from $STAGE --path releases/$VER"; return; fi
    local f   # hmctl overwrites: nothing may have been published since step 4
    for f in "$@"; do
      [[ "$(remote_hash "$f")" == ABSENT ]] || die "$f appeared in the published copy during the run; releases are immutable, it was not overwritten"
    done
    ssh "$DEPLOY_HOST" "$HMCTL site static $SLUG --from '$STAGE' --path releases/$VER"
  }
  early=()   # everything but release.json, in upload order
  if (( ${#group[@]} )); then upload "${group[@]}"; early+=("${group[@]}"); fi
  for t in "${todo[@]}"; do
    if [[ "$t" == release.json.sig ]]; then upload release.json.sig; early+=("$t"); fi
  done
  if (( CELL && ${#early[@]} )); then publish_cell "${early[@]}"; fi
  for t in "${todo[@]}"; do
    if [[ "$t" == release.json ]]; then
      upload release.json
      if (( CELL )); then publish_cell release.json; fi
    fi
  done
  if (( CELL && !DRY )); then ssh "$DEPLOY_HOST" "rm -rf -- '$STAGE'" || true; fi
fi

# 6. Re-hash on the server.
if (( DRY )); then echo "DRY: would re-hash ${#FILES[@]} files on the server"; exit 0; fi
for f in "${FILES[@]}"; do
  [[ "$(remote_hash "$f")" == "$(sha256sum "$DIR/$f" | awk '{print $1}')" ]] || die "after upload, $f on the server does not match"
done
echo "published $VER (${#FILES[@]} files)"
