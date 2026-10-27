#!/usr/bin/env bash
# check-channel-freshness.sh - fail loudly when a signed channel manifest is
# close to expiring.
#
# Usage: scripts/check-channel-freshness.sh [--base URL] [--min-days N] [CHANNEL...]
#
#   --base URL     where the signed manifests are served (default
#                  https://hatamoto.dev/releases/channels; file:// works)
#   --min-days N   fail when expires_at is less than N days away (default 7)
#   CHANNEL...     default: canary edge stable
#
# A manifest (its expires_at is in the signed "index" object) is signed for 14 days and re-signed daily, so a healthy channel is
# never closer than 13 days to expiry. Under 7 days left means a week of
# re-signs failed: boxes stop updating when it reaches zero ("release manifest has
# expired"). Reads the LIVE files, so it also catches a broken upload, not only
# a broken signer. Exit 1 on any stale, missing or unreadable manifest; 0 when all
# are fresh. Needs curl, jq and GNU date. It runs weekly from the ops repository's
# channels-freshness workflow (docs/site/migrate-contribute/release-process.md).
set -Eeuo pipefail

base=https://hatamoto.dev/releases/channels min=7 chans=()
while [ $# -gt 0 ]; do
  case "$1" in
    --base) [ $# -ge 2 ] || { sed -n '4,5p' "$0" >&2; exit 64; }; base=${2%/}; shift 2 ;;
    --min-days) [ $# -ge 2 ] || { sed -n '4,5p' "$0" >&2; exit 64; }; min=$2; shift 2 ;;
    -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; exit 64 ;;
    *) chans+=("$1"); shift ;;
  esac
done
[[ "$min" =~ ^[0-9]+$ ]] || { echo "--min-days must be a whole number" >&2; exit 64; }
[ ${#chans[@]} -gt 0 ] || chans=(canary edge stable)

now=$(date -u +%s) bad=0
for c in "${chans[@]}"; do
  [[ "$c" =~ ^[a-z]+$ ]] || { echo "bad channel name: $c" >&2; exit 64; }
  if ! doc=$(curl -fsS --max-time 30 "$base/$c.json" 2>&1); then
    echo "::error title=channel $c::cannot fetch $base/$c.json: $doc"; bad=1; continue
  fi
  exp=$(jq -r '.index.expires_at // empty' <<<"$doc" 2>/dev/null || true)
  if [ -z "$exp" ] || ! at=$(date -u -d "$exp" +%s 2>/dev/null); then
    echo "::error title=channel $c::$c.json has no readable expires_at (got '${exp:-nothing}')"; bad=1; continue
  fi
  left=$(( (at - now) / 86400 ))
  if [ $(( at - now )) -lt $(( min * 86400 )) ]; then
    echo "::error title=channel $c expires soon::$c.json expires $exp (${left}d left, minimum ${min}d): re-sign now (docs: Release process, channel freshness)"; bad=1
  else
    echo "ok   $c: expires $exp (${left}d left)"
  fi
done
[ "$bad" = 0 ] || { echo "channel freshness check FAILED" >&2; exit 1; }
