#!/usr/bin/env bash
# Build the approved community kits listed in a hatamoto-plugins checkout.
#
# Usage: build-community-kits.sh <plugins-repo-dir> <hatamoto-binary> <key-file> <packages-dir> <meta-file> <archive-dir>
#
# For every <plugins-repo-dir>/manifests/kits/<name>.json (an absent directory is
# not an error): validate the manifest, shallow-fetch the pinned commit, have
# `hatamoto kit pack` check the kit (kit.json rules, limits) and sign it into
# <packages-dir>/<name>.kit.hmp, verify the result with `hatamoto kit check --package`,
# and add an entry {title, about, features (when given), author, source, commit,
# tier: "community"} to <meta-file> (created when absent; other keys are kept).
# Everything is built in a temp dir first; the results are copied into place only
# after every manifest succeeded, so a failed run publishes nothing (the final copy
# itself is not atomic). A kit holds data only: nothing is compiled and no author
# code runs. There is no source mirror and there are no screenshots.
#
# The manifest (docs/KITS.md, "In the registry"): name, title, about, features[],
# repo, commit, subdir, author, license, archive_grant, homepage. name, repo, commit,
# author, license and archive_grant are required; archive_grant must be true and the
# license an allowlisted SPDX id.
#
# A kit file is <name>.kit.hmp, so a kit may share its name with a plugin or a theme
# (<name>.hmp). Only a kit-vs-kit collision is refused: an existing
# <packages-dir>/<name>.kit.hmp. The meta file is keyed by bare name for plugins and
# themes, so a kit whose name one of them already holds there (a file <name>.hmp in
# <packages-dir>, or a bare key from another source in <meta-file>) gets its entry under
# "kit:<name>" instead; `registry index` applies that key to the kit only.
#
# <archive-dir> is an append-only archive of signed builds, same layout as the plugin
# and theme ones, with the kind in the name:
#   <dir>/<name>/<name>-<version>-<id12>-<keyid>.kit.hmp plus a .kit.json sidecar
#   {name, kind: "kit", version, commit, id12, key_id, sha256}
# A manifest whose full commit matches a sidecar written for the current signing key
# reuses that file byte for byte (no build): its sha256 must match the sidecar, and
# `kit check --package` must find the sidecar's name, else the run dies. The version
# is also checked against the pinned source (kit.json name+version at subdir after a
# shallow fetch). Source unreachable: refuse, unless HATAMOTO_ARCHIVE_OFFLINE=1.
# New builds are appended only after every manifest succeeded; an existing file with
# different content is refused.
#
# Optional environment:
#   HATAMOTO_ARCHIVE_OFFLINE=1  reuse an archived build even when its pinned source
#     cannot be reached to check it (logged loudly; the reused build is unchecked).
#   HATAMOTO_KITS_SKIP="a b"  kit names to leave out of this build (logged loudly);
#     each must match an existing manifests/kits/<name>.json.
# Environment (tests only; the Makefile unsets it so a stray shell export cannot
# reach a production build):
#   HATAMOTO_ALLOW_FILE_REPOS=1     accept file:/// git repos
set -euo pipefail
export LC_ALL=C.UTF-8

[ $# -eq 6 ] || { echo "usage: $0 <plugins-repo-dir> <hatamoto-binary> <key-file> <packages-dir> <meta-file> <archive-dir>" >&2; exit 2; }
repo_dir=$1 bin=$2 key=$3 packages=$4 meta=$5 archive_dir=$6

die() { echo "build-community-kits: $*" >&2; exit 1; }

command -v jq >/dev/null || die "jq not found"
jq -n '"a" | test("\\p{L}")' >/dev/null 2>&1 || die "jq lacks \\p{} regex support"
jq -n '"\u3164" | test("\\p{Default_Ignorable_Code_Point}")' 2>/dev/null | grep -qx true \
  || die "jq lacks \\p{Default_Ignorable_Code_Point} support (needs jq 1.7+)"
command -v git >/dev/null || die "git not found"
[ -d "$repo_dir" ] || die "$repo_dir not found"
# No kits: nothing to do (a plugins repo from before kits).
[ -d "$repo_dir/manifests/kits" ] || { echo "build-community-kits: no manifests/kits, nothing to build"; exit 0; }
[ -x "$bin" ] || die "hatamoto binary $bin is not executable"
[ -f "$key" ] || die "key file $key not found"
# The signer's key id (as `keys generate` writes it into the key file) is part of the archive identity.
key_id=$(jq -r '.key_id // empty' "$key" 2>/dev/null) || key_id=
[[ $key_id =~ ^[0-9a-f]{8,64}$ ]] || die "key file $key has no valid key_id"

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/packages" "$stage/work" "$stage/p"
if [ -f "$meta" ]; then cp "$meta" "$stage/meta.json"; else printf '{}' >"$stage/meta.json"; fi

# Git: ignore the maintainer's config, hooks, submodules and LFS.
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_LFS_SKIP_SMUDGE=1
git_opts=(-c core.hooksPath=/dev/null -c submodule.recurse=false -c protocol.allow=never -c protocol.https.allow=always)
[ "${HATAMOTO_ALLOW_FILE_REPOS:-}" = 1 ] && git_opts+=(-c protocol.file.allow=always)

# Listing policy: the reserved names are read from <plugins-repo-dir>/policy/reserved-names.txt
# (data only). Fail closed: a missing, unreadable, symlinked or empty list stops the build.
pol="$repo_dir/policy"
[ -d "$pol" ] && [ ! -L "$pol" ] || die "missing policy: $pol"
{ [ -f "$pol/reserved-names.txt" ] && [ -r "$pol/reserved-names.txt" ] && [ ! -L "$pol/reserved-names.txt" ]; } || die "missing policy: reserved-names.txt"
# plist <list>: entries, trimmed and lowercased, without blank lines and "# " comments.
# keep in sync with hatamoto-plugins scripts/validate-theme-manifest.sh and registry/build-community.sh
plist() {
  jq -Rr 'ascii_downcase | gsub("\r"; "") | gsub("^\\s+|\\s+$"; "") | select(. != "" and . != "#" and (startswith("# ") | not))' "$pol/$1.txt"
}
reserved_names=$(plist reserved-names) || die "unreadable policy: reserved-names.txt"
[ -n "$reserved_names" ] || die "empty policy: reserved-names.txt"
# inlist <newline separated list> <word>: exact whole-line membership.
# keep in sync with hatamoto-plugins scripts/validate-theme-manifest.sh and registry/build-community.sh
inlist() { [[ $'\n'$1$'\n' == *$'\n'"$2"$'\n'* ]]; }

reserved_names=$(plist reserved-names) || die "unreadable policy: reserved-names.txt"
[ -n "$reserved_names" ] || die "empty policy: reserved-names.txt"

# Allowed SPDX licenses; keep in sync with scripts/validate-theme-manifest.sh in the hatamoto-plugins repo.
licenses=(MIT Apache-2.0 BSD-2-Clause BSD-3-Clause ISC MPL-2.0)
for l in GPL-2.0 GPL-3.0 LGPL-2.1 LGPL-3.0 AGPL-3.0; do
  licenses+=("$l" "$l-only" "$l-or-later")
done

# host_ok <url>: public DNS name only. No IP literal, localhost, single label,
# internal suffix, port or credentials. Keep in sync with
# scripts/validate-theme-manifest.sh in the hatamoto-plugins repo.
host_ok() {
  local h=${1#https://}
  h=${h%%/*}
  h=$(printf '%s' "$h" | tr '[:upper:]' '[:lower:]')
  case $h in
    ''|*@*|*:*|*\[*|*\]*) return 1 ;;
    localhost|*.localhost|*.local|*.internal|*.lan|*.home|*.corp|*.test|*.invalid) return 1 ;;
  esac
  [[ $h =~ ^[0-9.]+$ ]] && return 1
  [[ $h == *.* ]] || return 1
  return 0
}
url_re='^https://([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}/[^[:space:]@]+$'
repo_ok() { { [[ $1 =~ $url_re ]] && host_ok "$1"; } || { [ "${HATAMOTO_ALLOW_FILE_REPOS:-}" = 1 ] && [[ $1 =~ ^file:///[A-Za-z0-9._/-]+$ ]]; }; }

# field <file> <key>: the string value, or empty when absent or not a string.
field() { jq -r --arg k "$2" 'if (.[$k] | type) == "string" then .[$k] else "" end' "$1"; }
# Emoji, symbols and invisible characters (copy of build-community.sh and the validator's unsafe_chars_filter).
unsafe_chars_filter='$t | test("\\p{Cf}|\\p{Co}|\\p{Cs}|\\p{So}|\\p{Sk}|\\p{Extended_Pictographic}|\\p{Emoji_Presentation}|\\p{Default_Ignorable_Code_Point}|(?![\u0640\u3005\u3031-\u3035\u303B\u309D\u309E\u30FC-\u30FE])\\p{Lm}")'

# check_about_features <name> <manifest>: the rules for the optional manifest keys "about"
# (plain text, paragraphs separated by one newline) and "features" (a list of short lines).
# Caps match Meta.validate in backend/internal/packages/indexbuild.go; the brand, banned-word
# and script rules are for title and description only. The value only enters jq as input.
# keep in sync with hatamoto-plugins scripts/validate-theme-manifest.sh
check_about_features() {
  local name=$1 f=$2 r
  r=$(jq -r '
    def unsafe: '"${unsafe_chars_filter#\$t | }"';
    def line_problem:
      if test("[<>]") then "must not contain < or >"
      elif (ascii_downcase | contains("://") or contains("www.")) then "must not contain a URL"
      elif . != gsub("^\\s+|\\s+$"; "") then "must not start or end with whitespace"
      elif unsafe then "must not contain emoji, symbols or invisible characters"
      else empty end;
    def has_control($keep): explode | any(. != $keep and (. < 32 or (. >= 127 and . <= 159)));
    (if has("about") then
      .about as $a
      | if ($a | type) != "string" then "about must be a string"
        elif ($a | length) < 1 or ($a | length) > 1500 then "about must be 1 to 1500 characters"
        elif ($a | has_control(10)) then "about must not contain control characters other than a newline between paragraphs"
        elif ($a | split("\n") | length) > 10 then "about must have at most 10 paragraphs"
        elif ($a | split("\n") | any(. == "")) then "about must not have an empty paragraph"
        else ($a | split("\n") | map(line_problem) | first) as $p | if $p then "about \($p)" else empty end end
    else empty end),
    (if has("features") then
      .features as $l
      | if ($l | type) != "array" or ($l | any(type != "string")) then "features must be a list of strings"
        elif ($l | length) < 1 or ($l | length) > 8 then "features must list 1 to 8 items"
        elif ($l | any(length < 1 or length > 120)) then "each feature must be 1 to 120 characters"
        elif ($l | any(has_control(-1))) then "a feature must not contain control characters"
        else ($l | map(line_problem) | first) as $p
          | if $p then "a feature \($p)"
            elif ($l | map(ascii_downcase) | unique | length) != ($l | length) then "features must not repeat an item"
            else empty end end
    else empty end)' "$f" | head -n 1) || die "$name: about and features could not be checked"
  [ -z "$r" ] || die "$name: $r"
}

has() { jq -e --arg k "$2" 'has($k)' "$1" >/dev/null; }


# Pass 1: validate every manifest before touching the network.
shopt -s nullglob
manifests=("$repo_dir"/manifests/kits/*.json)
if [ -n "${HATAMOTO_KITS_SKIP:-}" ]; then
  read -ra skips <<<"$HATAMOTO_KITS_SKIP"
  for s in "${skips[@]}"; do
    [[ $s =~ ^[a-z][a-z0-9-]{1,39}$ ]] && [ -f "$repo_dir/manifests/kits/$s.json" ] || die "HATAMOTO_KITS_SKIP: no kit manifest named \"$s\""
  done
  kept=()
  for f in ${manifests[@]+"${manifests[@]}"}; do
    skip=
    for s in "${skips[@]}"; do [ "$s" = "$(basename "$f" .json)" ] && skip=1; done
    if [ -n "$skip" ]; then echo "build-community-kits: WARNING: SKIPPING $(basename "$f" .json) (HATAMOTO_KITS_SKIP): it will NOT be in the published index" >&2
    else kept+=("$f"); fi
  done
  manifests=(${kept[@]+"${kept[@]}"})
fi
seen=
for f in ${manifests[@]+"${manifests[@]}"}; do
  base=$(basename "$f" .json)
  jq -e 'type == "object"' "$f" >/dev/null 2>&1 || die "$base: manifest is not a JSON object"
  extra=$(jq -r '[keys[] | select(IN("name","title","about","features","repo","commit","subdir","author","license","archive_grant","homepage") | not)] | join(", ")' "$f")
  [ -z "$extra" ] || die "$base: unknown field(s): $extra (a kit manifest has no tier)"
  for k in name title author license homepage subdir repo commit about; do
    ! has "$f" "$k" || jq -e --arg k "$k" '.[$k] | type == "string"' "$f" >/dev/null || die "$base: $k must be a string"
  done
  name=$(field "$f" name); author=$(field "$f" author); license=$(field "$f" license)
  title=$(field "$f" title); homepage=$(field "$f" homepage); subdir=$(field "$f" subdir)
  repo=$(field "$f" repo); commit=$(field "$f" commit)
  [[ $name =~ ^[a-z][a-z0-9-]{1,39}$ ]] || die "$base: bad name"
  [ "$name" = "$base" ] || die "$base: name \"$name\" must equal the file name"
  inlist "$seen" "$name" && die "$name: duplicate kit manifest"
  seen+="$name"$'\n'
  ! inlist "$reserved_names" "$name" || die "$name: name is reserved"
  # Kit-vs-kit only: a plugin or theme may share the name (its file is <name>.hmp).
  [ ! -e "$packages/$name.kit.hmp" ] || die "$name: already in $packages (kit name collision)"
  [[ $author =~ ^[A-Za-z0-9][A-Za-z0-9\ ._@-]{0,60}$ ]] || die "$name: bad author"
  jq -e '.archive_grant == true' "$f" >/dev/null || die "$name: archive_grant must be true"
  allowed=
  for l in "${licenses[@]}"; do [ "$license" = "$l" ] && allowed=1; done
  [ -n "$allowed" ] || die "$name: license not allowed: \"$license\""
  if has "$f" title; then
    if [ -z "$title" ] || [ "${#title}" -gt 60 ] || [[ $title =~ [[:cntrl:]] ]] || [[ $title == " "* ]] || [[ $title == *" " ]]; then
      die "$name: bad title (1 to 60 characters, no control characters, no leading or trailing space)"
    fi
  fi
  check_about_features "$name" "$f"
  if has "$f" homepage; then [[ $homepage =~ ^https:// ]] || die "$name: homepage must start with https://"; fi
  if has "$f" subdir; then
    if ! [[ $subdir =~ ^[A-Za-z0-9._/-]+$ ]] || [[ $subdir == /* ]] || [[ $subdir =~ (^|/)\.\.(/|$) ]]; then
      die "$name: bad subdir"
    fi
  fi
  has "$f" repo && has "$f" commit || die "$name: repo and commit are required"
  repo_ok "$repo" || die "$name: repo must be an https:// URL on a public host name (no IP, localhost, port or credentials)"
  [[ $commit =~ ^[0-9a-f]{40}$ ]] || die "$name: commit must be 40 lowercase hex characters"
done

# fetch_git: put the pinned commit of $repo into $stage/work/$name and set $src.
fetch_git() {
  local clone="$stage/work/$name" want
  mkdir -p "$clone"
  git "${git_opts[@]}" -C "$clone" init -q
  git "${git_opts[@]}" -C "$clone" fetch -q --depth 1 --no-recurse-submodules "$repo" "$commit" || die "$name: cannot fetch $commit from $repo"
  git "${git_opts[@]}" -C "$clone" checkout -q --no-recurse-submodules FETCH_HEAD || die "$name: checkout failed"
  rm -rf "$clone/.git"
  src=$clone
  [ -z "$subdir" ] || src="$clone/${subdir%/}"
  [ -d "$src" ] || die "$name: subdir not found in the pinned source"
  # Refuse a symlink anywhere on the subdir path, even one that stays inside the clone.
  want="$(realpath "$clone")${subdir:+/${subdir%/}}"
  src=$(realpath "$src")
  [ "$src" = "$want" ] || die "$name: subdir is a symlink or resolves outside the clone"
  [ -z "$(find "$src" -type l -print -quit)" ] || die "$name: the kit source contains a symlink"
}

# authenticate_archive <sidecar-version>: check the archived version against the pinned
# source (see the header). Dies on a mismatch; an unreachable source dies unless
# HATAMOTO_ARCHIVE_OFFLINE=1.
authenticate_archive() {
  local got= pinned= c="$stage/work/$name-auth"
  rm -rf "$c"; mkdir -p "$c"
  git "${git_opts[@]}" -C "$c" init -q
  if git "${git_opts[@]}" -C "$c" fetch -q --depth 1 --no-recurse-submodules "$repo" "$commit" 2>/dev/null; then
    got=1
    pinned=$(git -C "$c" show "$commit:${subdir:+${subdir%/}/}kit.json" 2>/dev/null | jq -r '[.name, .version] | join(" ")' 2>/dev/null) || pinned=
  fi
  if [ -z "$got" ]; then
    [ "${HATAMOTO_ARCHIVE_OFFLINE:-}" = 1 ] || die "$name: cannot reach $repo to check the archived version against the pin (HATAMOTO_ARCHIVE_OFFLINE=1 reuses it unchecked)"
    echo "build-community-kits: WARNING: $name: archived version $1 NOT checked against the pinned source (HATAMOTO_ARCHIVE_OFFLINE=1)" >&2
    return 0
  fi
  [ "$pinned" = "$name $1" ] || die "$name: archived version \"$name $1\" does not match kit.json \"$pinned\" at the pinned source; refusing the archive (rolled back?)"
}

# reuse_archive: copy the archived .kit.hmp whose sidecar pin equals this manifest's
# into the stage; sets built_from=archive and version, else leaves them empty.
reuse_archive() {
  local side hmp sum sv out
  [ -d "$archive_dir/$name" ] || return 0
  for side in "$archive_dir/$name"/"$name"-*.kit.json; do
    [ -f "$side" ] || continue
    jq -e --arg n "$name" --arg p "$commit" --arg k "$key_id" \
      '.name == $n and .kind == "kit" and .commit == $p and .key_id == $k' "$side" >/dev/null 2>&1 || continue
    hmp=${side%.kit.json}.kit.hmp
    [ -f "$hmp" ] || die "$name: archive sidecar $side has no .kit.hmp"
    sum=$(sha256sum "$hmp" | cut -d' ' -f1)
    [ "$sum" = "$(jq -r '.sha256 // empty' "$side")" ] || die "$name: archived $hmp does not match its sidecar sha256"
    # The sidecar is unauthenticated: the package's own kit.json and the file name must agree with it.
    sv=$(jq -r '.version // empty' "$side")
    out=$("$bin" kit check --package "$hmp" --expect-name "$name" 2>&1) || die "$name: archived $hmp is refused: $out"
    [ "$(printf '%s\n' "$out" | sed -n 's/^kit \(.*\): ok$/\1/p' | head -n1)" = "$name $sv" ] \
      || die "$name: archived $hmp holds a different name or version than its sidecar (\"$sv\")"
    [ "$(basename "$hmp")" = "$name-$sv-${commit:0:12}-$key_id.kit.hmp" ] || die "$name: archived file name $(basename "$hmp") does not match its sidecar"
    authenticate_archive "$sv"
    version=$sv
    cp "$hmp" "$stage/packages/$name.kit.hmp"
    echo "build-community-kits: $name: reusing archived $(basename "$hmp")"
    built_from=archive
    return 0
  done
}

# build_kit: fresh build from the pinned source; sets version.
build_kit() {
  local out dest="$stage/p/$name.kit.hmp"
  fetch_git
  out=$("$bin" kit pack "$src" --expect-name "$name" --key "$key" --out "$dest" 2>&1) || die "$name: pack failed: $out"
  version=$(printf '%s\n' "$out" | sed -n "s/^  kit $name v\\(.*\\), [0-9]* bytes\$/\\1/p" | head -n1)
  [[ $version =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]] || die "$name: kit.json version is missing or unsafe for the archive"
  [ -s "$dest" ] || die "$name: pack wrote no package"
  out=$("$bin" kit check --package "$dest" --expect-name "$name" 2>&1) || die "$name: the packed kit is refused: $out"
  mv "$dest" "$stage/packages/$name.kit.hmp"
  built_from=built
}

for f in ${manifests[@]+"${manifests[@]}"}; do
  name=$(field "$f" name); author=$(field "$f" author); title=$(field "$f" title)
  subdir=$(field "$f" subdir); repo=$(field "$f" repo); commit=$(field "$f" commit)
  id12=${commit:0:12}
  echo "build-community-kits: $name @ $commit"
  built_from= version=
  reuse_archive
  [ -n "$built_from" ] || build_kit
  if [ "$built_from" = built ]; then
    af="$name-$version-$id12-$key_id"
    mkdir -p "$stage/archive/$name"
    cp "$stage/packages/$name.kit.hmp" "$stage/archive/$name/$af.kit.hmp"
    jq -n --arg n "$name" --arg v "$version" --arg c "$commit" --arg i "$id12" --arg k "$key_id" \
      --arg h "$(sha256sum "$stage/packages/$name.kit.hmp" | cut -d' ' -f1)" \
      '{name:$n, kind:"kit", version:$v, commit:$c, id12:$i, key_id:$k, sha256:$h}' \
      >"$stage/archive/$name/$af.kit.json"
  fi
  # The meta file is keyed by bare name for plugins and themes. A kit whose name one of
  # them holds (a file <name>.hmp here, or a bare entry from another source) is keyed
  # "kit:<name>", so neither entry overwrites the other.
  key=$name
  if [ -e "$packages/$name.hmp" ] || ! jq -e --arg n "$name" --arg s "$repo" '(.[$n].source // $s) == $s' "$stage/meta.json" >/dev/null; then
    key="kit:$name"
  fi
  jq --arg n "$key" --arg a "$author" --arg t "$title" --arg s "$repo" --arg c "$commit" \
    --arg about "$(field "$f" about)" --argjson feat "$(jq -c '.features // null' "$f")" \
    '.[$n] = ({author: $a, source: $s, commit: $c, tier: "community"} + (if $t != "" then {title: $t} else {} end) + (if $about != "" then {about: $about} else {} end) + (if $feat != null then {features: $feat} else {} end))' \
    "$stage/meta.json" >"$stage/meta.next" || die "$name: meta merge failed"
  mv "$stage/meta.next" "$stage/meta.json"
done

# Everything succeeded: publish. The archive is append-only: check every staged
# file first so a refusal leaves everything untouched.
if [ -d "$stage/archive" ]; then
  for a in "$stage"/archive/*/*; do
    dest="$archive_dir/${a#"$stage"/archive/}"
    [ ! -e "$dest" ] || [ "$(sha256sum "$a" | cut -d' ' -f1)" = "$(sha256sum "$dest" | cut -d' ' -f1)" ] \
      || die "archive is append-only: $dest exists with different content, nothing was published"
  done
  for a in "$stage"/archive/*/*; do
    dest="$archive_dir/${a#"$stage"/archive/}"
    # Temp name in the same dir, then rename. Files sort .kit.hmp before .kit.json, so a
    # sidecar only ever follows a complete package.
    [ -e "$dest" ] || { mkdir -p "$(dirname "$dest")" && cp "$a" "$dest.tmp.$$" && mv "$dest.tmp.$$" "$dest"; }
  done
fi
if [ ${#manifests[@]} -gt 0 ]; then
  mkdir -p "$packages"
  cp "$stage"/packages/*.kit.hmp "$packages/"
  cp "$stage/meta.json" "$meta"
fi
echo "build-community-kits: built ${#manifests[@]} kit(s)"
