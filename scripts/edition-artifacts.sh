#!/usr/bin/env bash
# edition-artifacts.sh - bring the builds of other editions into a release dist/ dir, verified.
#
# Usage:
#   scripts/edition-artifacts.sh fetch   --tag vX.Y.Z --sha SHA --dist DIR   dispatch the edition build, wait for its artifact, verify, copy
#   scripts/edition-artifacts.sh local   --tag vX.Y.Z [--sha SHA] --from SRC --dist DIR  verify a local edition build dir, copy
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
# Contract for the edition build: on the dispatch (client_payload.tag, .sha, .nonce) it runs as a
# repository_dispatch run that ends in success and uploads ONE Actions artifact named edition-builds-<tag>
# holding exactly: hatamoto-<edition>-<platform> for every opted-in edition and platform; SHA256SUMS
# listing exactly those binaries (not the manifest); and edition-build.json = {"tag","sha","nonce"}, the
# values it was dispatched with. fetch needs tag, sha and nonce to match exactly (the nonce is unique per
# dispatch), and the producing run to be completed, event repository_dispatch, conclusion success.
# local needs the manifest too: tag == --tag, sha == --sha when given; the nonce is not checked.
# The freshness filter allows 120s of clock skew; the nonce is what makes an older artifact harmless.
# dist/ is touched only after the whole source verified; an existing file (or symlink) is never overwritten.
# A non-empty RELEASE_EDITIONS that holds no edition name (only spaces or commas) is an error, not a skip.
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
if [[ -z ${RELEASE_EDITIONS:-} ]]; then echo "no edition opt-in (RELEASE_EDITIONS empty): skipped"; exit 0; fi
[[ -n $eds ]] || die "RELEASE_EDITIONS is set but holds no edition name"
for e in $eds; do [[ $e =~ ^[a-z][a-z0-9]*$ ]] || die "bad edition name: $e"; done

need_tag() { [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || die "bad or missing --tag"; }

RUN_URL=

# verify_copy <src> <dist> [nonce]: exact file set, regular non-empty files, manifest (tag, sha when known,
# nonce when given), sha256sum -c --strict, no existing target in dist, then copy and re-hash there.
verify_copy() {
  local src=$1 out=$2 nonce=${3:-} want=() e p f n have exp msg
  [[ -d $src ]] || die "no edition build dir: $src"
  [[ -d $out ]] || die "no dist dir: $out"
  src=$(cd "$src" && pwd); out=$(cd "$out" && pwd)
  for e in $eds; do for p in "${PLATFORMS[@]}"; do want+=("hatamoto-$e-$p"); done; done
  have=$(cd "$src" && find . -mindepth 1 -printf '%P\n' | LC_ALL=C sort)
  exp=$(printf '%s\n' "${want[@]}" SHA256SUMS edition-build.json | LC_ALL=C sort)
  [[ $have == "$exp" ]] || die "edition build files differ from the expected set (missing or extra files)"
  for f in "${want[@]}" SHA256SUMS edition-build.json; do
    [[ -f $src/$f && ! -L $src/$f && -s $src/$f ]] || die "$f is not a regular non-empty file"
  done
  jq -e --arg t "$tag" --arg s "$sha" --arg n "$nonce" '
      keys == ["nonce","sha","tag"] and all(.[]; type == "string")
      and .tag == $t and .nonce != "" and ($s == "" or .sha == $s) and ($n == "" or .nonce == $n)' \
    "$src/edition-build.json" >/dev/null 2>&1 || die "edition-build.json does not match this release (tag, sha or nonce)"
  n=$(wc -l < "$src/SHA256SUMS")
  [[ $n -eq ${#want[@]} ]] || die "SHA256SUMS must list exactly ${#want[@]} files"
  (cd "$src" && sha256sum -c --strict --quiet SHA256SUMS >/dev/null 2>&1) || die "sha256 mismatch in the edition build"
  for f in "${want[@]}"; do
    grep -q "^[0-9a-f]\{64\}  $f\$" "$src/SHA256SUMS" || die "SHA256SUMS does not list $f"
    [[ ! -e $out/$f && ! -L $out/$f ]] || die "$out/$f already exists"
  done
  for f in "${want[@]}"; do cp "$src/$f" "$out/$f"; done
  (cd "$out" && sha256sum -c --strict --quiet "$src/SHA256SUMS" >/dev/null 2>&1) \
    || die "sha256 mismatch after copying into $out"
  msg="edition builds verified and copied into $out: ${#want[@]} files (editions: $eds)"
  echo "$msg"
  if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then
    { echo "$msg"
      [[ -z $RUN_URL ]] || echo "Edition run: $RUN_URL"
      echo '```'; cat "$src/SHA256SUMS"; echo '```'; } >> "$GITHUB_STEP_SUMMARY"
  fi
}

case $cmd in
  local)
    need_tag; [[ -n $from && -n $dist ]] || usage
    [[ -z $sha || $sha =~ ^[0-9a-f]{7,64}$ ]] || die "bad --sha"
    verify_copy "$from" "$dist"
    ;;
  fetch)
    need_tag; [[ -n $sha && -n $dist ]] || usage
    [[ -n ${GH_TOKEN:-} ]] || die "RELEASE_EDITIONS is set but no token (EDITION_DISPATCH_TOKEN) was given"
    [[ ${EDITION_REPO:-} =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || die "EDITION_REPO must be owner/name"
    [[ $sha =~ ^[0-9a-f]{7,64}$ ]] || die "bad --sha"
    WAIT=${EDITION_WAIT_SECONDS:-2700}; POLL=${EDITION_POLL_SECONDS:-30}; EVENT=${EDITION_EVENT:-edition-build}
    start=$(date -u -d '120 seconds ago' +%Y-%m-%dT%H:%M:%SZ); name="edition-builds-$tag"
    nonce="${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    # api <path>: result in $out; 401/403/404 fail at once, anything else (5xx, network) is retried by the caller
    api() {
      if out=$(gh api "$1" 2>"$tmp/err"); then return 0; fi
      if grep -qE 'HTTP (401|403|404)' "$tmp/err"; then die "gh api $1 failed: $(head -c 300 "$tmp/err")"; fi
      return 1
    }
    gh api -X POST "repos/$EDITION_REPO/dispatches" -f event_type="$EVENT" \
      -f "client_payload[tag]=$tag" -f "client_payload[sha]=$sha" -f "client_payload[nonce]=$nonce" >/dev/null || die "dispatch failed"
    deadline=$((SECONDS + WAIT))
    while :; do
      id=
      if api "repos/$EDITION_REPO/actions/artifacts?name=$name&per_page=20"; then
        id=$(jq -r --arg s "$start" '[.artifacts[] | select((.expired | not) and .created_at >= $s)]
                                   | sort_by(.created_at) | last | .workflow_run.id // empty' <<<"$out" 2>/dev/null) || id=
      fi
      [[ -n $id ]] && break
      (( SECONDS >= deadline )) && die "timed out after ${WAIT}s waiting for $name; no draft is made"
      sleep "$POLL"
    done
    [[ $id =~ ^[0-9]+$ ]] || die "bad run id"
    while :; do # the producing run must be a successful repository_dispatch run, finished
      if api "repos/$EDITION_REPO/actions/runs/$id"; then
        info=$(jq -r '[.event, .status, (.conclusion // ""), .html_url] | @tsv' <<<"$out" 2>/dev/null) || info=
        IFS=$'\t' read -r ev st concl RUN_URL <<<"$info" || true
        [[ -z $ev || $ev == repository_dispatch ]] || die "edition run $id is a $ev run, not repository_dispatch"
        if [[ $st == completed ]]; then
          [[ $concl == success ]] || die "edition run $id ended with conclusion $concl"
          break
        fi
      fi
      (( SECONDS >= deadline )) && die "timed out after ${WAIT}s waiting for edition run $id to finish; no draft is made"
      sleep "$POLL"
    done
    gh run download "$id" -R "$EDITION_REPO" -n "$name" -D "$tmp/src" >/dev/null || die "download failed"
    verify_copy "$tmp/src" "$dist" "$nonce"
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
