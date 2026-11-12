#!/usr/bin/env bash
# publish-registry.sh - upload the built plugin registry to the host.
#
# Usage:
#     scripts/publish-registry.sh [--dry-run]
#
# Run after `make registry-pull-archive` and `make registry`. Uploads, in order:
#   1. registry/archive/   (append-only: a file already on the host with a
#                           different sha256 aborts before anything is uploaded)
#   2. registry/packages/  3. registry/screenshots/ (only when it exists: theme
#                           screenshots from build-community-themes.sh)
#   4. registry/plugins/ (only when it exists: the static directory page from
#                           `hatamoto registry page` is optional, a site can
#                           show the plugin directory as a CMS page instead)
#   5. registry/registry.json (last, so the index never points at a missing file)
#
# Env (all optional):
#   DEPLOY_HOST       ssh host alias; else read from .deploy-host (this checkout,
#                     else the main checkout)
#   REMOTE_REGISTRY   default /opt/hatamoto/registry
#   ARCHIVE_DIR       default registry/archive
#   DEPLOY_TARGET     box (default) or hmctl:<slug> (see scripts/deploy-target.sh).
#                     Cell mode stages the whole tree under DEPLOY_STAGING/registry
#                     (default /var/tmp/hatamoto-deploy) and publishes it with
#                     `hmctl site static --path registry` (never --delete); the
#                     archive check reads the published copy under DEPLOY_STATIC_ROOT
#                     (default /srv/sites/<slug>/static). DEPLOY_HMCTL overrides hmctl.
set -Eeuo pipefail

die() { echo "publish-registry: $*" >&2; exit 1; }

DRY=0
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    -h|--help) sed -n '2,27p' "$0"; exit 0 ;;
    *) echo "unknown argument: $a" >&2; sed -n '4,5p' "$0" >&2; exit 64 ;;
  esac
done

REPO=$(git rev-parse --show-toplevel)
cd "$REPO"
DEPLOY_HOST=$("$(dirname "${BASH_SOURCE[0]}")/deploy-host.sh") || exit 1
REMOTE=${REMOTE_REGISTRY:-/opt/hatamoto/registry}
[[ "$REMOTE" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "REMOTE_REGISTRY must be a plain absolute path"
ARCHIVE=${ARCHIVE_DIR:-registry/archive}

# Target: box (REMOTE is the served directory) or an hmctl cell (REMOTE is staging).
TARGET=$("$(dirname "${BASH_SOURCE[0]}")/deploy-target.sh") || exit 1
CELL=0; SLUG=""; PUBROOT=""; STATIC_ROOT=""; HMCTL=hmctl
if [[ "$TARGET" != box ]]; then
  CELL=1; SLUG=${TARGET#hmctl }
  STAGING=${DEPLOY_STAGING:-/var/tmp/hatamoto-deploy}
  [[ "$STAGING" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "DEPLOY_STAGING must be a plain absolute path"
  STATIC_ROOT=${DEPLOY_STATIC_ROOT:-/srv/sites/$SLUG/static}
  [[ "$STATIC_ROOT" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "DEPLOY_STATIC_ROOT must be a plain absolute path"
  HMCTL=${DEPLOY_HMCTL:-hmctl}
  [[ "$HMCTL" =~ ^[A-Za-z0-9._/\ -]+$ ]] || die "bad DEPLOY_HMCTL"
  PUBROOT=$STATIC_ROOT/registry   # published copy: what the archive check reads
  REMOTE=$STAGING/registry        # everything is uploaded here, then published
fi

[[ -s registry/registry.json && -d registry/packages ]] || die "registry/ is not built: run make registry first"
plugins_page=()
[[ ! -d registry/plugins ]] || plugins_page=(registry/plugins)
screenshots=()
[[ ! -d registry/screenshots ]] || screenshots=(registry/screenshots)
[[ -z "$(find registry/packages "${screenshots[@]}" "${plugins_page[@]}" -type l -print -quit)" ]] || die "symlinks are not allowed in registry/"

# Archive: preflight against the host, nothing is uploaded when a file differs.
archive_files=()
if [[ -d "$ARCHIVE" ]]; then
  [[ -z "$(find "$ARCHIVE" ! -type d ! -type f -print -quit)" ]] || die "$ARCHIVE holds something other than regular files"
  ARCHIVE=$(cd "$ARCHIVE" && pwd)
  while IFS= read -r -d '' f; do archive_files+=("${f#"$ARCHIVE"/}"); done < <(cd "$ARCHIVE" && find . -type f -print0 | LC_ALL=C sort -z | sed -z 's|^\./||')
  # These names go into remote shell strings (sha256sum below): plain path characters only.
  for f in "${archive_files[@]}"; do
    [[ "$f" =~ ^[A-Za-z0-9._+-]+(/[A-Za-z0-9._+-]+)*$ && "$f" != *..* ]] || die "unsafe file name in $ARCHIVE: $f"
  done
fi
declare -A remote_h=()
if (( CELL )); then
  # Fail closed: a missing published archive counts as empty only while the static root itself exists.
  pub_sums=$(ssh "$DEPLOY_HOST" "if [ -d '$PUBROOT/archive' ]; then cd '$PUBROOT/archive' && find . -type f -exec sha256sum -- {} +; elif [ ! -d '$STATIC_ROOT' ]; then exit 1; fi") \
    || die "cannot read the published archive under $STATIC_ROOT on the host"
  while read -r h f; do [[ -n "${f:-}" ]] && remote_h["${f#./}"]=$h; done <<<"$pub_sums"
else
  while read -r h f; do [[ -n "${f:-}" ]] && remote_h["${f#./}"]=$h; done \
    < <(ssh "$DEPLOY_HOST" "cd '$REMOTE/archive' 2>/dev/null && find . -type f -exec sha256sum -- {} +" || true)
fi
declare -A local_set=()
for f in "${archive_files[@]}"; do local_set["$f"]=1; done
missing_local=0
for f in "${!remote_h[@]}"; do
  [[ -n "${local_set[$f]:-}" ]] || { echo "remote:  archive/$f is not in $ARCHIVE" >&2; missing_local=1; }
done
(( ! missing_local )) || echo "publish-registry: the host archive has files this checkout lacks: run make registry-pull-archive first" >&2
todo=()
for f in "${archive_files[@]}"; do
  local_h=$(sha256sum "$ARCHIVE/$f" | awk '{print $1}')
  r=${remote_h[$f]:-}
  if [[ -z "$r" ]]; then todo+=("$f")
  elif [[ "$r" == "$local_h" ]]; then echo "same:    archive/$f"
  else die "archive/$f differs from the file on the host; the archive is append-only, nothing was uploaded (run make registry-pull-archive first if this checkout is stale)"
  fi
done

up() { # up <rsync-args...> <src> <dest-subpath>
  local dest=${*: -1}; local args=("${@:1:$#-1}")
  if (( DRY )); then echo "DRY: rsync -rt --no-links ${args[*]} $DEPLOY_HOST:$REMOTE/$dest"; return; fi
  rsync -rt --no-links "${args[@]}" "$DEPLOY_HOST:$REMOTE/$dest"
}
if (( CELL )); then
  # fresh staging tree each run: leftovers of a failed run must not be published
  if (( DRY )); then echo "DRY: ssh $DEPLOY_HOST rm -rf $REMOTE && mkdir -p $REMOTE/archive"
  else ssh "$DEPLOY_HOST" "rm -rf -- '$REMOTE' && mkdir -p -- '$REMOTE/archive'"; fi
elif (( DRY )); then echo "DRY: ssh $DEPLOY_HOST mkdir -p $REMOTE/archive"; else ssh "$DEPLOY_HOST" "mkdir -p -- '$REMOTE/archive'"; fi
if (( ${#todo[@]} )); then
  printf 'new:     archive/%s\n' "${todo[@]}"
  up --ignore-existing "$ARCHIVE/" archive/
else
  echo "archive: nothing new"
fi

# Verify the uploaded archive files by hash before the index goes out.
if (( ! DRY && ${#todo[@]} )); then
  for f in "${todo[@]}"; do
    local_h=$(sha256sum "$ARCHIVE/$f" | awk '{print $1}')
    got=$(ssh "$DEPLOY_HOST" "sha256sum -- '$REMOTE/archive/$f'" | awk '{print $1}')
    [[ "$got" == "$local_h" ]] || die "archive/$f hash mismatch after upload"
  done
fi

up registry/packages/ packages/
if (( ${#screenshots[@]} )); then up registry/screenshots/ screenshots/; fi
if (( ${#plugins_page[@]} )); then up registry/plugins/ plugins/; fi
up registry/registry.json registry.json
if (( CELL )); then   # publish the registry sub-path only: no --delete, the rest of the site is untouched
  if (( DRY )); then echo "DRY: hmctl site static ... --from $REMOTE --path registry"
  else ssh "$DEPLOY_HOST" "$HMCTL site static $SLUG --from '$REMOTE' --path registry"; fi
fi
echo "published registry to $DEPLOY_HOST:$REMOTE$( (( DRY )) && echo ' (dry run)')"
