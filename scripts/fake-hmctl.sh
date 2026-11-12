#!/usr/bin/env bash
# fake-hmctl.sh - test double for `hmctl`, shared by the cell-mode shell tests.
#
# Install it as `hmctl` in a stub dir (cp scripts/fake-hmctl.sh $STUBS/hmctl) and
# put that dir on PATH. Every call is logged as `hmctl <argv>` to $STUB_LOG.
#
# The matching fake ssh must: skip `-o X` pairs and `-T`, drop the host argument,
# then run the remaining single string with `sh -c "$*"` (PATH carries the fake).
#
# Env knobs:
#   FAKE_STATE=running          STATE column of `site status`
#   FAKE_HEALTH=ok              HEALTH column (FAKE_HEALTH_FAIL=1 forces `down`)
#   FAKE_GOOSE=5:20260101       answer to the goose_db_version query; the answer
#                               lives in $FAKE_GOOSE_FILE (default $STUB_LOG.goose)
#                               once `site run -- migrate` bumped it
#   FAKE_MIGRATE_BUMPS=1        `site run <slug> -- migrate` bumps the goose answer
#   FAKE_MEDIA=                 answer to the newest-media query
#   FAKE_BIN_STATE=<file>       `site binary --from F` copies F here; --clear removes it
#   FAKE_MARK=<file>            `site mark` store (one value; get exits 1 when absent)
#   FAKE_STATIC=<dir>           `site static --from D --path P` rsyncs D/ to $FAKE_STATIC/P/
#                               (exits 3 without --path: that would be a destructive mirror)
set -Eeuo pipefail

: "${STUB_LOG:?fake-hmctl: STUB_LOG not set}"
echo "hmctl $*" >>"$STUB_LOG"

[[ ${1:-} == site ]] || { echo "fake-hmctl: unsupported: $*" >&2; exit 2; }
verb=${2:-}; slug=${3:-}
shift 3 || true
goose_file=${FAKE_GOOSE_FILE:-$STUB_LOG.goose}

case $verb in
  status)
    health=${FAKE_HEALTH:-ok}; [[ ${FAKE_HEALTH_FAIL:-} == 1 ]] && health=down
    echo "SITE  STATE  MEM  CPU  BACKUP  VERSION  UPDATE  HEALTH"
    echo "$slug  ${FAKE_STATE:-running}  1M  1%  3h  v1  ok  $health"
    ;;
  backup)
    echo "ok  backup      20260101T000000Z-manual uploaded to sites/$slug/backups/20260101T000000Z-manual.tar"
    ;;
  psql)
    sql=$(cat)
    echo "$sql" >>"$STUB_LOG.stdin"
    # real psql prints each \pset status line unless QUIET is on
    if [[ $sql != *'\set QUIET on'* ]]; then echo "Tuples only is on."; echo "Output format is unaligned."; fi
    case $sql in
      *goose_db_version*) if [[ -f $goose_file ]]; then cat "$goose_file"; else echo "${FAKE_GOOSE:-5:20260101}"; fi ;;
      *hatamoto_media*) [[ -z ${FAKE_MEDIA:-} ]] || echo "$FAKE_MEDIA" ;;
    esac
    ;;
  binary)
    case ${1:-} in
      --from) cp "$2" "${FAKE_BIN_STATE:?fake-hmctl: FAKE_BIN_STATE not set}" ;;
      --clear) rm -f "${FAKE_BIN_STATE:-/dev/null}" ;;
    esac
    ;;
  run)
    [[ ${1:-} == -- ]] || { echo "fake-hmctl: usage: site run <slug> -- <args>" >&2; exit 2; }
    if [[ ${2:-} == migrate && ${FAKE_MIGRATE_BUMPS:-} == 1 ]]; then echo "6:20260102" >"$goose_file"; fi
    ;;
  mark)
    store=${FAKE_MARK:?fake-hmctl: FAKE_MARK not set}
    if [[ $# -ge 2 ]]; then printf '%s\n' "$2" >"$store"
    elif [[ -f $store ]]; then cat "$store"
    else echo "hmctl: site $slug has no mark \"${1:-}\"" >&2; exit 1; fi
    ;;
  static)
    from= path=
    while [[ $# -gt 0 ]]; do
      case $1 in --from) from=$2; shift ;; --path) path=$2; shift ;; esac
      shift
    done
    [[ -n $path ]] || { echo "fake-hmctl: static without --path would mirror and delete" >&2; exit 3; }
    mkdir -p "${FAKE_STATIC:?fake-hmctl: FAKE_STATIC not set}/$path"
    rsync -rlt "$from/" "$FAKE_STATIC/$path/"
    ;;
  *) echo "fake-hmctl: unsupported verb: $verb" >&2; exit 2 ;;
esac
