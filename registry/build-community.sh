#!/usr/bin/env bash
# Build the approved community plugins listed in a hatamoto-plugins checkout.
#
# Usage: build-community.sh <plugins-repo-dir> <hatamoto-binary> <key-file> <packages-dir> <meta-file>
#
# For every <plugins-repo-dir>/manifests/<name>.json (schema.json is skipped,
# any other file name must be a valid plugin name): validate the manifest, fetch
# the pinned commit, build it like `make plugin-<name>` into a fresh directory
# in the stage, pack and sign it into <packages-dir>/<name>.hmp, and add an entry
# {title (when given), author, source, commit, tier: "community"} to <meta-file> (created when
# absent; other keys are kept). A manifest with "tier": "first-party" gets
# {title (when given), source, commit, tier: "first-party"} instead (no author, and a
# manifest "author" is refused); it is accepted only when the repo owner is listed in
# FIRST_PARTY_OWNERS, never silently downgraded. The tier is a label: trust is the signature. A name that already exists in <packages-dir> or
# <meta-file> (a first-party plugin) is refused, never replaced. Everything is
# built and signed in a temp dir first; the results are copied into place only
# after every manifest succeeded, so a failed run publishes nothing (the final
# copy itself is not atomic).
#
# A plugin go.mod may not replace hatamoto.dev/sdk or use a local-path replace, and
# a go.work or go.work.sum at the build source or clone root is refused.
#
# Every manifest needs "archive_grant": true and an allowlisted SPDX license (the
# PR template's two boxes); this backstop holds even when PR CI was bypassed.
#
# The manifest rules mirror manifests/schema.json and scripts/validate-manifest.sh
# in the hatamoto-plugins repo; keep them in sync. Only Go plugins are built.
#
# Required environment:
#   HATAMOTO_BUILD_GOTOOLCHAIN=goX.Y[.Z]  the toolchain every plugin is built with
#     (the Makefile passes the toolchain line of backend/go.mod). It is used as
#     GOTOOLCHAIN exactly, never "+auto": a plugin's go.mod cannot select a
#     newer toolchain, so a plugin that needs a newer Go fails loudly.
# Optional environment:
#   HATAMOTO_MIRROR_BASE=https://github.com/<org>  source mirror: each plugin is fetched
#     first from <base>/<name> at refs/tags/src/<commit> (annotated tag on one commit
#     that sits directly on the pinned commit and touches only vendor/ and
#     MIRROR.json; a tag that exists but fails this check stops the run, a missing
#     tag or repo falls back) and only then from the manifest repo.
#     A mirror is built exactly like upstream (-mod=readonly, go.sum and sumdb verified);
#     its vendor/ is ignored, because Go never checks vendor/ against go.sum.
#   HATAMOTO_TRUST_MIRROR_VENDOR=1  last resort when the author's repo AND the module
#     proxy are gone: build a mirror that has vendor/modules.txt with -mod=vendor and
#     GOPROXY=off, trusting the mirror's vendor/ (a loud warning is printed). Refused
#     when the pinned commit itself already contains vendor/.
#     The meta "source" is the manifest repo while it answers, else the mirror when the
#     mirror has the commit.
#   ARCHIVE_DIR=<dir>             append-only archive of signed builds:
#     <dir>/<name>/<name>-<version>-<commit12>-<keyid>.hmp plus a .json sidecar
#     {name, version, commit, key_id, sha256}. A manifest whose full commit matches a
#     sidecar written for the current signing key (a rotated key rebuilds into a new file)
#     reuses that .hmp byte for byte (no fetch, no build); its sha256 must still match
#     the sidecar, else the run dies. New builds are written here only after every
#     manifest succeeded, and an existing file with a different sha256 is refused.
#     Reuse also requires the .hmp's own plugin.json name and version to match the
#     sidecar and the file name, and that version to equal plugin.json at the pinned
#     commit (fetched from the mirror tag, else upstream; a rolled-back old .hmp renamed
#     to a new commit is refused). When neither source is reachable the reuse is refused
#     unless HATAMOTO_ARCHIVE_OFFLINE=1, which reuses it unchecked with a warning.
#     New files are written under a temp name and renamed, the sidecar after the .hmp.
#     There is no hatamoto verify command: trust rests on the sidecar sha256, the
#     later `registry index` parse and the signature check at install time.
#   COMMUNITY_SKIP="a b"          manifest names to leave out of this build (logged
#     loudly). Escape hatch for a pinned commit that no longer exists upstream.
#   FIRST_PARTY_OWNERS="a b"      GitHub owners whose manifests may carry "tier": "first-party"
#     (space separated, compared case-insensitively; default empty = nobody). The names gtm
#     and searchconsole are reserved for first-party manifests from these owners; webhooks
#     and hello are reserved for everyone (core ships webhooks, hello is the example).
# Environment (tests only; honoured only when this script is run directly by the tests,
# the Makefile unsets them so a stray shell export cannot reach a production build):
#   HATAMOTO_ALLOW_FILE_REPOS=1   accept file:// repos (and a file:/// HATAMOTO_MIRROR_BASE); the
#     owner of a file:// repo is the name of its parent directory (so tests can be first-party)
#   HATAMOTO_SDK_REPLACE=<dir>    run `go mod edit -replace hatamoto.dev/sdk=<dir>` in the clone
# (`make registry` runs this script with env -u for both of these.)
set -euo pipefail
# Count characters, not bytes, in the title checks.
export LC_ALL=C.UTF-8

[ $# -eq 5 ] || { echo "usage: $0 <plugins-repo-dir> <hatamoto-binary> <key-file> <packages-dir> <meta-file>" >&2; exit 2; }
repo_dir=$1 bin=$2 key=$3 packages=$4 meta=$5

die() { echo "build-community: $*" >&2; exit 1; }

[[ ${HATAMOTO_BUILD_GOTOOLCHAIN:-} =~ ^go[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || die "HATAMOTO_BUILD_GOTOOLCHAIN must be set to a Go toolchain such as go1.27.2"
command -v jq >/dev/null || die "jq not found"
command -v git >/dev/null || die "git not found"
[ -d "$repo_dir/manifests" ] || die "$repo_dir/manifests not found"
[ -x "$bin" ] || die "hatamoto binary $bin is not executable"
[ -f "$key" ] || die "key file $key not found"
# The signer's key id (as `keys generate` writes it into the key file) is part of the archive identity.
key_id=$(jq -r '.key_id // empty' "$key" 2>/dev/null) || key_id=
[[ $key_id =~ ^[0-9a-f]{8,64}$ ]] || die "key file $key has no valid key_id"
mkdir -p "$packages"

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/packages" "$stage/work"
if [ -f "$meta" ]; then cp "$meta" "$stage/meta.json"; else printf '{}' >"$stage/meta.json"; fi

# Git: ignore the maintainer's config, hooks, submodules and LFS.
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_LFS_SKIP_SMUDGE=1
git_opts=(-c core.hooksPath=/dev/null -c submodule.recurse=false -c protocol.allow=never -c protocol.https.allow=always)
[ "${HATAMOTO_ALLOW_FILE_REPOS:-}" = 1 ] && git_opts+=(-c protocol.file.allow=always)

# Allowed SPDX licenses; keep in sync with the hatamoto-plugins repo.
license_re='^(MIT|Apache-2\.0|BSD-2-Clause|BSD-3-Clause|ISC|MPL-2\.0|((A?GPL-3\.0|GPL-2\.0|LGPL-2\.1|LGPL-3\.0)(-only|-or-later)?))$'

trust_vendor=
if [ "${HATAMOTO_TRUST_MIRROR_VENDOR:-}" = 1 ]; then
  trust_vendor=1
  echo "build-community: WARNING: HATAMOTO_TRUST_MIRROR_VENDOR=1: a mirror's vendor/ is built WITHOUT go.sum or sumdb verification. Use only when the upstream repos and the module proxy are gone." >&2
fi

mirror_base=${HATAMOTO_MIRROR_BASE:-}
if [ -n "$mirror_base" ]; then
  if [[ $mirror_base =~ ^https://github\.com/[A-Za-z0-9._-]+$ ]]; then :
  elif [ "${HATAMOTO_ALLOW_FILE_REPOS:-}" = 1 ] && [[ $mirror_base == file:///* ]]; then :
  else die "HATAMOTO_MIRROR_BASE must be https://github.com/<org>"; fi
fi

# Owners allowed to use "tier": "first-party" (lowercase); empty means nobody.
read -ra fp_owners <<<"$(printf '%s' "${FIRST_PARTY_OWNERS:-}" | tr '[:upper:]' '[:lower:]')"
# repo_owner <repo-url>: the lowercase owner (https://github.com/<owner>/..., or the parent
# directory name of a file:// repo, which only passes the repo check under the test flag).
repo_owner() {
  local o
  if [[ $1 == file:///* ]]; then o=$(basename "$(dirname "${1#file://}")"); else o=${1#https://github.com/}; o=${o%%/*}; fi
  printf '%s' "$o" | tr '[:upper:]' '[:lower:]'
}
# owner_allowed <lowercase-owner>
owner_allowed() { local w; for w in ${fp_owners[@]+"${fp_owners[@]}"}; do [ "$w" = "$1" ] && return 0; done; return 1; }

# field <file> <key>: the string value, or empty when absent or not a string.
field() { jq -r --arg k "$2" 'if (.[$k] | type) == "string" then .[$k] else "" end' "$1"; }

# Pass 1: validate every manifest before touching the network or running a build.
shopt -s nullglob
manifests=()
for f in "$repo_dir"/manifests/*.json; do
  [ "$(basename "$f" .json)" = schema ] || manifests+=("$f")
done
# Skipped names (COMMUNITY_SKIP) are dropped before validation; each must match a
# manifest. The names are split on whitespace only, never glob-expanded.
if [ -n "${COMMUNITY_SKIP:-}" ]; then
  read -ra skips <<<"$COMMUNITY_SKIP"
  for s in "${skips[@]}"; do
    [ -f "$repo_dir/manifests/$s.json" ] && [ "$s" != schema ] || die "COMMUNITY_SKIP: no manifest named \"$s\""
  done
  kept=()
  for f in ${manifests[@]+"${manifests[@]}"}; do
    skip=
    for s in "${skips[@]}"; do [ "$s" = "$(basename "$f" .json)" ] && skip=1; done
    if [ -n "$skip" ]; then echo "build-community: WARNING: SKIPPING $(basename "$f" .json) (COMMUNITY_SKIP): it will NOT be in the published index" >&2
    else kept+=("$f"); fi
  done
  manifests=(${kept[@]+"${kept[@]}"})
fi
for f in ${manifests[@]+"${manifests[@]}"}; do
  base=$(basename "$f" .json)
  jq -e 'type == "object"' "$f" >/dev/null 2>&1 || die "$base: manifest is not a JSON object"
  name=$(field "$f" name); repo=$(field "$f" repo); commit=$(field "$f" commit)
  subdir=$(field "$f" subdir); author=$(field "$f" author); license=$(field "$f" license)
  title=$(field "$f" title)
  [[ $name =~ ^[a-z][a-z0-9-]{1,39}$ ]] || die "$base: bad name"
  [ "$name" = "$base" ] || die "$base: name \"$name\" must equal the file name"
  [ ! -e "$packages/$name.hmp" ] || die "$name: already in $packages (first-party name collision)"
  jq -e --arg n "$name" 'has($n) | not' "$stage/meta.json" >/dev/null || die "$name: already in the meta file (first-party name collision)"
  if [[ $repo =~ ^https://github\.com/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$ ]]; then :
  elif [ "${HATAMOTO_ALLOW_FILE_REPOS:-}" = 1 ] && [[ $repo == file:///* ]]; then :
  else die "$name: repo must be https://github.com/<owner>/<repo>"; fi
  [[ $commit =~ ^[0-9a-f]{40}$ ]] || die "$name: commit must be 40 lowercase hex characters"
  if [ -n "$subdir" ]; then
    if ! [[ $subdir =~ ^[A-Za-z0-9._/-]+$ ]] || [[ $subdir == /* ]] || [[ $subdir =~ (^|/)\.\.(/|$) ]]; then
      die "$name: bad subdir"
    fi
  fi
  if jq -e 'has("title")' "$f" >/dev/null; then
    if [ -z "$title" ] || [ "${#title}" -gt 60 ] || [[ $title =~ [[:cntrl:]] ]] || [[ $title == " "* ]] || [[ $title == *" " ]]; then
      die "$name: bad title (1 to 60 characters, no control characters, no leading or trailing space)"
    fi
  fi
  # Tier, owner and reserved names (keep in sync with the hatamoto-plugins validator and schema).
  tier=
  if jq -e 'has("tier")' "$f" >/dev/null; then
    [ "$(field "$f" tier)" = first-party ] || die "$name: bad tier (only \"first-party\" is allowed; omit it for a community plugin)"
    tier=first-party
  fi
  owner_ok=
  owner_allowed "$(repo_owner "$repo")" && owner_ok=1
  case $name in
    webhooks|hello) die "$name: name is reserved" ;;
    gtm|searchconsole) { [ -n "$tier" ] && [ -n "$owner_ok" ]; } || die "$name: name is reserved for a first-party plugin from an allowed owner" ;;
  esac
  if [ -n "$tier" ]; then
    [ -n "$owner_ok" ] || die "$name: tier first-party is not allowed for the owner of $repo"
    ! jq -e 'has("author")' "$f" >/dev/null || die "$name: a first-party manifest must not have an author"
  else
    [[ $author =~ ^[A-Za-z0-9][A-Za-z0-9-]{0,38}$ ]] || die "$name: bad author"
  fi
  jq -e '.archive_grant == true' "$f" >/dev/null || die "$name: archive_grant must be true"
  [[ $license =~ $license_re ]] || die "$name: license not allowed: \"$license\""
done

# Pass 2: build. Every go call below uses the pinned toolchain, no workspace and
# read-only modules, so a plugin go.mod can neither select nor download another Go.
export GOTOOLCHAIN="$HATAMOTO_BUILD_GOTOOLCHAIN" GOWORK=off GOFLAGS=-mod=readonly CGO_ENABLED=0
# mirror_touches_only_vendor: the top commit in $clone changes nothing but
# MIRROR.json and files under <subdir>/vendor/ (fixed-string prefix compare).
mirror_touches_only_vendor() {
  local p vp="${subdir:+${subdir%/}/}vendor/"
  while IFS= read -r -d '' p; do
    [[ $p == MIRROR.json || $p == "$vp"* ]] || return 1
  done < <(git -C "$clone" diff -z --no-renames --name-only "FETCH_HEAD^{commit}^" "FETCH_HEAD^{commit}")
}
# build_plugin: fetch (mirror first), check and build the plugin in $name/$repo/$commit/$subdir
# into $out, then pack it to $stage/packages/$name.hmp. Sets built_from.
build_plugin() {
  clone="$stage/work/$name"
  mkdir -p "$clone"
  git "${git_opts[@]}" -C "$clone" init -q
  built_from=upstream
  if [ -n "$mirror_base" ]; then
    m="$mirror_base/$name"
    # A missing tag or repo is a miss (fall back to upstream). A tag that exists but is
    # not on the pinned commit or touches more than vendor/ and MIRROR.json is
    # tampering: stop, never fall back.
    if git "${git_opts[@]}" -C "$clone" fetch -q --depth 2 --no-recurse-submodules "$m" "refs/tags/src/$commit" 2>/dev/null; then
      [ "$(git -C "$clone" rev-parse "FETCH_HEAD^{commit}^" 2>/dev/null)" = "$commit" ] && mirror_touches_only_vendor \
        || die "$name: mirror $m tag src/$commit is not the pinned commit plus vendor/ and MIRROR.json only; refusing (possible tampering)"
      built_from=mirror
    else
      echo "build-community: $name: no mirror tag at $m, using $repo" >&2
      rm -rf "$clone"; mkdir -p "$clone"
      git "${git_opts[@]}" -C "$clone" init -q
    fi
  fi
  if [ "$built_from" = upstream ]; then
    git "${git_opts[@]}" -C "$clone" fetch -q --depth 1 --no-recurse-submodules "$repo" "$commit" || die "$name: cannot fetch $commit from the mirror or $repo"
  fi
  git "${git_opts[@]}" -C "$clone" checkout -q --no-recurse-submodules FETCH_HEAD || die "$name: checkout failed"
  src=$clone
  [ -z "$subdir" ] || src="$clone/${subdir%/}"
  [ -d "$src" ] || die "$name: subdir not found in the pinned source"
  # Refuse a symlink anywhere on the subdir path, even one that stays inside the clone.
  want="$(realpath "$clone")${subdir:+/${subdir%/}}"
  src=$(realpath "$src")
  [ "$src" = "$want" ] || die "$name: subdir is a symlink or resolves outside the clone"
  for e in plugin.json migrations ui assets; do
    [ ! -L "$src/$e" ] || die "$name: $e is a symlink"
  done
  [ -f "$src/plugin.json" ] || die "$name: no plugin.json in the pinned source"
  [ "$(jq -r '.name // empty' "$src/plugin.json")" = "$name" ] || die "$name: plugin.json name does not match the manifest"
  # The package must be built against the real SDK from a plain module: no
  # go.work, no replace of hatamoto.dev/sdk (to any target), no local-path replace.
  # This runs before the HATAMOTO_SDK_REPLACE test hook is applied.
  for d in "$src" "$clone"; do
    for w in go.work go.work.sum; do
      [ ! -e "$d/$w" ] && [ ! -L "$d/$w" ] || die "$name: $w is not allowed in the source or the repository root"
    done
  done
  gomod=$(cd "$src" && [ -f go.mod ] && go mod edit -json) || die "$name: no readable go.mod in the pinned source"
  jq -e '[.Replace[]? | select(.Old.Path == "hatamoto.dev/sdk")] | length == 0' <<<"$gomod" >/dev/null \
    || die "$name: go.mod replaces hatamoto.dev/sdk"
  jq -e '[.Replace[]? | select(.New.Path | startswith(".") or startswith("/"))] | length == 0' <<<"$gomod" >/dev/null \
    || die "$name: go.mod has a local-path replace directive"
  if [ -n "${HATAMOTO_SDK_REPLACE:-}" ]; then
    (cd "$src" && go mod edit -replace "hatamoto.dev/sdk=$HATAMOTO_SDK_REPLACE") || die "$name: go mod edit failed"
  fi
  # Modules are go.sum-verified (vendor/ is ignored) unless the operator trusts the
  # mirror's vendor/ explicitly; a vendor/ in the author's own commit is never used.
  build_flags=-mod=readonly build_proxy=${GOPROXY:-}
  if [ -n "$trust_vendor" ] && [ "$built_from" = mirror ] && [ -f "$src/vendor/modules.txt" ]; then
    ! git -C "$clone" cat-file -e "$commit:${subdir:+${subdir%/}/}vendor" 2>/dev/null \
      || die "$name: the pinned commit itself contains vendor/; refusing HATAMOTO_TRUST_MIRROR_VENDOR"
    build_flags=-mod=vendor build_proxy=off
  fi
  out="$stage/out/$name"
  mkdir -p "$out"
  # && chain: set -e is ignored inside a subshell that is tested with ||.
  (
    cd "$src" &&
    env GOOS=wasip1 GOARCH=wasm GOFLAGS="$build_flags" ${build_proxy:+GOPROXY="$build_proxy"} \
      go build -trimpath -buildmode=c-shared -o "$out/plugin.wasm" . &&
    cp plugin.json "$out/" &&
    for d in migrations ui assets; do
      if [ -d "$d" ]; then cp -rP "$d" "$out/$d" || exit 1; fi
    done
  ) || die "$name: build failed"
  [ -s "$out/plugin.wasm" ] || die "$name: build produced no plugin.wasm"
  [ "$(jq -r '.name // empty' "$out/plugin.json")" = "$name" ] || die "$name: built plugin.json name does not match"
  "$bin" plugin pack "$out" --key "$key" --out "$stage/packages/$name.hmp" >/dev/null || die "$name: pack failed"
}

# authenticate_archive <sidecar-version>: the archive files sit on a host that can be
# compromised, so the version they claim is checked against the pinned commit: fetch it
# (mirror tag src/<commit> first, whose parent must be the pinned commit, else upstream;
# --depth) and compare plugin.json name and version at that commit. Both sources
# unreachable: refuse, unless HATAMOTO_ARCHIVE_OFFLINE=1 (warning).
authenticate_archive() {
  local c="$stage/work/$name-auth" got=
  rm -rf "$c"; mkdir -p "$c"
  git "${git_opts[@]}" -C "$c" init -q
  if [ -n "$mirror_base" ] && git "${git_opts[@]}" -C "$c" fetch -q --depth 2 --no-recurse-submodules "$mirror_base/$name" "refs/tags/src/$commit" 2>/dev/null; then
    [ "$(git -C "$c" rev-parse "FETCH_HEAD^{commit}^" 2>/dev/null)" = "$commit" ] \
      || die "$name: mirror tag src/$commit is not on the pinned commit; refusing (possible tampering)"
    got=1
  elif git "${git_opts[@]}" -C "$c" fetch -q --depth 1 --no-recurse-submodules "$repo" "$commit" 2>/dev/null; then
    got=1
  fi
  if [ -z "$got" ]; then
    [ "${HATAMOTO_ARCHIVE_OFFLINE:-}" = 1 ] || die "$name: cannot reach the mirror or $repo to check the archived version against $commit (HATAMOTO_ARCHIVE_OFFLINE=1 reuses it unchecked)"
    echo "build-community: WARNING: $name: archived version $1 NOT checked against $commit (HATAMOTO_ARCHIVE_OFFLINE=1)" >&2
    return 0
  fi
  local pj; pj=$(git -C "$c" show "$commit:${subdir:+${subdir%/}/}plugin.json" 2>/dev/null | jq -r '[.name, .version] | join(" ")' 2>/dev/null) || pj=
  [ "$pj" = "$name $1" ] || die "$name: archived version \"$name $1\" does not match plugin.json \"$pj\" at pinned commit $commit; refusing the archive (rolled back?)"
}

# reuse_archive: copy the archived .hmp whose sidecar commit equals $commit into the
# stage; sets built_from=archive, else leaves it empty.
reuse_archive() {
  local side hmp sum
  [ -n "${ARCHIVE_DIR:-}" ] || return 0
  for side in "$ARCHIVE_DIR/$name"/"$name"-*.json; do
    [ -f "$side" ] || continue
    jq -e --arg n "$name" --arg c "$commit" --arg k "$key_id" '.name == $n and .commit == $c and .key_id == $k' "$side" >/dev/null 2>&1 || continue
    hmp=${side%.json}.hmp
    [ -f "$hmp" ] || die "$name: archive sidecar $side has no .hmp"
    sum=$(sha256sum "$hmp" | cut -d' ' -f1)
    [ "$sum" = "$(jq -r '.sha256 // empty' "$side")" ] || die "$name: archived $hmp does not match its sidecar sha256"
    # The sidecar is unauthenticated: the package's own plugin.json and the file name must agree with it.
    local inner sv
    sv=$(jq -r '.version // empty' "$side")
    inner=$(unzip -p "$hmp" plugin.json 2>/dev/null | jq -r '[.name, .version] | join(" ")' 2>/dev/null) || inner=
    [ "$inner" = "$name $sv" ] || die "$name: archived $hmp holds plugin.json \"$inner\" but its sidecar says \"$name $sv\""
    [ "$(basename "$hmp")" = "$name-$sv-${commit:0:12}-$key_id.hmp" ] || die "$name: archived file name $(basename "$hmp") does not match its sidecar"
    authenticate_archive "$sv"
    cp "$hmp" "$stage/packages/$name.hmp"
    echo "build-community: $name: reusing archived $(basename "$hmp")"
    built_from=archive
    return 0
  done
}

for f in ${manifests[@]+"${manifests[@]}"}; do
  name=$(field "$f" name); repo=$(field "$f" repo); commit=$(field "$f" commit)
  subdir=$(field "$f" subdir); author=$(field "$f" author); title=$(field "$f" title)
  tier=community; jq -e 'has("tier")' "$f" >/dev/null && tier=first-party
  echo "build-community: $name @ $commit"
  built_from=
  m=${mirror_base:+$mirror_base/$name}
  reuse_archive
  [ -n "$built_from" ] || build_plugin
  if [ -n "${ARCHIVE_DIR:-}" ] && [ "$built_from" != archive ]; then
    version=$(jq -r '.version // empty' "$stage/out/$name/plugin.json")
    [[ $version =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]] || die "$name: plugin.json version is missing or unsafe for the archive"
    af="$name-$version-${commit:0:12}-$key_id"
    mkdir -p "$stage/archive/$name"
    cp "$stage/packages/$name.hmp" "$stage/archive/$name/$af.hmp"
    jq -n --arg n "$name" --arg v "$version" --arg c "$commit" --arg k "$key_id" --arg h "$(sha256sum "$stage/packages/$name.hmp" | cut -d' ' -f1)" \
      '{name:$n, version:$v, commit:$c, key_id:$k, sha256:$h}' >"$stage/archive/$name/$af.json"
  fi
  # Point at the author's repo while it still answers, else at our mirror, but only
  # when the mirror really has the commit (it was just fetched, or its tag is there).
  source_url=$repo
  if [ -n "$m" ] && [ "$built_from" != upstream ] && ! git "${git_opts[@]}" ls-remote --exit-code "$repo" HEAD >/dev/null 2>&1; then
    if [ "$built_from" = mirror ] || git "${git_opts[@]}" ls-remote --exit-code "$m" "refs/tags/src/$commit" >/dev/null 2>&1; then source_url=$m; fi
  fi
  jq --arg n "$name" --arg a "$author" --arg t "$title" --arg s "$source_url" --arg c "$commit" --arg k "$tier" \
    '.[$n] = ((if $k == "first-party" then {source: $s, commit: $c, tier: $k} else {author: $a, source: $s, commit: $c, tier: $k} end) + (if $t != "" then {title: $t} else {} end))' "$stage/meta.json" >"$stage/meta.next" \
    || die "$name: meta merge failed"
  mv "$stage/meta.next" "$stage/meta.json"
done

# Everything succeeded: publish. The archive is append-only: check every staged
# file first so a refusal leaves everything untouched.
if [ -n "${ARCHIVE_DIR:-}" ] && [ -d "$stage/archive" ]; then
  for a in "$stage"/archive/*/*; do
    dest="$ARCHIVE_DIR/${a#"$stage"/archive/}"
    [ ! -e "$dest" ] || [ "$(sha256sum "$a" | cut -d' ' -f1)" = "$(sha256sum "$dest" | cut -d' ' -f1)" ] \
      || die "archive is append-only: $dest exists with different content, nothing was published"
  done
  for a in "$stage"/archive/*/*; do
    dest="$ARCHIVE_DIR/${a#"$stage"/archive/}"
    # Copy to a temp name in the same dir, then rename: no partial file under a final
    # name. Files sort .hmp before .json, so a sidecar only ever follows a complete .hmp.
    [ -e "$dest" ] || { mkdir -p "$(dirname "$dest")" && cp "$a" "$dest.tmp.$$" && mv "$dest.tmp.$$" "$dest"; }
  done
fi
for p in "$stage"/packages/*.hmp; do
  [ -e "$p" ] || continue
  cp "$p" "$packages/"
done
cp "$stage/meta.json" "$meta"
echo "build-community: built ${#manifests[@]} plugin(s)"
