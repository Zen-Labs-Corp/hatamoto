#!/usr/bin/env bash
# Offline tests for registry/build-community-kits.sh. No network; go is used here
# only to build the CLI.
# Run: registry/test-build-community-kits.sh   (HATAMOTO_BIN reuses a built binary)
set -euo pipefail
export LC_ALL=C.UTF-8

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(dirname "$here")
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
script="$here/build-community-kits.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }

if [ -n "${HATAMOTO_BIN:-}" ]; then bin=$HATAMOTO_BIN; else
  (cd "$root/backend" && go build -o "$work/hatamoto" ./cmd/hatamoto)
  bin=$work/hatamoto
fi
"$bin" keys generate --name t --dir "$work" >/dev/null
"$bin" keys generate --name t2 --dir "$work" >/dev/null
key=$work/t.key key2=$work/t2.key
[ -f "$key" ] && [ -f "$key2" ] || fail "throwaway keys not created"
export HATAMOTO_ALLOW_FILE_REPOS=1

# mkkit <dir> <name> [version]: a kit source tree (no plugins or themes: nothing to resolve).
mkkit() {
  mkdir -p "$1"
  jq -n --arg n "$2" --arg v "${3:-1.0.0}" '{name:$n,version:$v,kit:1,sdk:"^0.27",title:"Community",
    schemas:[{slug:"story",name:"Stories",schema:{type:"object"}}]}' >"$1/kit.json"
}
gitinit() { git -C "$1" init -q; git -C "$1" add -A; git -C "$1" -c user.name=t -c user.email=t@example.com commit -q -m init; git -C "$1" rev-parse HEAD; }
# plugins checkout with the policy files and one kit manifest.
plugins() { mkdir -p "$1/manifests/kits"; [ -d "$1/policy" ] || cp -r "$here/testdata/policy" "$1/policy"; }
manifest() { # manifest <plugins-dir> <file-name> <json>
  plugins "$1"; printf '%s' "$3" >"$1/manifests/kits/$2.json"
}
base() { # base <name> <repo-dir> <commit> [subdir]
  jq -n --arg n "$1" --arg r "file://$2" --arg c "$3" --arg s "${4:-}" \
    '{name:$n,repo:$r,commit:$c,author:"acme",license:"MIT",archive_grant:true,title:"Community",about:"A community site.",features:["Ranked stories"]} + (if $s != "" then {subdir:$s} else {} end)'
}

src=$work/src; mkkit "$src" community; commit=$(gitinit "$src")
pl=$work/plugins; manifest "$pl" community "$(base community "$src" "$commit")"

# run <plugins-dir> [env...]: output dirs live under $work/o.
run() { local d=$1; shift; env "$@" "$script" "$d" "$bin" "${KEY:-$key}" "$work/o/pk" "$work/o/meta.json" "${ARCH:-$work/o/arch}" 2>&1; }
refuses() { # refuses <label> <substring> <plugins-dir> [env...]
  local out l=$1 s=$2 d=$3; shift 3
  rm -rf "$work/o"
  if out=$(run "$d" "$@"); then fail "$l: accepted"; fi
  grep -q -- "$s" <<<"$out" || fail "$l: wrong error: $out"
  [ ! -e "$work/o" ] || fail "$l: wrote output: $(find "$work/o" | head)"
}

# 1. A signed kit is built from the pinned local repo and verifies.
rm -rf "$work/o"
out=$(run "$pl") || fail "build: $out"
[ -f "$work/o/pk/community.kit.hmp" ] || fail "no community.kit.hmp: $out"
"$bin" kit check --package "$work/o/pk/community.kit.hmp" --expect-name community | grep -qx 'kit community 1.0.0: ok' || fail "output does not verify"
jq -e --arg c "$commit" '.community.tier == "community" and .community.commit == $c and .community.author == "acme" and .community.features == ["Ranked stories"]' "$work/o/meta.json" >/dev/null || fail "meta: $(cat "$work/o/meta.json")"
side=$(ls "$work/o/arch/community/"*.kit.json)
jq -e '.kind == "kit" and .version == "1.0.0"' "$side" >/dev/null || fail "sidecar: $(cat "$side")"

# 2. Reuse: the archived build comes back byte for byte (the source is not rebuilt).
cp "$work/o/pk/community.kit.hmp" "$work/first.hmp"
rm -rf "$work/o/pk" "$work/o/meta.json"
out=$(run "$pl") || fail "reuse: $out"
grep -q "reusing archived" <<<"$out" || fail "no reuse: $out"
cmp -s "$work/first.hmp" "$work/o/pk/community.kit.hmp" || fail "reused build differs"
# A reused build is checked against the pin: a tampered archive file is refused.
rm -rf "$work/o/pk" "$work/o/meta.json"
printf 'x' >>"$work/o/arch/community/"*.kit.hmp
refuses_out=$(run "$pl" 2>&1) && fail "tampered archive accepted"
grep -q "does not match its sidecar" <<<"$refuses_out" || fail "tampered archive: wrong error: $refuses_out"

# 3. A plugin of the same name does not stop the kit; the plugin's meta entry is kept and the kit's goes under kit:<name>.
rm -rf "$work/o"; mkdir -p "$work/o/pk"
printf 'plugin' >"$work/o/pk/community.hmp"
printf '{"community":{"tier":"community"}}' >"$work/o/meta.json"
out=$(run "$pl") || fail "shared name: $out"
[ -f "$work/o/pk/community.kit.hmp" ] && [ "$(cat "$work/o/pk/community.hmp")" = plugin ] || fail "shared name: wrong files"
jq -e '.community == {"tier":"community"}' "$work/o/meta.json" >/dev/null || fail "plugin meta overwritten: $(cat "$work/o/meta.json")"
jq -e --arg c "$commit" '.["kit:community"].tier == "community" and .["kit:community"].commit == $c' "$work/o/meta.json" >/dev/null || fail "kit meta missing: $(cat "$work/o/meta.json")"
# Same, with the plugin only in the meta file (a bare entry from another source).
rm -rf "$work/o"; mkdir -p "$work/o"
printf '{"community":{"tier":"community","source":"https://example.org/other"}}' >"$work/o/meta.json"
out=$(run "$pl") || fail "shared name, meta only: $out"
jq -e '.community.source == "https://example.org/other" and (.["kit:community"].tier == "community")' "$work/o/meta.json" >/dev/null || fail "meta-only shared name: $(cat "$work/o/meta.json")"

# 4. Kit-vs-kit collisions, duplicates and bad manifests are refused.
rm -rf "$work/o"; mkdir -p "$work/o/pk"; : >"$work/o/pk/community.kit.hmp"
out=$(run "$pl") && fail "kit collision accepted"; grep -q "kit name collision" <<<"$out" || fail "collision: $out"
dup=$work/dup; manifest "$dup" community "$(base community "$src" "$commit")"; manifest "$dup" copy "$(base community "$src" "$commit")"
refuses "duplicate name" "must equal the file name" "$dup"
n=0
neg() { n=$((n + 1)); local d="$work/neg$n"; manifest "$d" "$2" "$3"; refuses "$1" "$4" "$d"; }
neg "bad commit" community "$(base community "$src" 0123456789abcdef0123456789abcdef01234567)" "cannot fetch"
neg "short commit" community "$(base community "$src" abc123)" "commit must be 40"
neg "no grant" community "$(base community "$src" "$commit" | jq 'del(.archive_grant)')" "archive_grant must be true"
neg "bad license" community "$(base community "$src" "$commit" | jq '.license="WTFPL"')" "license not allowed"
neg "unknown field" community "$(base community "$src" "$commit" | jq '.tier="first-party"')" "unknown field"
neg "https repo only" community "$(base community "$src" "$commit" | jq '.repo="http://example.com/a/b"')" "repo must be"
neg "bad subdir" community "$(base community "$src" "$commit" | jq '.subdir="../x"')" "bad subdir"
neg "reserved name" seo "$(base seo "$src" "$commit")" "reserved"
# A kit that does not match its manifest name, or breaks the kit rules, is refused by the CLI.
neg "wrong kit name" other "$(base other "$src" "$commit")" "pack failed"
bad=$work/badsrc; mkkit "$bad" community; jq '.mystery=1' "$bad/kit.json" >"$bad/k" && mv "$bad/k" "$bad/kit.json"; badc=$(gitinit "$bad")
neg "invalid kit.json" community "$(base community "$bad" "$badc")" "pack failed"

# 5. subdir, skip, and no kits directory.
sub=$work/subsrc; mkkit "$sub/kits/community" community 2.1.0; subc=$(gitinit "$sub")
manifest "$work/plsub" community "$(base community "$sub" "$subc" kits/community)"
rm -rf "$work/o"; out=$(run "$work/plsub") || fail "subdir: $out"
"$bin" kit check --package "$work/o/pk/community.kit.hmp" | grep -qx 'kit community 2.1.0: ok' || fail "subdir build"
refuses "skip of an unknown name" "no kit manifest named" "$pl" HATAMOTO_KITS_SKIP=nope
rm -rf "$work/o"; out=$(run "$pl" HATAMOTO_KITS_SKIP=community) || fail "skip: $out"
grep -q "SKIPPING community" <<<"$out" || fail "skip not logged"
[ ! -e "$work/o/pk/community.kit.hmp" ] || fail "skipped kit was built"
mkdir -p "$work/nokits"; rm -rf "$work/o"
out=$("$script" "$work/nokits" "$bin" "$key" "$work/o/pk" "$work/o/meta.json" "$work/o/arch" 2>&1) || fail "no manifests/kits must be a no-op: $out"
[ ! -e "$work/o" ] || fail "no-op wrote output"

echo "ok: build-community-kits"
