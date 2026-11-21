#!/usr/bin/env bash
# edition-artifacts.sh - bring the builds of other editions into a release dist/ dir, verified.
#
# Usage:
#   scripts/edition-artifacts.sh fetch   --tag vX.Y.Z --sha SHA --dist DIR   dispatch the edition build, wait for its artifact, verify, copy
#   scripts/edition-artifacts.sh local   --tag vX.Y.Z --from SRC --dist DIR  verify a local edition build dir, copy
#   scripts/edition-artifacts.sh require --record FILE                       every opted-in edition has all four platforms in the record
#
# Environment:
#   RELEASE_EDITIONS      space/comma list of edition names; empty => every subcommand prints "skipped", exit 0
#   EDITION_REPO          owner/name of the edition build repository (fetch only)
#   GH_TOKEN              token that may dispatch to and read the Actions artifacts of EDITION_REPO (fetch only; never printed)
#   EDITION_WAIT_SECONDS  how long to wait for the artifact (default 2700)
#   EDITION_POLL_SECONDS  poll interval (default 30)
#   EDITION_EVENT         repository_dispatch event type (default edition-build)
#
# Contract for the edition build: on the dispatch (client_payload.tag, client_payload.sha) it uploads ONE
# Actions artifact named edition-builds-<tag> holding hatamoto-<edition>-<platform> for every opted-in
# edition and platform, plus one SHA256SUMS listing exactly those files.
# dist/ is touched only after the whole source verified; an existing file is never overwritten.
set -Eeuo pipefail

PLATFORMS=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64) # = Makefile release-build
usage() { sed -n '4,7p' "$0" >&2; exit 64; }
die() { echo "edition-artifacts: $*" >&2; exit 1; }

[[ $# -ge 1 ]] || usage
cmd=$1; shift
tag= sha= dist= from= rec=
while [[ $# -gt 0 ]]; do
  [[ $# -ge 2 ]] || usage
  case $1 in
    --tag) tag=$2 ;; --sha) sha=$2 ;; --dist) dist=$2 ;; --from) from=$2 ;; --record) rec=$2 ;;
    *) usage ;;
  esac
  shift 2
done

eds=$(tr ',' ' ' <<<"${RELEASE_EDITIONS:-}" | xargs || true)
if [[ -z $eds ]]; then echo "no edition opt-in (RELEASE_EDITIONS empty): skipped"; exit 0; fi
for e in $eds; do [[ $e =~ ^[a-z][a-z0-9]*$ ]] || die "bad edition name: $e"; done

need_tag() { [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || die "bad or missing --tag"; }

# verify_copy <src> <dist>: exact file set, regular non-empty files, sha256sum -c --strict,
# no existing target in dist, then copy and re-hash there.
verify_copy() {
  local src=$1 out=$2 want=() e p f n have exp msg
  [[ -d $src ]] || die "no edition build dir: $src"
  [[ -d $out ]] || die "no dist dir: $out"
  for e in $eds; do for p in "${PLATFORMS[@]}"; do want+=("hatamoto-$e-$p"); done; done
  have=$(cd "$src" && find . -mindepth 1 -printf '%P\n' | LC_ALL=C sort)
  exp=$(printf '%s\n' "${want[@]}" SHA256SUMS | LC_ALL=C sort)
  [[ $have == "$exp" ]] || die "edition build files differ from the expected set (missing or extra files)"
  for f in "${want[@]}" SHA256SUMS; do
    [[ -f $src/$f && ! -L $src/$f && -s $src/$f ]] || die "$f is not a regular non-empty file"
  done
  n=$(wc -l < "$src/SHA256SUMS")
  [[ $n -eq ${#want[@]} ]] || die "SHA256SUMS must list exactly ${#want[@]} files"
  (cd "$src" && sha256sum -c --strict --quiet SHA256SUMS >/dev/null 2>&1) || die "sha256 mismatch in the edition build"
  for f in "${want[@]}"; do
    grep -q "^[0-9a-f]\{64\}  $f\$" "$src/SHA256SUMS" || die "SHA256SUMS does not list $f"
    [[ ! -e $out/$f ]] || die "$out/$f already exists"
  done
  for f in "${want[@]}"; do cp "$src/$f" "$out/$f"; done
  (cd "$out" && sha256sum -c --strict --quiet "$src/SHA256SUMS" >/dev/null 2>&1) \
    || die "sha256 mismatch after copying into $out"
  msg="edition builds verified and copied into $out: ${#want[@]} files (editions: $eds)"
  echo "$msg"
  if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then echo "$msg" >> "$GITHUB_STEP_SUMMARY"; fi
}

case $cmd in
  local)
    need_tag; [[ -n $from && -n $dist ]] || usage
    verify_copy "$from" "$dist"
    ;;
  fetch)
    need_tag; [[ -n $sha && -n $dist ]] || usage
    [[ -n ${GH_TOKEN:-} ]] || die "RELEASE_EDITIONS is set but no token (EDITION_DISPATCH_TOKEN) was given"
    [[ ${EDITION_REPO:-} =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || die "EDITION_REPO must be owner/name"
    [[ $sha =~ ^[0-9a-f]{7,64}$ ]] || die "bad --sha"
    WAIT=${EDITION_WAIT_SECONDS:-2700}; POLL=${EDITION_POLL_SECONDS:-30}; EVENT=${EDITION_EVENT:-edition-build}
    start=$(date -u +%Y-%m-%dT%H:%M:%SZ); name="edition-builds-$tag"
    gh api -X POST "repos/$EDITION_REPO/dispatches" -f event_type="$EVENT" \
      -f "client_payload[tag]=$tag" -f "client_payload[sha]=$sha" >/dev/null || die "dispatch failed"
    deadline=$((SECONDS + WAIT))
    while :; do
      id=$(gh api "repos/$EDITION_REPO/actions/artifacts?name=$name&per_page=20" 2>/dev/null \
        | jq -r --arg s "$start" '[.artifacts[] | select((.expired | not) and .created_at >= $s)]
                                   | sort_by(.created_at) | last | .workflow_run.id // empty' 2>/dev/null) || id=
      [[ -n $id ]] && break
      (( SECONDS >= deadline )) && die "timed out after ${WAIT}s waiting for $name; no draft is made"
      sleep "$POLL"
    done
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    gh run download "$id" -R "$EDITION_REPO" -n "$name" -D "$tmp/src" >/dev/null || die "download failed"
    verify_copy "$tmp/src" "$dist"
    ;;
  require)
    [[ -n $rec && -f $rec ]] || usage
    for e in $eds; do
      jq -e --arg e "$e" '(.editions[$e] // {}) | keys == ["darwin-amd64","darwin-arm64","linux-amd64","linux-arm64"]' "$rec" >/dev/null \
        || die "$rec lacks a complete $e build"
      echo "$e: all four platforms are in the record"
    done
    ;;
  *) usage ;;
esac
