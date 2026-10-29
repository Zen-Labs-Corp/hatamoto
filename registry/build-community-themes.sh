#!/usr/bin/env bash
# Build the approved community themes listed in a hatamoto-plugins checkout.
#
# Usage: build-community-themes.sh <plugins-repo-dir> <hatamoto-binary> <key-file> <packages-dir> <meta-file> <screenshots-dir> <archive-dir>
#
# For every <plugins-repo-dir>/manifests/themes/<name>.json (an absent directory is
# not an error): validate the manifest, get the theme source (a git commit or a
# pinned zip archive), have `hatamoto theme pack ... --registry` check it (theme.json
# rules, screenshot, no wasm, Limits) and sign it into <packages-dir>/<name>.hmp,
# put its screenshot into <screenshots-dir>/<name>.<ext> and add an entry
# {title, about, features (when given), author, source, commit (git only), tier: "community",
# screenshot: "screenshots/<name>.<ext>"} to <meta-file> (created when absent;
# other keys are kept). Everything is built in a temp dir first; the results are
# copied into place only after every manifest succeeded, so a failed run publishes
# nothing (the final copy itself is not atomic). No Go toolchain is needed: a theme
# is data, nothing is compiled and no author code runs. There is no source mirror.
#
# A name that already exists in <packages-dir>, in <meta-file> (a plugin built just
# before, or a first-party package) or as manifests/<name>.json (a plugin manifest)
# is refused, never replaced.
#
# Every manifest needs "archive_grant": true and an allowlisted SPDX license; this
# backstop holds even when PR CI was bypassed. The manifest rules mirror
# schemas/theme-manifest.schema.json and scripts/validate-theme-manifest.sh in the
# hatamoto-plugins repo; keep them in sync (licenses, host_ok and the URL pattern
# are copies; build-community.sh has the plugin license list).
#
# <archive-dir> is an append-only archive of signed builds, same layout as the plugin one:
#   <dir>/<name>/<name>-<version>-<id12>-<keyid>.hmp plus a .json sidecar
#   {name, kind: "theme", version, id12, key_id, sha256, and commit (git source) or
#   archive_sha256 (archive source)}
# where id12 is the first 12 characters of the commit (git) or of the manifest sha256
# (archive). A manifest whose full commit / sha256 matches a sidecar written for the
# current signing key reuses that .hmp byte for byte (no fetch, no build): its sha256
# must match the sidecar, and `theme check --package` must find the same name and
# version as the sidecar and the file name, else the run dies. The archive files sit on
# a host that can be compromised, so the version is also checked against the pinned
# source: a git pin is shallow-fetched and theme.json name+version at subdir compared;
# an archive pin is re-downloaded, its sha256 verified and its theme.json name+version
# compared. A rolled-back (older, genuinely signed) build renamed under a new pin is
# refused. Source unreachable: refuse, unless HATAMOTO_ARCHIVE_OFFLINE=1 (loud warning).
# Residual risk: the check compares name+version, not bytes; a host-planted build with
# the pinned version but other content passes. New builds are appended only
# after every manifest succeeded; an existing file with different content is refused.
#
# Optional environment:
#   HATAMOTO_ARCHIVE_OFFLINE=1  reuse an archived build even when its pinned source
#     cannot be reached to check it (logged loudly; the reused build is unchecked).
#   HATAMOTO_THEMES_SKIP="a b"  theme names to leave out of this build (logged loudly);
#     each must match an existing manifests/themes/<name>.json.
# Environment (tests only; the Makefile unsets them so a stray shell export cannot
# reach a production build):
#   HATAMOTO_ALLOW_FILE_REPOS=1     accept file:/// git repos
#   HATAMOTO_ALLOW_FILE_ARCHIVES=1  accept file:/// zip archives
set -euo pipefail
# Count characters, not bytes, in the title checks.
export LC_ALL=C.UTF-8

[ $# -eq 7 ] || { echo "usage: $0 <plugins-repo-dir> <hatamoto-binary> <key-file> <packages-dir> <meta-file> <screenshots-dir> <archive-dir>" >&2; exit 2; }
repo_dir=$1 bin=$2 key=$3 packages=$4 meta=$5 shots_dir=$6 archive_dir=$7

die() { echo "build-community-themes: $*" >&2; exit 1; }

command -v jq >/dev/null || die "jq not found"
command -v git >/dev/null || die "git not found"
command -v curl >/dev/null || die "curl not found"
[ -d "$repo_dir" ] || die "$repo_dir not found"
# No themes: nothing to do (a plugins repo from before themes).
[ -d "$repo_dir/manifests/themes" ] || { echo "build-community-themes: no manifests/themes, nothing to build"; exit 0; }
[ -x "$bin" ] || die "hatamoto binary $bin is not executable"
[ -f "$key" ] || die "key file $key not found"
# The signer's key id (as `keys generate` writes it into the key file) is part of the archive identity.
key_id=$(jq -r '.key_id // empty' "$key" 2>/dev/null) || key_id=
[[ $key_id =~ ^[0-9a-f]{8,64}$ ]] || die "key file $key has no valid key_id"

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/packages" "$stage/shots" "$stage/work" "$stage/p"
if [ -f "$meta" ]; then cp "$meta" "$stage/meta.json"; else printf '{}' >"$stage/meta.json"; fi

# Git: ignore the maintainer's config, hooks, submodules and LFS.
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_LFS_SKIP_SMUDGE=1
git_opts=(-c core.hooksPath=/dev/null -c submodule.recurse=false -c protocol.allow=never -c protocol.https.allow=always)
[ "${HATAMOTO_ALLOW_FILE_REPOS:-}" = 1 ] && git_opts+=(-c protocol.file.allow=always)

# Listing policy: the reserved names are read from <plugins-repo-dir>/policy/reserved-names.txt
# (data only; nothing in that checkout is executed). Fail closed: a missing, unreadable,
# symlinked or empty list stops the build before pass 1.
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
archive_ok() { { [[ $1 =~ $url_re ]] && host_ok "$1"; } || { [ "${HATAMOTO_ALLOW_FILE_ARCHIVES:-}" = 1 ] && [[ $1 =~ ^file:///[A-Za-z0-9._/-]+\.zip$ ]]; }; }

# field <file> <key>: the string value, or empty when absent or not a string.
field() { jq -r --arg k "$2" 'if (.[$k] | type) == "string" then .[$k] else "" end' "$1"; }
# Emoji, symbols and invisible characters (copy of build-community.sh).
unsafe_chars_filter='$t | test("\\p{Cf}|\\p{Co}|\\p{Cs}|\\p{So}|\\p{Sk}|\\p{Extended_Pictographic}|\\p{Emoji_Presentation}")'

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
manifests=("$repo_dir"/manifests/themes/*.json)
if [ -n "${HATAMOTO_THEMES_SKIP:-}" ]; then
  read -ra skips <<<"$HATAMOTO_THEMES_SKIP"
  for s in "${skips[@]}"; do
    [[ $s =~ ^[a-z][a-z0-9-]{1,39}$ ]] && [ -f "$repo_dir/manifests/themes/$s.json" ] || die "HATAMOTO_THEMES_SKIP: no theme manifest named \"$s\""
  done
  kept=()
  for f in ${manifests[@]+"${manifests[@]}"}; do
    skip=
    for s in "${skips[@]}"; do [ "$s" = "$(basename "$f" .json)" ] && skip=1; done
    if [ -n "$skip" ]; then echo "build-community-themes: WARNING: SKIPPING $(basename "$f" .json) (HATAMOTO_THEMES_SKIP): it will NOT be in the published index" >&2
    else kept+=("$f"); fi
  done
  manifests=(${kept[@]+"${kept[@]}"})
fi
for f in ${manifests[@]+"${manifests[@]}"}; do
  base=$(basename "$f" .json)
  jq -e 'type == "object"' "$f" >/dev/null 2>&1 || die "$base: manifest is not a JSON object"
  extra=$(jq -r '[keys[] | select(IN("name","author","license","homepage","subdir","repo","commit","archive","sha256","title","archive_grant","about","features") | not)] | join(", ")' "$f")
  [ -z "$extra" ] || die "$base: unknown field(s): $extra (a theme manifest has no tier)"
  for k in name author license title homepage subdir repo commit archive sha256 about; do
    ! has "$f" "$k" || jq -e --arg k "$k" '.[$k] | type == "string"' "$f" >/dev/null || die "$base: $k must be a string"
  done
  name=$(field "$f" name); author=$(field "$f" author); license=$(field "$f" license)
  title=$(field "$f" title); homepage=$(field "$f" homepage); subdir=$(field "$f" subdir)
  repo=$(field "$f" repo); commit=$(field "$f" commit); archive=$(field "$f" archive); sha=$(field "$f" sha256)
  [[ $name =~ ^[a-z][a-z0-9-]{1,39}$ ]] || die "$base: bad name"
  [ "$name" = "$base" ] || die "$base: name \"$name\" must equal the file name"
  case $name in default|webhooks|hello|gtm|searchconsole) die "$name: name is reserved" ;; esac
  ! inlist "$reserved_names" "$name" || die "$name: name is reserved"
  # Cross-kind uniqueness: plugins (manifest, built package, meta) and first-party packages.
  [ ! -e "$repo_dir/manifests/$name.json" ] || die "$name: already used by a plugin (manifests/$name.json)"
  [ ! -e "$packages/$name.hmp" ] || die "$name: already in $packages (name collision)"
  jq -e --arg n "$name" 'has($n) | not' "$stage/meta.json" >/dev/null || die "$name: already in the meta file (name collision)"
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
  git_src=; arc_src=
  { has "$f" repo || has "$f" commit; } && git_src=1
  { has "$f" archive || has "$f" sha256; } && arc_src=1
  if [ -n "$git_src" ] && [ -n "$arc_src" ]; then die "$name: use either repo+commit or archive+sha256, not both"
  elif [ -z "$git_src" ] && [ -z "$arc_src" ]; then die "$name: missing source: give repo+commit or archive+sha256"
  elif [ -n "$git_src" ]; then
    has "$f" repo && has "$f" commit || die "$name: repo and commit go together"
    repo_ok "$repo" || die "$name: repo must be an https:// URL on a public host name (no IP, localhost, port or credentials)"
    [[ $commit =~ ^[0-9a-f]{40}$ ]] || die "$name: commit must be 40 lowercase hex characters"
  else
    has "$f" archive && has "$f" sha256 || die "$name: archive and sha256 go together"
    ! has "$f" subdir || die "$name: subdir is not allowed with an archive source"
    archive_ok "$archive" || die "$name: archive must be an https:// URL on a public host name (no IP, localhost, port or credentials)"
    [[ $sha =~ ^[0-9a-f]{64}$ ]] || die "$name: sha256 must be 64 lowercase hex characters"
  fi
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
  [ -z "$(find "$src" -type l -print -quit)" ] || die "$name: the theme source contains a symlink"
}

# download <dest>: fetch $archive (hardened curl); non-zero when it cannot be fetched.
download() {
  local protos='=https'
  case $archive in file://*) protos='=file' ;; esac
  curl --proto "$protos" --proto-redir '=https' --max-redirs 5 --max-filesize 67108864 --max-time 120 -fsSL -o "$1" "$archive"
}

# authenticate_archive <sidecar-version>: check the archived version against the pinned
# source (see the header). Dies on a mismatch; an unreachable source dies unless
# HATAMOTO_ARCHIVE_OFFLINE=1.
authenticate_archive() {
  local got= pinned= a="$stage/work/$name-auth.zip" c="$stage/work/$name-auth" out
  if [ -n "$commit" ]; then
    rm -rf "$c"; mkdir -p "$c"
    git "${git_opts[@]}" -C "$c" init -q
    if git "${git_opts[@]}" -C "$c" fetch -q --depth 1 --no-recurse-submodules "$repo" "$commit" 2>/dev/null; then
      got=1
      pinned=$(git -C "$c" show "$commit:${subdir:+${subdir%/}/}theme.json" 2>/dev/null | jq -r '[.name, .version] | join(" ")' 2>/dev/null) || pinned=
    fi
  elif download "$a" 2>/dev/null; then
    got=1
    [ "$(sha256sum "$a" | cut -d' ' -f1)" = "$sha" ] || die "$name: $archive no longer matches the pinned sha256; refusing the archived build"
    out=$("$bin" theme check --archive "$a" --sha256 "$sha" --expect-name "$name" 2>&1) || die "$name: pinned archive is refused: $out"
    pinned=$(printf '%s\n' "$out" | sed -n 's/^ok: theme \(.*\) v\(.*\)$/\1 \2/p' | head -n1)
  fi
  if [ -z "$got" ]; then
    [ "${HATAMOTO_ARCHIVE_OFFLINE:-}" = 1 ] || die "$name: cannot reach ${repo:-$archive} to check the archived version against the pin (HATAMOTO_ARCHIVE_OFFLINE=1 reuses it unchecked)"
    echo "build-community-themes: WARNING: $name: archived version $1 NOT checked against the pinned source (HATAMOTO_ARCHIVE_OFFLINE=1)" >&2
    return 0
  fi
  [ "$pinned" = "$name $1" ] || die "$name: archived version \"$name $1\" does not match theme.json \"$pinned\" at the pinned source; refusing the archive (rolled back?)"
}

# packed_version <pack-output>: the version `theme pack` printed ("  theme <name> v<version>, N bytes").
# shot_file <output>: the last line "screenshot: <name>.<ext>", checked.
shot_file() {
  local s; s=$(printf '%s\n' "$1" | sed -n 's/^screenshot: //p' | tail -n1)
  [[ $s =~ ^$name\.(png|webp|jpg)$ ]] || die "$name: the CLI reported no valid screenshot (\"$s\")"
  [ -s "$stage/shots/$s" ] || die "$name: screenshot $s was not written"
  printf '%s' "$s"
}

# reuse_archive: copy the archived .hmp whose sidecar pin equals this manifest's into
# the stage; sets built_from=archive, else leaves it empty. Sets version and shot.
reuse_archive() {
  local side hmp sum sv out pin_key pin_val
  [ -d "$archive_dir/$name" ] || return 0
  if [ -n "$commit" ]; then pin_key=commit pin_val=$commit; else pin_key=archive_sha256 pin_val=$sha; fi
  for side in "$archive_dir/$name"/"$name"-*.json; do
    [ -f "$side" ] || continue
    jq -e --arg n "$name" --arg p "$pin_val" --arg k "$key_id" --arg pk "$pin_key" \
      '.name == $n and .kind == "theme" and .[$pk] == $p and .key_id == $k' "$side" >/dev/null 2>&1 || continue
    hmp=${side%.json}.hmp
    [ -f "$hmp" ] || die "$name: archive sidecar $side has no .hmp"
    sum=$(sha256sum "$hmp" | cut -d' ' -f1)
    [ "$sum" = "$(jq -r '.sha256 // empty' "$side")" ] || die "$name: archived $hmp does not match its sidecar sha256"
    # The sidecar is unauthenticated: the package's own theme.json and the file name must agree with it.
    sv=$(jq -r '.version // empty' "$side")
    out=$("$bin" theme check --package "$hmp" --registry --expect-name "$name" --screenshot-out "$stage/shots" 2>&1) \
      || die "$name: archived $hmp is refused: $out"
    [ "$(printf '%s\n' "$out" | sed -n 's/^ok: theme //p' | head -n1)" = "$name v$sv" ] \
      || die "$name: archived $hmp holds a different name or version than its sidecar (\"$sv\")"
    [ "$(basename "$hmp")" = "$name-$sv-${pin_val:0:12}-$key_id.hmp" ] || die "$name: archived file name $(basename "$hmp") does not match its sidecar"
    authenticate_archive "$sv"
    version=$sv
    shot=$(shot_file "$out")
    cp "$hmp" "$stage/packages/$name.hmp"
    echo "build-community-themes: $name: reusing archived $(basename "$hmp")"
    built_from=archive
    return 0
  done
}

# build_theme: fresh build from the pinned source; sets version and shot.
build_theme() {
  local out dest="$stage/p/$name.hmp" a
  if [ -n "$commit" ]; then
    fetch_git
    out=$("$bin" theme pack "$src" --registry --expect-name "$name" --key "$key" --out "$dest" --screenshot-out "$stage/shots" 2>&1) \
      || die "$name: pack failed: $out"
  else
    a="$stage/work/$name.zip"
    download "$a" || die "$name: cannot download $archive"
    out=$("$bin" theme pack --archive "$a" --sha256 "$sha" --registry --expect-name "$name" --key "$key" --out "$dest" --screenshot-out "$stage/shots" 2>&1) \
      || die "$name: pack failed: $out"
  fi
  version=$(printf '%s\n' "$out" | sed -n "s/^  theme $name v\\(.*\\), [0-9]* bytes\$/\\1/p" | head -n1)
  [[ $version =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]] || die "$name: theme.json version is missing or unsafe for the archive"
  shot=$(shot_file "$out")
  [ -s "$dest" ] || die "$name: pack wrote no package"
  mv "$dest" "$stage/packages/$name.hmp"
  built_from=built
}

for f in ${manifests[@]+"${manifests[@]}"}; do
  name=$(field "$f" name); author=$(field "$f" author); title=$(field "$f" title)
  subdir=$(field "$f" subdir); repo=$(field "$f" repo); commit=$(field "$f" commit)
  archive=$(field "$f" archive); sha=$(field "$f" sha256)
  if [ -n "$commit" ]; then pin=$commit source_url=$repo; else pin=$sha source_url=$archive; fi
  id12=${pin:0:12}
  echo "build-community-themes: $name @ $pin"
  built_from= version= shot=
  reuse_archive
  [ -n "$built_from" ] || build_theme
  if [ "$built_from" = built ]; then
    af="$name-$version-$id12-$key_id"
    mkdir -p "$stage/archive/$name"
    cp "$stage/packages/$name.hmp" "$stage/archive/$name/$af.hmp"
    # publish-registry.sh needs no sidecar field; commit is git-only, archive_sha256 archive-only.
    jq -n --arg n "$name" --arg v "$version" --arg c "$commit" --arg a "$sha" --arg i "$id12" --arg k "$key_id" \
      --arg h "$(sha256sum "$stage/packages/$name.hmp" | cut -d' ' -f1)" \
      '{name:$n, kind:"theme", version:$v} + (if $c != "" then {commit:$c} else {archive_sha256:$a} end) + {id12:$i, key_id:$k, sha256:$h}' \
      >"$stage/archive/$name/$af.json"
  fi
  jq --arg n "$name" --arg a "$author" --arg t "$title" --arg s "$source_url" --arg c "$commit" --arg p "screenshots/$shot" \
    --arg about "$(field "$f" about)" --argjson feat "$(jq -c '.features // null' "$f")" \
    '.[$n] = ({author: $a, source: $s} + (if $c != "" then {commit: $c} else {} end) + {tier: "community", screenshot: $p} + (if $t != "" then {title: $t} else {} end) + (if $about != "" then {about: $about} else {} end) + (if $feat != null then {features: $feat} else {} end))' \
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
    # Temp name in the same dir, then rename. Files sort .hmp before .json, so a
    # sidecar only ever follows a complete .hmp.
    [ -e "$dest" ] || { mkdir -p "$(dirname "$dest")" && cp "$a" "$dest.tmp.$$" && mv "$dest.tmp.$$" "$dest"; }
  done
fi
if [ ${#manifests[@]} -gt 0 ]; then
  mkdir -p "$packages" "$shots_dir"
  cp "$stage"/shots/* "$shots_dir/"
  cp "$stage"/packages/*.hmp "$packages/"
  cp "$stage/meta.json" "$meta"
fi
echo "build-community-themes: built ${#manifests[@]} theme(s)"
