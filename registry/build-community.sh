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
# in the hatamoto-plugins repo; keep them in sync. The listing rules read their word
# lists from <plugins-repo-dir>/policy/ (the same files the validator reads); a missing,
# unreadable, symlinked or empty list stops the build before anything is fetched.
# Only Go plugins are built.
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

# Listing policy: the word lists behind the listing rules are read from
# <plugins-repo-dir>/policy/ (data only; nothing in that checkout is executed).
# Fail closed: a missing, unreadable, symlinked or empty list stops the build before
# pass 1. Only placeholders.txt may be empty.
# The functions below are copied from hatamoto-plugins scripts/validate-manifest.sh.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
pol="$repo_dir/policy"
[ -d "$pol" ] && [ ! -L "$pol" ] || die "missing policy: $pol"
# Oniguruma classes (\p{L}, \p{Cf}, ...) drive the title and description rules.
jq -n '"a" | test("\\p{L}")' >/dev/null 2>&1 || die "jq lacks \\p{} regex support"
for p in reserved-names banned-tokens banned-words brands placeholders; do
  { [ -f "$pol/$p.txt" ] && [ -r "$pol/$p.txt" ] && [ ! -L "$pol/$p.txt" ]; } || die "missing policy: $p.txt"
done
# plist <list>: entries, trimmed and lowercased, without blank lines and "# " comments.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
plist() {
  jq -Rr 'ascii_downcase | gsub("\r"; "") | gsub("^\\s+|\\s+$"; "") | select(. != "" and . != "#" and (startswith("# ") | not))' "$pol/$1.txt"
}
reserved_names=$(plist reserved-names) || die "unreadable policy: reserved-names.txt"
banned_tokens=$(plist banned-tokens) || die "unreadable policy: banned-tokens.txt"
banned_words=$(plist banned-words) || die "unreadable policy: banned-words.txt"
brands=$(plist brands) || die "unreadable policy: brands.txt"
placeholders=$(plist placeholders) || die "unreadable policy: placeholders.txt"
# An empty list would switch its rule off; only the placeholders may be empty.
for p in reserved_names banned_tokens banned_words brands; do
  [ -n "${!p}" ] || die "empty policy: ${p//_/-}.txt"
done
# inlist <newline separated list> <word>: exact whole-line membership.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
inlist() { [[ $'\n'$1$'\n' == *$'\n'"$2"$'\n'* ]]; }
# brand_owned <brand> <owner>: the brand is listed with that owner.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
brand_owned() {
  local b o owners
  while read -r -a owners; do
    [ "${owners[0]:-}" = "$1" ] || continue
    for o in "${owners[@]:1}"; do [ "$o" = "$2" ] && return 0; done
  done <<<"$brands"
  return 1
}
# brand_match <word>: print the brand in brands.txt that the word starts with, if any.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
brand_match() {
  local b rest
  while read -r b rest; do
    if [ -n "$b" ] && [[ $1 == "$b"* ]]; then printf '%s' "$b"; return 0; fi
  done <<<"$brands"
  return 1
}
# brand_listed <word>: the word is exactly a brand in brands.txt.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
brand_listed() {
  local b rest
  while read -r b rest; do [ "$b" = "$1" ] && return 0; done <<<"$brands"
  return 1
}
# lev1 <a> <b>: edit distance of at most 1.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
lev1() {
  local a=$1 b=$2 i=0
  [ "$a" = "$b" ] && return 0
  while [ "$i" -lt "${#a}" ] && [ "$i" -lt "${#b}" ] && [ "${a:i:1}" = "${b:i:1}" ]; do i=$((i + 1)); done
  a=${a:i}; b=${b:i}
  while [ -n "$a" ] && [ -n "$b" ] && [ "${a: -1}" = "${b: -1}" ]; do a=${a%?}; b=${b%?}; done
  [ "${#a}" -le 1 ] && [ "${#b}" -le 1 ]
}
# lkey <name>: hyphens removed, 0 1 3 5 folded to o l e s.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
lkey() { printf '%s' "$1" | tr -d '-' | tr '0135' 'oles'; }
# jv <value> <jq filter> <label> [jq args...]: true when the filter holds. The
# filter is a literal; the value only enters as --arg t. A jq failure stops the build.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
jv() {
  local v=$1 f=$2 l=$3 r
  shift 3
  r=$(jq -n --arg t "$v" "$@" "$f") || die "$l could not be checked"
  [ "$r" = true ]
}
# fold: look-alike letters (Cyrillic, Greek, a few Latin) to ASCII, for the word and brand
# comparisons only. Pairs of code points: look-alike, ASCII letter.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
fold_def='def fold:
  ([[1072,97],[1074,98],[1077,101],[1109,115],[1110,105],[1112,106],[1082,107],[1084,109],[1085,104],[1086,111],[1088,112],[1089,99],[1090,116],[1091,121],[1093,120],[1231,108],
    [1040,97],[1042,98],[1045,101],[1029,115],[1030,105],[1032,106],[1050,107],[1052,109],[1053,104],[1054,111],[1056,112],[1057,99],[1058,116],[1059,121],[1061,120],[1216,108],
    [959,111],[945,97],[949,101],[953,105],[954,107],[957,118],[961,112],[964,116],[965,117],[967,120],
    [927,111],[913,97],[917,101],[921,105],[922,107],[925,110],[929,112],[932,116],[933,121],[935,120],[914,98],[918,122],[919,104],[924,109],
    [609,103],[305,105],[304,105],[593,97]]
   | map({(.[0] | tostring): .[1]}) | add) as $m
  | explode | map($m[tostring] // .) | implode | ascii_downcase;
'
# Emoji, symbols and invisible characters (not \p{Emoji}: that is true for digits).
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
unsafe_chars_filter='$t | test("\\p{Cf}|\\p{Co}|\\p{Cs}|\\p{So}|\\p{Sk}|\\p{Extended_Pictographic}|\\p{Emoji_Presentation}")'

# check_name <name> <owner> <fpx> <own-file-base>: the listing rules for a name (the name
# pattern is checked by the caller). fpx=1: first-party exempt from the brand and reserved rules.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
check_name() {
  local name=$1 owner=$2 fpx=$3 base=$4 ntoks t b sib sname r nkey
  IFS=- read -ra ntoks <<<"$name"
  for t in "${ntoks[@]}"; do
    if inlist "$banned_tokens" "$t"; then die "$name: name contains the banned word \"$t\""; fi
  done
  if [ "$fpx" -eq 0 ]; then
    if inlist "$reserved_names" "$name"; then die "$name: name is reserved"; fi
    if b=$(brand_match "${ntoks[0]}") && ! brand_owned "$b" "$owner"; then
      die "$name: name must not start with the brand \"$b\""
    fi
  fi
  # Look-alikes: every other manifest (plugins and themes, whether or not it is part of
  # this build) and, unless first-party exempt, the reserved names.
  nkey=$(lkey "$name")
  look() { # look <other name>
    [ "$1" != "$name" ] || return 0
    if [ "$(lkey "$1")" = "$nkey" ] || lev1 "$name" "$1"; then die "$name: name looks like \"$1\""; fi
  }
  for sib in "$repo_dir"/manifests/*.json "$repo_dir"/manifests/themes/*.json; do
    sname=$(basename "$sib" .json)
    [ "$sname" != "$base" ] && [ "$sname" != schema ] || continue
    [[ $sname =~ ^[a-z][a-z0-9-]{1,39}$ ]] || continue
    look "$sname"
  done
  if [ "$fpx" -eq 0 ]; then
    while read -r r; do
      if [[ $r =~ ^[a-z][a-z0-9-]{1,39}$ ]]; then look "$r"; fi
    done <<<"$reserved_names"
  fi
}

# check_title <name> <title> <owner> <fpx>: the listing rules for a non-empty title
# (length and control characters are checked by the caller).
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh
check_title() {
  local name=$1 title=$2 owner=$3 fpx=$4 jt scripts title_words first_word w b shared t ntoks
  jt() { jv "$title" "$1" "$name: title"; }
  if jt "$unsafe_chars_filter"; then
    die "$name: title must not contain emoji, symbols or invisible characters"
  fi
  if jt '[$t | scan("[:,&/|+]| - ")] | length > 1'; then
    die "$name: title \"$title\" has more than one separator (: , & / | + and a spaced hyphen)"
  fi
  if jt '$t | test("[\\[\\](){}<>]|\\p{Ps}|\\p{Pe}")'; then
    die "$name: title must not contain brackets"
  fi
  if jt '$t | test("[\uFF00-\uFFEF]")'; then
    die "$name: title must not contain fullwidth or halfwidth forms"
  fi
  if jt '$t | test("^[\\p{L}\\p{N} .,:&/|+\u0027!?-]*$") | not'; then
    die "$name: title may contain only letters, digits, spaces and . , : & / | + ' ! ? -"
  fi
  # One script: Han with Kana or Hangul counts as one group, Kana with Hangul does not.
  scripts=$(jq -nr --arg t "$title" '
    def has($re): $t | test($re);
    [ (if has("\\p{Latin}") then "Latin" else empty end),
      (if has("[\\p{Han}\\p{Hiragana}\\p{Katakana}\\p{Hangul}ー]")
       then (if has("[\\p{Hiragana}\\p{Katakana}ー]") and has("\\p{Hangul}") then "Kana", "Hangul" else "Han/Kana/Hangul" end)
       else empty end),
      (if has("\\p{Cyrillic}") then "Cyrillic" else empty end),
      (if has("\\p{Arabic}") then "Arabic" else empty end),
      (if has("\\p{Greek}") then "Greek" else empty end),
      (if has("\\p{Hebrew}") then "Hebrew" else empty end),
      (if has("[^\\p{Latin}\\p{Han}\\p{Hiragana}\\p{Katakana}\\p{Hangul}ー\\p{Cyrillic}\\p{Arabic}\\p{Greek}\\p{Hebrew}\\P{L}]") then "other" else empty end)
    ] | join(" ")') || die "$name: title could not be checked"
  case $scripts in
    *other*) die "$name: title uses a script that is not accepted" ;;
    *" "*) die "$name: title mixes scripts: $scripts" ;;
  esac
  # Words: lowercase alphanumeric tokens ('#' kept so "#1" is a word).
  title_words=$(jq -nr --arg t "$title" "$fold_def"'$t | fold | scan("[\\p{L}\\p{N}#]+")') \
    || die "$name: title could not be checked"
  first_word=""
  while read -r w; do
    [ -n "$w" ] || continue
    [ -n "$first_word" ] || first_word=$w
    if inlist "$banned_words" "$w"; then die "$name: title contains the banned word \"$w\""; fi
  done <<<"$title_words"
  if [ "$fpx" -eq 0 ]; then
    if [ -n "$first_word" ] && b=$(brand_match "$first_word") && ! brand_owned "$b" "$owner"; then
      die "$name: title must not start with the brand \"$b\""
    fi
    # The title must share an alphanumeric word with the name.
    shared=0
    IFS=- read -ra ntoks <<<"$name"
    while read -r w; do
      # A word made of digits only does not count.
      for t in "${ntoks[@]}"; do [ "$w" = "$t" ] && [[ $w =~ [^0-9] ]] && shared=1; done
    done <<<"$(jq -nr --arg t "$title" '$t | ascii_downcase | scan("[\\p{L}\\p{N}]+")' || true)"
    [ "$shared" -eq 1 ] || die "$name: title must share a word with the name"
  fi
}

# check_description <plugin.json> <title> <name>: the listing rules for the description in
# the built plugin.json (also for first-party plugins). Run on every path that publishes a
# package: a fresh build and a reused archive. Values only enter jq as --arg.
# keep in sync with hatamoto-plugins scripts/validate-manifest.sh (follow-up there: it needs
# the same top-level key check, or a case-variant key bypasses the description rule)
check_description() {
  local pj=$1 title=$2 name=$3 pdesc jd dnorm desc_words seen_brands nbrands w
  # jq matches keys exactly, but the signed index is built by Go's encoding/json, which
  # matches them case-insensitively (and folds U+017F to s). Refuse any key that could
  # differ: every sdk.Manifest json tag is a lowercase identifier.
  if ! jq -e 'type == "object" and (keys | all(test("^[a-z][a-z0-9_]*$")))' "$pj" >/dev/null 2>&1; then
    die "$name: plugin.json keys must be lowercase identifiers"
  fi
  if ! jq -e '.description | type == "string"' "$pj" >/dev/null 2>&1; then
    die "$name: plugin.json description must be a string of 20 to 160 characters"
  fi
  pdesc=$(jq -r '.description' "$pj")
  jd() { jv "$pdesc" "$1" "$name: description" "${@:2}"; }
  # Length and control characters are judged on the raw value: command substitution
  # drops NULs and trailing newlines.
  if ! jq -e '.description | (length >= 20 and length <= 160 and (test("\\p{Cc}") | not))' "$pj" >/dev/null 2>&1; then
    die "$name: plugin.json description must be 20 to 160 characters without control characters"
  fi
  if jd '[$t | scan("[.!?。！？]")] | length > 2'; then
    die "$name: plugin.json description must have at most two sentence terminators"
  fi
  if jd '$t | test("://|www\\.|[A-Za-z0-9-]+[.\u3002\uFF0E\uFF61\u2024][A-Za-z]{2,}"; "i")'; then
    die "$name: plugin.json description must not contain a URL or domain"
  fi
  if jd '$t | test("\\p{M}|[\uFF00-\uFFEF]") or ($t | test("^[\\p{L}\\p{N} \u0021-\u007E\u3001\u3002\u2019]*$") | not)'; then
    die "$name: plugin.json description may contain only letters, digits, spaces, ASCII punctuation and 。 、 (no combining marks, fullwidth forms or symbols)"
  fi
  if jd "$unsafe_chars_filter"; then
    die "$name: plugin.json description must not contain emoji, symbols or invisible characters"
  fi
  if jd '($t | ascii_downcase | gsub("^\\s+|\\s+$"; "")) as $d | $d == ($title | ascii_downcase) or $d == $name' --arg title "$title" --arg name "$name"; then
    die "$name: plugin.json description must not equal the title or the name"
  fi
  dnorm=$(jq -nr --arg t "$pdesc" '$t | ascii_downcase | gsub("^\\s+|\\s+$"; "")') || dnorm=""
  if inlist "$placeholders" "$dnorm"; then die "$name: plugin.json description is a placeholder"; fi
  desc_words=$(jq -nr --arg t "$pdesc" "$fold_def"'$t | fold | scan("[\\p{L}\\p{N}#]+")') \
    || die "$name: description could not be checked"
  seen_brands=" "
  nbrands=0
  while read -r w; do
    [ -n "$w" ] || continue
    if [ "$w" != free ] && [ "$w" != plugin ] && inlist "$banned_words" "$w"; then
      die "$name: plugin.json description contains the banned word \"$w\""
    fi
    if brand_listed "$w" && [[ $seen_brands != *" $w "* ]]; then
      seen_brands="$seen_brands$w "
      nbrands=$((nbrands + 1))
    fi
  done <<<"$desc_words"
  if [ "$nbrands" -gt 3 ]; then die "$name: plugin.json description names more than 3 brands"; fi
}

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
  [ ! -L "$f" ] || die "$base: manifest is a symlink"
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
    if [ -z "$title" ] || [ "${#title}" -gt 40 ] || [[ $title =~ [[:cntrl:]] ]] || [[ $title == " "* ]] || [[ $title == *" " ]]; then
      die "$name: bad title (1 to 40 characters, no control characters, no leading or trailing space)"
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
    # A community listing is published by the owner of the repo (case-insensitive).
    [ "${author,,}" = "$(repo_owner "$repo")" ] || die "$name: author must equal the repo owner (author \"$author\", owner \"$(repo_owner "$repo")\")"
  fi
  jq -e '.archive_grant == true' "$f" >/dev/null || die "$name: archive_grant must be true"
  [[ $license =~ $license_re ]] || die "$name: license not allowed: \"$license\""
  # Listing rules (first-party from an allowed owner is exempt from the brand and reserved rules).
  fpx=0; [ -n "$tier" ] && [ -n "$owner_ok" ] && fpx=1
  check_name "$name" "$(repo_owner "$repo")" "$fpx" "$base"
  if [ -n "$title" ]; then check_title "$name" "$title" "$(repo_owner "$repo")" "$fpx"; fi
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
  check_description "$src/plugin.json" "$title" "$name"
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
    # The archived package is held to the description rule too: it must not bypass it.
    unzip -p "$hmp" plugin.json >"$stage/work/$name-archived-plugin.json" 2>/dev/null || die "$name: cannot read plugin.json from archived $hmp"
    check_description "$stage/work/$name-archived-plugin.json" "$title" "$name"
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
