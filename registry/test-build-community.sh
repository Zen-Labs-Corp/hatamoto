#!/usr/bin/env bash
# Offline tests for registry/build-community.sh.
# Negative cases (bad manifests) always run and never touch the network. The
# positive case builds a real wasm plugin from a local git repo, so it needs go
# and a warm module cache.
# Run: registry/test-build-community.sh   (HATAMOTO_BIN reuses a built binary)
set -euo pipefail
export LC_ALL=C.UTF-8

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(dirname "$here")
# File repos take their owner from the parent directory name: the fixtures live under "acme",
# the author of the fixture manifests.
top=$(mktemp -d)
work=$top/acme
mkdir -p "$work"
trap 'rm -rf "$top"' EXIT
script="$here/build-community.sh"
# The build is pinned to the backend's toolchain, as the Makefile does.
export HATAMOTO_BUILD_GOTOOLCHAIN=$(awk '/^toolchain/{print $2}' "$root/backend/go.mod")
fail() { echo "FAIL: $*" >&2; exit 1; }

if [ -n "${HATAMOTO_BIN:-}" ]; then bin=$HATAMOTO_BIN; else
  (cd "$root/backend" && go build -o "$work/hatamoto" ./cmd/hatamoto)
  bin=$work/hatamoto
fi
(cd "$root/backend" && go run ./cmd/hatamoto keys generate --name t --dir "$work" >/dev/null)
key=$work/t.key
[ -f "$key" ] || fail "throwaway key not created"

good=0123456789abcdef0123456789abcdef01234567
# manifest <dir> <file-name> <json>
manifest() {
  mkdir -p "$1/manifests"
  [ -d "$1/policy" ] || cp -r "$here/testdata/policy" "$1/policy"
  printf '%s' "$3" >"$1/manifests/$2.json"; }
base() { # base <name> <repo> <commit> [subdir]
  jq -n --arg n "$1" --arg r "$2" --arg c "$3" --arg s "${4:-}" \
    '{name:$n,repo:$r,commit:$c,author:"acme",license:"MIT",archive_grant:true,sdk:"^0.10"} + (if $s != "" then {subdir:$s} else {} end)'
}

# refuses <label> <expected-stderr-substring> <repo-dir>
refuses() {
  local out
  if out=$("$script" "$3" "$bin" "$key" "$work/out-pk" "$work/out-meta.json" 2>&1); then fail "$1: accepted"; fi
  grep -q "$2" <<<"$out" || fail "$1: wrong error: $out"
  [ ! -e "$work/out-meta.json" ] || fail "$1: wrote meta"
  ! ls "$work"/out-pk/*.hmp >/dev/null 2>&1 || fail "$1: wrote packages"
}

n=0
neg() { # neg <label> <file-name> <json> <substring>
  n=$((n + 1)); local d="$work/neg$n"
  manifest "$d" "$2" "$3"
  refuses "$1" "$4" "$d"
}
https=https://github.com/acme/hatamoto-acme-seo
neg "traversal name" "x" "$(base ../x $https $good)" "bad name"
neg "uppercase name" "Acme" "$(base Acme $https $good)" "bad name"
neg "name vs file" "other-name" "$(base acme-seo $https $good)" "must equal the file name"
neg "short commit" "acme-seo" "$(base acme-seo $https abc123)" "40 lowercase hex"
neg "http repo" "acme-seo" "$(base acme-seo http://github.com/acme/x $good)" "repo must be"
neg "non-github repo" "acme-seo" "$(base acme-seo https://example.com/acme/x $good)" "repo must be"
neg "file repo without opt-in" "acme-seo" "$(base acme-seo file:///tmp/x $good)" "repo must be"
neg "subdir dotdot" "acme-seo" "$(base acme-seo $https $good ../etc)" "bad subdir"
neg "subdir absolute" "acme-seo" "$(base acme-seo $https $good /etc)" "bad subdir"
neg "bad author" "acme-seo" "$(base acme-seo $https $good | jq '.author="-x"')" "bad author"
neg "empty license" "acme-seo" "$(base acme-seo $https $good | jq '.license=""')" "license not allowed"
neg "bad license" "acme-seo" "$(base acme-seo $https $good | jq '.license="WTFPL"')" "license not allowed"
neg "missing grant" "acme-seo" "$(base acme-seo $https $good | jq 'del(.archive_grant)')" "archive_grant must be true"
neg "false grant" "acme-seo" "$(base acme-seo $https $good | jq '.archive_grant=false')" "archive_grant must be true"
neg "string grant" "acme-seo" "$(base acme-seo $https $good | jq '.archive_grant="true"')" "archive_grant must be true"
neg "empty title" "acme-seo" "$(base acme-seo $https $good | jq '.title=""')" "bad title"
neg "control title" "acme-seo" "$(base acme-seo $https $good | jq '.title="a\u0007b"')" "bad title"
neg "long title" "acme-seo" "$(base acme-seo $https $good | jq --arg t "$(printf 'x%.0s' $(seq 61))" '.title=$t')" "bad title"
neg "tab title" "acme-seo" "$(base acme-seo $https $good | jq '.title="a\tb"')" "bad title"
neg "padded title" "acme-seo" "$(base acme-seo $https $good | jq '.title="a "')" "bad title"
neg "non-string title" "acme-seo" "$(base acme-seo $https $good | jq '.title=5')" "bad title"
echo "ok: $n negative cases"

# Titles that must pass pass 1: accents, a title of exactly 40 characters, a title that
# only shares one word with the name.
# (Pass 1 then fails at the fetch of a missing repo, which proves the title was accepted.)
for t in $'Acme SEO Caf\u00e9' "Acme SEO $(printf 'x%.0s' $(seq 31))" "Seo Tools"; do
  d="$work/titleok"; rm -rf "$d"
  manifest "$d" acme-seo "$(base acme-seo file://$work/nonexistent $good | jq --arg t "$t" '.title=$t')"
  out=$(HATAMOTO_ALLOW_FILE_REPOS=1 "$script" "$d" "$bin" "$key" "$work/t-pk" "$work/t-meta.json" 2>&1) && fail "title case built nothing"
  ! grep -q "bad title" <<<"$out" || fail "title '$t' rejected: $out"
  grep -q "cannot fetch" <<<"$out" || fail "title '$t': unexpected error: $out"
done
# An allowlisted license passes pass 1 (it then fails at the fetch).
d="$work/licok"; manifest "$d" acme-seo "$(base acme-seo file://$work/nonexistent $good | jq '.license="GPL-3.0-or-later"')"
out=$(HATAMOTO_ALLOW_FILE_REPOS=1 "$script" "$d" "$bin" "$key" "$work/l-pk" "$work/l-meta.json" 2>&1) && fail "license case built nothing"
grep -q "cannot fetch" <<<"$out" || fail "GPL-3.0-or-later rejected: $out"
echo "ok: title rule"

# The toolchain pin is required and must look like a Go version.
for bad in "" "local" "go1.27.2+auto" "auto"; do
  if out=$(HATAMOTO_BUILD_GOTOOLCHAIN=$bad "$script" "$work/neg1" "$bin" "$key" "$work/out-pk" "$work/out-meta.json" 2>&1); then fail "toolchain '$bad' accepted"; fi
  grep -q "HATAMOTO_BUILD_GOTOOLCHAIN" <<<"$out" || fail "toolchain '$bad': wrong error: $out"
done
echo "ok: toolchain pin"


# Positive case: a local repo holding a copy of the hello example.
plug="$work/plugrepo"
mkdir -p "$plug"
cp -r "$root/examples/plugins/hello/." "$plug/"
(cd "$plug" && go mod edit -dropreplace hatamoto.dev/sdk)
# "hello" is a reserved name (core ships the example), so the fixture plugin is called "acme-notes".
jq '.name = "acme-notes"' "$plug/plugin.json" >"$plug/plugin.json.new" && mv "$plug/plugin.json.new" "$plug/plugin.json"
git -C "$plug" init -q
git -C "$plug" add -A
git -C "$plug" -c user.name=t -c user.email=t@example.com commit -q -m init
sha=$(git -C "$plug" rev-parse HEAD)
reg="$work/registry-repo"
manifest "$reg" acme-notes "$(base acme-notes "file://$plug" "$sha" | jq '.title="Acme Notes"')"

# A name mismatch with plugin.json must fail before anything is published.
mm="$work/mismatch"
manifest "$mm" other "$(base other "file://$plug" "$sha")"
if HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" "$script" "$mm" "$bin" "$key" "$work/mm-pk" "$work/mm-meta.json" >"$work/mm.log" 2>&1; then
  fail "plugin.json name mismatch accepted"
fi
grep -q "plugin.json name does not match" "$work/mm.log" || fail "mismatch: wrong error: $(cat "$work/mm.log")"
[ ! -e "$work/mm-meta.json" ] || fail "mismatch wrote meta"

# Real plugins repos also hold manifests/schema.json and a README.
printf '{}' >"$reg/manifests/schema.json"
printf '# manifests\n' >"$reg/manifests/README.md"

# build_neg <label> <substring> <repo-dir> [packages-dir] [meta-file]
build_neg() {
  local out pk=${4:-$work/bn-pk} mt=${5:-$work/bn-meta.json}
  if out=$(HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" "$script" "$3" "$bin" "$key" "$pk" "$mt" 2>&1); then fail "$1: accepted"; fi
  grep -q "$2" <<<"$out" || fail "$1: wrong error: $out"
}
# plugrepo_variant <name> <mutator-command...>: a committed copy of the example repo, changed by the command run inside it.
variant() {
  local v="$work/var-$1"; shift
  cp -r "$plug" "$v"; rm -rf "$v/.git"
  (cd "$v" && "$@")
  git -C "$v" init -q; git -C "$v" add -A
  git -C "$v" -c user.name=t -c user.email=t@example.com commit -q -m v
  echo "$v"
}

# A first-party name already in packages or meta is refused, not replaced.
mkdir -p "$work/col-pk"; echo old >"$work/col-pk/acme-notes.hmp"
build_neg "package collision" "first-party name collision" "$reg" "$work/col-pk" "$work/col-meta.json"
[ "$(cat "$work/col-pk/acme-notes.hmp")" = old ] || fail "collision replaced the package"
printf '{"acme-notes":{"tier":"first-party"}}' >"$work/col-meta2.json"
build_neg "meta collision" "first-party name collision" "$reg" "$work/col-pk2" "$work/col-meta2.json"
[ "$(jq -r '.["acme-notes"].tier' "$work/col-meta2.json")" = first-party ] || fail "collision changed meta"

# A symlinked build source is refused.
v=$(variant symui sh -c 'rm -rf ui; ln -s /tmp ui')
sha2=$(git -C "$v" rev-parse HEAD)
manifest "$work/symrepo" acme-notes "$(base acme-notes "file://$v" "$sha2")"
build_neg "symlinked ui" "ui is a symlink" "$work/symrepo"
v=$(variant symsub sh -c 'mkdir -p real; mv main.go go.mod go.sum plugin.json real/; ln -s real sub')
manifest "$work/symsub" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)" sub)"
build_neg "symlinked subdir" "symlink or resolves outside" "$work/symsub"

# A committed prebuilt wasm must not be signed when the source does not compile.
v=$(variant broken sh -c 'mkdir -p build; echo fake >build/plugin.wasm; echo "package main; func main() { undefined() }" >main.go')
manifest "$work/broken" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)")"
build_neg "non-compiling source" "build failed" "$work/broken"

# A local-path replace is refused.
v=$(variant repl go mod edit -replace example.com/x=../x)
manifest "$work/repl" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)")"
build_neg "local replace" "local-path replace" "$work/repl"

# A replace of the SDK to any target (here a module path), or a go.work, is refused.
v=$(variant sdkrepl go mod edit -replace hatamoto.dev/sdk=github.com/someone/sdk@v0.10.0)
manifest "$work/sdkrepl" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)")"
build_neg "sdk module replace" "replaces hatamoto.dev/sdk" "$work/sdkrepl"
v=$(variant gowork sh -c 'printf "go 1.22\nuse .\n" >go.work')
manifest "$work/gowork" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)")"
build_neg "go.work at the root" "go.work is not allowed" "$work/gowork"
v=$(variant gowsub sh -c 'mkdir -p real; mv main.go go.mod go.sum plugin.json real/; printf "go 1.22\nuse ./real\n" >go.work')
manifest "$work/gowsub" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)" real)"
build_neg "go.work above the subdir" "go.work is not allowed" "$work/gowsub"
# A plugin that needs a newer Go than the pin fails clearly and downloads nothing.
v=$(variant newgo go mod edit -go 1.99.0)
manifest "$work/newgo" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)")"
build_neg "newer toolchain" "requires go >= 1.99" "$work/newgo"
echo "ok: build negatives"

# COMMUNITY_SKIP leaves a manifest out (even a broken one) and says so.
manifest "$work/skip" gone "$(base gone "file://$work/nonexistent" "$good")"
out=$(COMMUNITY_SKIP="gone" HATAMOTO_ALLOW_FILE_REPOS=1 "$script" "$work/skip" "$bin" "$key" "$work/skip-pk" "$work/skip-meta.json" 2>&1) || fail "skip: $out"
grep -q "SKIPPING gone" <<<"$out" || fail "skip not logged: $out"
for bad in "nosuch" "*" "gone nosuch" "schema"; do
  if out=$(COMMUNITY_SKIP="$bad" HATAMOTO_ALLOW_FILE_REPOS=1 "$script" "$work/skip" "$bin" "$key" "$work/skip-pk" "$work/skip-meta.json" 2>&1); then fail "skip '$bad' accepted"; fi
  grep -q "COMMUNITY_SKIP: no manifest named" <<<"$out" || fail "skip '$bad': wrong error: $out"
done
echo "ok: skip"

# Mirror: <base>/<name> carries refs/tags/src/<commit> on one commit above the pinned one.
# mkmirror <name> <upstream-dir> <mirror-base-dir> <mutator-command...>: the command runs in a
# checkout of the upstream and its changes become the top commit; prints nothing.
mkmirror() {
  local n=$1 up=$2 mb=$3; shift 3
  local m="$mb/$n" c; c=$(git -C "$up" rev-parse HEAD)
  git clone -q "$up" "$m"
  (cd "$m" && "$@")
  git -C "$m" add -A
  git -C "$m" -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m mirror
  git -C "$m" -c user.name=t -c user.email=t@example.com tag -a -m "src $c" "src/$c"
}
mirror_run() { # mirror_run <label> <repo-dir> <mirror-base-dir> <pk> <meta> [extra env...]; logs to $work/mr.log
  local d=$2 mb=$3 pk=$4 mt=$5; shift 5
  env HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_MIRROR_BASE="file://$mb" "$@" "$script" "$d" "$bin" "$key" "$pk" "$mt" >"$work/mr.log" 2>&1
}

# A mirror base must be an https github org URL (file:/// only under the test flag).
for bad in "http://github.com/o" "https://github.com/o/r" "https://example.com/o" "file:///tmp/x"; do
  if out=$(env -u HATAMOTO_ALLOW_FILE_REPOS HATAMOTO_MIRROR_BASE=$bad "$script" "$work/neg1" "$bin" "$key" "$work/out-pk" "$work/out-meta.json" 2>&1); then fail "mirror base '$bad' accepted"; fi
  grep -q "HATAMOTO_MIRROR_BASE" <<<"$out" || fail "mirror base '$bad': wrong error: $out"
done

# Upstream deleted, mirror alive: the plugin builds from the mirror and source is the mirror.
up="$work/up-notes"; cp -r "$plug" "$up"
mb="$work/mirrors"; mkdir -p "$mb"
mkmirror acme-notes "$up" "$mb" sh -c 'echo "{}" >MIRROR.json'
ghost="$work/ghost-reg"
manifest "$ghost" acme-notes "$(base acme-notes "file://$work/does-not-exist" "$sha")"
mirror_run "mirror build" "$ghost" "$mb" "$work/m-pk" "$work/m-meta.json" HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" || fail "mirror build: $(cat "$work/mr.log")"
[ -s "$work/m-pk/acme-notes.hmp" ] || fail "mirror build: no package"
jq -e --arg s "file://$mb/acme-notes" '."acme-notes".source == $s' "$work/m-meta.json" >/dev/null || fail "mirror source: $(cat "$work/m-meta.json")"
# Upstream alive: source stays the author's repo.
mirror_run "upstream source" "$reg" "$mb" "$work/m2-pk" "$work/m2-meta.json" HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" || fail "upstream source: $(cat "$work/mr.log")"
jq -e --arg s "file://$plug" '."acme-notes".source == $s' "$work/m2-meta.json" >/dev/null || fail "upstream source: $(cat "$work/m2-meta.json")"
echo "ok: mirror build"

# A mirror's vendor/ is ignored by default (-mod=readonly) and used only with
# HATAMOTO_TRUST_MIRROR_VENDOR=1 (-mod=vendor, GOPROXY=off). No SDK hook.
vup="$work/up-vend"; mkdir -p "$vup"
cat >"$vup/go.mod" <<'EOF'
module example.com/vend

go 1.22
EOF
printf 'package main\n\nfunc main() {}\n' >"$vup/main.go"
printf '{"name":"vend","version":"0.1.0","sdk":"^0.10","description":"Fixture that builds without dependencies."}\n' >"$vup/plugin.json"
git -C "$vup" init -q; git -C "$vup" add -A
git -C "$vup" -c user.name=t -c user.email=t@example.com commit -q -m init
vsha=$(git -C "$vup" rev-parse HEAD)
mkmirror vend "$vup" "$mb" sh -c 'mkdir vendor; printf "# no dependencies\n" >vendor/modules.txt; echo "{}" >MIRROR.json'
vreg="$work/vend-reg"
manifest "$vreg" vend "$(base vend "file://$work/does-not-exist" "$vsha")"
mirror_run "vendored" "$vreg" "$mb" "$work/v-pk" "$work/v-meta.json" HATAMOTO_TRUST_MIRROR_VENDOR=1 || fail "vendored build: $(cat "$work/mr.log")"
[ -s "$work/v-pk/vend.hmp" ] || fail "vendored build: no package"
grep -q "WARNING: HATAMOTO_TRUST_MIRROR_VENDOR=1" "$work/mr.log" || fail "trust mode: no warning: $(cat "$work/mr.log")"
# A vendor/modules.txt that disagrees with go.mod proves -mod=vendor was in effect.
v2up="$work/up-vend2"; cp -r "$vup" "$v2up"
printf '{"name":"vend2","version":"0.1.0","sdk":"^0.10","description":"Fixture that builds without dependencies."}\n' >"$v2up/plugin.json"
git -C "$v2up" -c user.name=t -c user.email=t@example.com commit -q -am vend2
mkmirror vend2 "$v2up" "$mb" sh -c 'mkdir vendor; printf "# example.com/x v1.0.0\n## explicit\nexample.com/x\n" >vendor/modules.txt'
manifest "$work/vend2-reg" vend2 "$(base vend2 "file://$work/does-not-exist" "$(git -C "$v2up" rev-parse HEAD)")"
if mirror_run "vendor in effect" "$work/vend2-reg" "$mb" "$work/v2-pk" "$work/v2-meta.json" HATAMOTO_TRUST_MIRROR_VENDOR=1; then fail "inconsistent vendor dir was built"; fi
grep -q "inconsistent vendoring" "$work/mr.log" || fail "vendor in effect: $(cat "$work/mr.log")"
# Default: the same inconsistent vendor/ is ignored, the build is go.sum-verified (upstream style).
mirror_run "vendor ignored" "$work/vend2-reg" "$mb" "$work/v2-pk" "$work/v2-meta.json" || fail "default build must ignore vendor/: $(cat "$work/mr.log")"
! grep -q "TRUST_MIRROR_VENDOR" "$work/mr.log" || fail "default build warned about trust mode"
# A vendor/ in the pinned commit itself is never trusted.
v3up="$work/up-vend3"; cp -r "$vup" "$v3up"
printf '{"name":"vend3","version":"0.1.0","sdk":"^0.10","description":"Fixture that builds without dependencies."}\n' >"$v3up/plugin.json"
mkdir "$v3up/vendor"; printf '# no dependencies\n' >"$v3up/vendor/modules.txt"
git -C "$v3up" add -A; git -C "$v3up" -c user.name=t -c user.email=t@example.com commit -q -am vend3
mkmirror vend3 "$v3up" "$mb" sh -c 'echo "{}" >MIRROR.json'
manifest "$work/vend3-reg" vend3 "$(base vend3 "file://$work/does-not-exist" "$(git -C "$v3up" rev-parse HEAD)")"
if mirror_run "pinned vendor" "$work/vend3-reg" "$mb" "$work/v3-pk" "$work/v3-meta.json" HATAMOTO_TRUST_MIRROR_VENDOR=1; then fail "pinned vendor/ trusted"; fi
grep -q "pinned commit itself contains vendor" "$work/mr.log" || fail "pinned vendor: $(cat "$work/mr.log")"
echo "ok: vendored mirror"

# A subdir plugin: the mirror may touch <subdir>/vendor/ and MIRROR.json, nothing else
# (neither the root vendor/ nor a sibling prefix such as sub/vendor-x/).
sup="$work/up-sub"; mkdir -p "$sup/sub"
cp "$vup/go.mod" "$vup/main.go" "$sup/sub/"
printf '{"name":"vsub","version":"0.1.0","sdk":"^0.10","description":"Fixture that builds without dependencies."}\n' >"$sup/sub/plugin.json"
git -C "$sup" init -q; git -C "$sup" add -A
git -C "$sup" -c user.name=t -c user.email=t@example.com commit -q -m init
ssha=$(git -C "$sup" rev-parse HEAD)
manifest "$work/sub-reg" vsub "$(base vsub "file://$work/does-not-exist" "$ssha" sub)"
smb="$work/mirrors-sub"; mkdir -p "$smb"
mkmirror vsub "$sup" "$smb" sh -c 'mkdir sub/vendor; printf "# no dependencies\n" >sub/vendor/modules.txt; echo "{}" >MIRROR.json'
mirror_run "subdir mirror" "$work/sub-reg" "$smb" "$work/s-pk" "$work/s-meta.json" || fail "subdir mirror rejected: $(cat "$work/mr.log")"
[ -s "$work/s-pk/vsub.hmp" ] || fail "subdir mirror: no package"
jq -e --arg s "file://$smb/vsub" '.vsub.source == $s' "$work/s-meta.json" >/dev/null || fail "subdir mirror source: $(cat "$work/s-meta.json")"
for bad in 'mkdir vendor; echo x >vendor/a' 'mkdir sub/vendor-x; echo x >sub/vendor-x/a' 'echo x >sub/main.go'; do
  rm -rf "$smb"; mkdir -p "$smb"
  mkmirror vsub "$sup" "$smb" sh -c "$bad"
  mirror_run "subdir bad" "$work/sub-reg" "$smb" "$work/s2-pk" "$work/s2-meta.json" && fail "subdir mirror touching '$bad' accepted"
  grep -q "possible tampering" "$work/mr.log" || fail "subdir bad '$bad': $(cat "$work/mr.log")"
done
echo "ok: subdir mirror"

# A tampered mirror (tag present, wrong content or parent) stops the run, even with a live upstream.
# A missing tag or repo is a miss and falls back to upstream.
bp="$work/mirrors-bad"; mkdir -p "$bp"
mkmirror acme-notes "$up" "$bp" sh -c 'echo x >main.go'
mirror_run "non-vendor path" "$ghost" "$bp" "$work/b1-pk" "$work/b1-meta.json" HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" && fail "non-vendor top commit accepted"
grep -q "possible tampering" "$work/mr.log" && ! grep -q "cannot fetch" "$work/mr.log" || fail "non-vendor path: $(cat "$work/mr.log")"
bp2="$work/mirrors-bad2"; mkdir -p "$bp2"
git clone -q "$up" "$bp2/acme-notes"
git -C "$bp2/acme-notes" -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m a
git -C "$bp2/acme-notes" -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m b
git -C "$bp2/acme-notes" -c user.name=t -c user.email=t@example.com tag -a -m x "src/$sha"
mirror_run "bad parent" "$ghost" "$bp2" "$work/b2-pk" "$work/b2-meta.json" HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" && fail "mirror with the wrong parent accepted"
grep -q "possible tampering" "$work/mr.log" || fail "bad parent: $(cat "$work/mr.log")"
# Mirror missing entirely and upstream dead: still "cannot fetch".
mirror_run "no mirror" "$ghost" "$work/mirrors-none" "$work/b3-pk" "$work/b3-meta.json" && fail "dead upstream and mirror built"
grep -q "cannot fetch" "$work/mr.log" || fail "no mirror: $(cat "$work/mr.log")"
# A tampered mirror with a live upstream still stops; a mirror lacking the tag falls back and succeeds.
mirror_run "tamper live" "$reg" "$bp" "$work/b4-pk" "$work/b4-meta.json" HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" && fail "tampered mirror ignored"
grep -q "possible tampering" "$work/mr.log" || fail "tamper live: $(cat "$work/mr.log")"
[ ! -e "$work/b4-pk/acme-notes.hmp" ] || fail "tamper published"
mirror_run "fallback" "$reg" "$work/mirrors" "$work/b5-pk" "$work/b5-meta.json" HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" HATAMOTO_MIRROR_BASE="file://$work/mirrors-none" || fail "fallback to upstream: $(cat "$work/mr.log")"
grep -q "no mirror tag" "$work/mr.log" || fail "fallback not logged: $(cat "$work/mr.log")"
echo "ok: mirror verification"

# Archive: new builds are kept, an unchanged name@commit is reused without building.
arch="$work/archive"
sdkenv=(HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk")
env "${sdkenv[@]}" ARCHIVE_DIR="$arch" "$script" "$reg" "$bin" "$key" "$work/a1-pk" "$work/a1-meta.json" >/dev/null || fail "archive build"
kid=$(jq -r .key_id "$key")
af="acme-notes-0.2.0-${sha:0:12}-$kid"
[ -s "$arch/acme-notes/$af.hmp" ] || fail "archive: no .hmp in $(find "$arch")"
jq -e --arg c "$sha" --arg k "$kid" --arg h "$(sha256sum "$arch/acme-notes/$af.hmp" | cut -d' ' -f1)" \
  '.name == "acme-notes" and .version == "0.2.0" and .commit == $c and .key_id == $k and .sha256 == $h' "$arch/acme-notes/$af.json" >/dev/null || fail "archive sidecar: $(cat "$arch/acme-notes/$af.json")"
cmp "$arch/acme-notes/$af.hmp" "$work/a1-pk/acme-notes.hmp" || fail "archived .hmp differs from the package"
# Upstream gone, no mirror, and a go that must not run: the archived bytes are reused.
shim="$work/shim"; mkdir -p "$shim"
printf '#!/bin/sh\necho "go must not run" >&2\nexit 99\n' >"$shim/go"; chmod +x "$shim/go"
reuse_run() { # reuse_run <repo-dir> <pk> <meta> [archive-dir]
  PATH="$shim:$PATH" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ARCHIVE_OFFLINE="${offline:-}" ARCHIVE_DIR="${4:-$arch}" "$script" "$1" "$bin" "$key" "$2" "$3" >"$work/ar.log" 2>&1
}
# Neither upstream nor a mirror is reachable: refused, unless the offline opt-in is set.
reuse_run "$ghost" "$work/a2-pk" "$work/a2-meta.json" && fail "archive reused although unauthenticated"
grep -q "cannot reach the mirror" "$work/ar.log" || fail "offline refusal: $(cat "$work/ar.log")"
offline=1
reuse_run "$ghost" "$work/a2-pk" "$work/a2-meta.json" || fail "archive reuse: $(cat "$work/ar.log")"
cmp "$arch/acme-notes/$af.hmp" "$work/a2-pk/acme-notes.hmp" || fail "reused .hmp differs"
grep -q "reusing archived" "$work/ar.log" || fail "reuse not logged"
grep -q "NOT checked" "$work/ar.log" || fail "offline reuse: no warning"
offline=
jq -e --arg c "$sha" '."acme-notes".commit == $c and ."acme-notes".tier == "community"' "$work/a2-meta.json" >/dev/null || fail "reuse meta: $(cat "$work/a2-meta.json")"
# A sidecar for another commit is not a match: it rebuilds (here: cannot fetch).
cp -r "$arch" "$work/arch-mm"
jq '.commit = "ffffffffffffffffffffffffffffffffffffffff"' "$arch/acme-notes/$af.json" >"$work/arch-mm/acme-notes/$af.json"
reuse_run "$ghost" "$work/a3-pk" "$work/a3-meta.json" "$work/arch-mm" && fail "mismatched sidecar reused"
grep -q "cannot fetch" "$work/ar.log" || fail "sidecar mismatch: $(cat "$work/ar.log")"
# ...and the rebuild may not overwrite the different archived file (append-only).
if env "${sdkenv[@]}" ARCHIVE_DIR="$work/arch-mm" "$script" "$reg" "$bin" "$key" "$work/a4-pk" "$work/a4-meta.json" >"$work/ar.log" 2>&1; then fail "append-only violated"; fi
grep -q "append-only" "$work/ar.log" || fail "append-only: $(cat "$work/ar.log")"
[ ! -e "$work/a4-pk/acme-notes.hmp" ] && [ ! -e "$work/a4-meta.json" ] || fail "append-only refusal published"
[ "$(jq -r .commit "$work/arch-mm/acme-notes/$af.json")" = ffffffffffffffffffffffffffffffffffffffff ] || fail "append-only changed the sidecar"
# Key rotation: a build signed by another key is never reused; it is rebuilt into its own file.
(cd "$root/backend" && go run ./cmd/hatamoto keys generate --name t2 --dir "$work" >/dev/null)
key2=$work/t2.key; kid2=$(jq -r .key_id "$key2"); [ "$kid2" != "$kid" ] || fail "rotation: same key id"
if PATH="$shim:$PATH" HATAMOTO_ALLOW_FILE_REPOS=1 ARCHIVE_DIR="$arch" "$script" "$ghost" "$bin" "$key2" "$work/k1-pk" "$work/k1-meta.json" >"$work/ar.log" 2>&1; then fail "archive built with another key was reused"; fi
grep -q "cannot fetch" "$work/ar.log" || fail "rotation reuse: $(cat "$work/ar.log")"
cp -r "$arch" "$work/arch-rot"
env "${sdkenv[@]}" ARCHIVE_DIR="$work/arch-rot" "$script" "$reg" "$bin" "$key2" "$work/k2-pk" "$work/k2-meta.json" >"$work/ar.log" 2>&1 || fail "rotation rebuild: $(cat "$work/ar.log")"
af2="acme-notes-0.2.0-${sha:0:12}-$kid2"
[ -s "$work/arch-rot/acme-notes/$af2.hmp" ] && [ -s "$work/arch-rot/acme-notes/$af.hmp" ] || fail "rotation: expected both files: $(ls "$work/arch-rot/acme-notes")"
cmp "$arch/acme-notes/$af.hmp" "$work/arch-rot/acme-notes/$af.hmp" || fail "rotation changed the old archive file"
jq -e --arg k "$kid2" '.key_id == $k' "$work/arch-rot/acme-notes/$af2.json" >/dev/null || fail "rotation sidecar"
# Both keys now archived: each reuses its own build.
PATH="$shim:$PATH" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ARCHIVE_OFFLINE=1 ARCHIVE_DIR="$work/arch-rot" "$script" "$ghost" "$bin" "$key2" "$work/k3-pk" "$work/k3-meta.json" >/dev/null 2>&1 || fail "rotation: key2 reuse"
cmp "$work/arch-rot/acme-notes/$af2.hmp" "$work/k3-pk/acme-notes.hmp" || fail "rotation: key2 reused the wrong file"
# A sidecar that lies about the package (version, or a file name that does not fit) dies.
cp -r "$arch" "$work/arch-ver"; jq '.version = "9.9.9"' "$arch/acme-notes/$af.json" >"$work/arch-ver/acme-notes/$af.json"
reuse_run "$reg" "$work/s1-pk" "$work/s1-meta.json" "$work/arch-ver" && fail "sidecar with a wrong version reused"
grep -q "holds plugin.json" "$work/ar.log" || fail "sidecar version: $(cat "$work/ar.log")"
cp -r "$arch" "$work/arch-fn"; wrong="acme-notes-0.2.0-${sha:0:11}z-$kid"
mv "$work/arch-fn/acme-notes/$af.hmp" "$work/arch-fn/acme-notes/$wrong.hmp"; mv "$work/arch-fn/acme-notes/$af.json" "$work/arch-fn/acme-notes/$wrong.json"
reuse_run "$reg" "$work/s2-pk" "$work/s2-meta.json" "$work/arch-fn" && fail "misnamed archive file reused"
grep -q "file name .* does not match" "$work/ar.log" || fail "sidecar file name: $(cat "$work/ar.log")"
# A tampered archived .hmp dies.
cp -r "$arch" "$work/arch-bad"; echo x >>"$work/arch-bad/acme-notes/$af.hmp"
reuse_run "$reg" "$work/a5-pk" "$work/a5-meta.json" "$work/arch-bad" && fail "tampered .hmp reused"
grep -q "sidecar sha256" "$work/ar.log" || fail "tamper: $(cat "$work/ar.log")"
# Rollback: an old genuine .hmp renamed to a newer commit with a forged sidecar is refused
# when upstream is reachable (plugin.json at the claimed commit says another version)...
v=$(variant newer sh -c 'sed -i "s/0.2.0/0.3.0/" plugin.json')
nsha=$(git -C "$v" rev-parse HEAD); nreg="$work/newer-reg"; manifest "$nreg" acme-notes "$(base acme-notes "file://$v" "$nsha")"
cp -r "$arch" "$work/arch-rb"
rb="acme-notes-0.2.0-${nsha:0:12}-$kid"
mv "$work/arch-rb/acme-notes/$af.hmp" "$work/arch-rb/acme-notes/$rb.hmp"; rm "$work/arch-rb/acme-notes/$af.json"
jq --arg c "$nsha" '.commit = $c' "$arch/acme-notes/$af.json" >"$work/arch-rb/acme-notes/$rb.json"
reuse_run "$nreg" "$work/rb-pk" "$work/rb-meta.json" "$work/arch-rb" && fail "rolled-back .hmp reused"
grep -q "does not match plugin.json" "$work/ar.log" || fail "rollback upstream: $(cat "$work/ar.log")"
# ...and when the commit is only reachable through the mirror.
mkdir -p "$work/mirrors-rb"; mkmirror acme-notes "$v" "$work/mirrors-rb" sh -c 'echo "{}" >MIRROR.json'
manifest "$work/ghost-newer" acme-notes "$(base acme-notes "file://$work/does-not-exist" "$nsha")"
mirror_run "rollback via mirror" "$work/ghost-newer" "$work/mirrors-rb" "$work/rb2-pk" "$work/rb2-meta.json" ARCHIVE_DIR="$work/arch-rb" && fail "rolled-back .hmp reused (mirror)"
grep -q "does not match plugin.json" "$work/mr.log" || fail "rollback mirror: $(cat "$work/mr.log")"
# A genuine archive through the mirror alone is accepted (the upstream is gone).
mirror_run "genuine via mirror" "$ghost" "$mb" "$work/g-pk" "$work/g-meta.json" ARCHIVE_DIR="$arch" || fail "genuine via mirror: $(cat "$work/mr.log")"
# No temp files are left in the archive.
! find "$arch" -name '*.tmp.*' | grep -q . || fail "temp files left in the archive"
# A failed run archives nothing: acme-notes builds, then a broken plugin fails.
v=$(variant broken2 sh -c 'echo "package main; func main() { undefined() }" >main.go')
fr="$work/failreg"; manifest "$fr" acme-notes "$(base acme-notes "file://$plug" "$sha")"
manifest "$fr" zzz "$(base zzz "file://$v" "$(git -C "$v" rev-parse HEAD)")"
sed -i 's/"acme-notes"/"zzz"/' "$v/plugin.json"; git -C "$v" -c user.name=t -c user.email=t@example.com commit -q -am n; manifest "$fr" zzz "$(base zzz "file://$v" "$(git -C "$v" rev-parse HEAD)")"
if env "${sdkenv[@]}" ARCHIVE_DIR="$work/arch-fail" "$script" "$fr" "$bin" "$key" "$work/f-pk" "$work/f-meta.json" >"$work/ar.log" 2>&1; then fail "failing run succeeded"; fi
grep -q "zzz: build failed" "$work/ar.log" || fail "failing run: $(cat "$work/ar.log")"
[ ! -e "$work/arch-fail" ] || fail "failed run archived: $(find "$work/arch-fail")"
# Source follows the mirror only when the mirror has the commit: an archive reuse with a
# mirror base that lacks the tag keeps the (dead) author repo; one that has it points there.
mirror_run "source nomirror" "$ghost" "$work/mirrors-none" "$work/sm1-pk" "$work/sm1-meta.json" ARCHIVE_DIR="$arch" HATAMOTO_ARCHIVE_OFFLINE=1 || fail "source nomirror: $(cat "$work/mr.log")"
jq -e --arg s "file://$work/does-not-exist" '."acme-notes".source == $s' "$work/sm1-meta.json" >/dev/null || fail "source without a mirror: $(cat "$work/sm1-meta.json")"
mirror_run "source mirror" "$ghost" "$mb" "$work/sm2-pk" "$work/sm2-meta.json" ARCHIVE_DIR="$arch" || fail "source mirror: $(cat "$work/mr.log")"
jq -e --arg s "file://$mb/acme-notes" '."acme-notes".source == $s' "$work/sm2-meta.json" >/dev/null || fail "source with a mirror: $(cat "$work/sm2-meta.json")"
echo "ok: archive"

# Tier first-party: label only, accepted for allowlisted owners, never silently downgraded,
# and the reserved names (also enforced by the hatamoto-plugins validator) are kept.
fp() { jq -n --arg n "$1" --arg r "$2" --arg c "$good" '{name:$n,repo:$r,commit:$c,license:"MIT",archive_grant:true,sdk:"^0.10",tier:"first-party"}'; }
fpn=0
fp_neg() { # fp_neg <label> <file-name> <json> <substring> [FIRST_PARTY_OWNERS]
  fpn=$((fpn + 1)); local d="$work/fpneg$fpn" out
  manifest "$d" "$2" "$3"
  if out=$(HATAMOTO_ALLOW_FILE_REPOS=1 FIRST_PARTY_OWNERS="${5-zenlabscorp Zen-Labs-Corp}" "$script" "$d" "$bin" "$key" "$work/fp-pk" "$work/fp-meta.json" 2>&1); then fail "$1: accepted"; fi
  grep -q "$4" <<<"$out" || fail "$1: wrong error: $out"
  [ ! -e "$work/fp-meta.json" ] || fail "$1: wrote meta"
}
zl=https://github.com/Zen-Labs-Corp
fp_neg "first-party from another owner" acme-seo "$(fp acme-seo https://github.com/acme/x)" "not allowed for the owner"
fp_neg "first-party with an empty allowlist" acme-seo "$(fp acme-seo $zl/x)" "not allowed for the owner" ""
for o in zenlabscorp-evil zenlabscorpx zenlabscorp.evil zen-labs-corp-evil zen-labs-corpx zen-labs-corp.evil; do
  fp_neg "look-alike owner $o" acme-seo "$(fp acme-seo https://github.com/$o/x)" "not allowed for the owner"
done
fp_neg "first-party from a file repo outside the owner dir" acme-seo "$(fp acme-seo file://$work/acme/x)" "not allowed for the owner" Zen-Labs-Corp
fp_neg "gtm first-party from another owner" gtm "$(fp gtm https://github.com/acme/gtm)" "reserved for a first-party plugin"
fp_neg "searchconsole first-party from another owner" searchconsole "$(fp searchconsole https://github.com/acme/x)" "reserved for a first-party plugin"
fp_neg "gtm without the tier" gtm "$(fp gtm $zl/gtm | jq 'del(.tier) + {author:"acme"}')" "reserved for a first-party plugin"
fp_neg "community gtm" gtm "$(base gtm https://github.com/acme/gtm $good)" "reserved for a first-party plugin"
fp_neg "webhooks first-party from the owner" webhooks "$(fp webhooks $zl/webhooks)" "name is reserved"
fp_neg "hello first-party from the owner" hello "$(fp hello $zl/hello)" "name is reserved"
fp_neg "community hello" hello "$(base hello https://github.com/acme/hello $good)" "name is reserved"
fp_neg "first-party with an author" acme-seo "$(fp acme-seo $zl/x | jq '.author="acme"')" "must not have an author"
fp_neg "tier community" acme-seo "$(fp acme-seo $zl/x | jq '.tier="community"')" "bad tier"
fp_neg "tier junk" acme-seo "$(fp acme-seo $zl/x | jq '.tier="gold"')" "bad tier"
fp_neg "tier not a string" acme-seo "$(fp acme-seo $zl/x | jq '.tier=true')" "bad tier"
echo "ok: $fpn first-party negative cases"

# Accepted: an allowlisted owner (case-insensitive) builds with no author in the meta.
for ownerdir in zen-labs-corp Zen-Labs-Corp; do
  fpup="$work/$ownerdir/gtm"; rm -rf "$work/zen-labs-corp" "$work/Zen-Labs-Corp"; mkdir -p "$fpup"
  cp -r "$plug/." "$fpup/"; rm -rf "$fpup/.git"
  jq '.name = "gtm"' "$fpup/plugin.json" >"$fpup/plugin.json.new" && mv "$fpup/plugin.json.new" "$fpup/plugin.json"
  git -C "$fpup" init -q; git -C "$fpup" add -A
  git -C "$fpup" -c user.name=t -c user.email=t@example.com commit -q -m init
  manifest "$work/fp-ok" gtm "$(fp gtm "file://$fpup" | jq --arg c "$(git -C "$fpup" rev-parse HEAD)" '.commit=$c | .title="Google Tag Manager"')"
  rm -rf "$work/fp-ok-pk" "$work/fp-ok-meta.json"
  FIRST_PARTY_OWNERS=Zen-Labs-Corp HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" \
    "$script" "$work/fp-ok" "$bin" "$key" "$work/fp-ok-pk" "$work/fp-ok-meta.json" >"$work/fp.log" 2>&1 || fail "first-party build ($ownerdir): $(cat "$work/fp.log")"
  [ -s "$work/fp-ok-pk/gtm.hmp" ] || fail "first-party build: no package"
  jq -e --arg s "file://$fpup" '.gtm == {source: $s, commit: .gtm.commit, tier: "first-party", title: "Google Tag Manager"} and (.gtm | has("author") | not)' "$work/fp-ok-meta.json" >/dev/null \
    || fail "first-party meta wrong: $(cat "$work/fp-ok-meta.json")"
done
echo "ok: first-party build"

# Listing rules (author, name, title): each case isolates one rule and must publish nothing.
# The repo owner of an https manifest is the owner in its URL; a file repo uses the parent directory.
ln0=$n
neg "author is not the owner" "acme-seo" "$(base acme-seo https://github.com/someoneelse/x $good | jq '.author="google"')" "author must equal the repo owner"
neg "name with a banned token" "acme-demo" "$(base acme-demo $https $good)" "banned word \"demo\""
neg "reserved name" "forms" "$(base forms $https $good)" "name is reserved"
neg "brand-prefixed name" "stripe-checkout" "$(base stripe-checkout $https $good)" "must not start with the brand \"stripe\""
neg "brand-glued name" "stripecheckout" "$(base stripecheckout $https $good)" "must not start with the brand \"stripe\""
neg "look-alike of a reserved name" "seos" "$(base seos $https $good)" "looks like \"seo\""
neg "look-alike with digits" "s3o" "$(base s3o $https $good)" "looks like \"seo\""
# A sibling manifest in manifests/themes/ is a look-alike target too.
d="$work/lookalike"; manifest "$d" acme-seo "$(base acme-seo $https $good)"
mkdir -p "$d/manifests/themes"; printf '{}' >"$d/manifests/themes/acme-seq.json"
refuses "look-alike of a theme" 'looks like "acme-seq"' "$d"
# ...and of a plugin manifest that COMMUNITY_SKIP leaves out of the build.
d="$work/lookalike2"; manifest "$d" acme-seo "$(base acme-seo $https $good)"; manifest "$d" acme-sea "$(base acme-sea $https $good)"
if out=$(COMMUNITY_SKIP=acme-sea "$script" "$d" "$bin" "$key" "$work/out-pk" "$work/out-meta.json" 2>&1); then fail "look-alike of a skipped manifest accepted"; fi
grep -q 'looks like "acme-sea"' <<<"$out" || fail "skipped look-alike: $out"
neg "title with a separator pile" "acme-seo" "$(base acme-seo $https $good | jq '.title="Acme SEO, Leads & Email"')" "more than one separator"
neg "the example title" "acme-seo" "$(base acme-seo $https $good | jq '.title="Lead Delivery: HubSpot, Zapier, Webhooks & Email"')" "bad title"
neg "title with brackets" "acme-seo" "$(base acme-seo $https $good | jq '.title="Acme SEO [Beta]"')" "must not contain brackets"
neg "title with an emoji" "acme-seo" "$(base acme-seo $https $good | jq '.title="Acme SEO \ud83d\ude80"')" "emoji, symbols or invisible"
neg "title with a symbol" "acme-seo" "$(base acme-seo $https $good | jq '.title="Acme SEO ★"')" "emoji, symbols or invisible"
neg "title mixing scripts" "acme-seo" "$(base acme-seo $https $good | jq '.title="Acme SEO 定期定額"')" "mixes scripts"
neg "title with a look-alike letter mix" "acme-seo" "$(base acme-seo $https $good | jq '.title="Acme SEO Кyrillic"')" "mixes scripts"
neg "title with a banned word" "acme-seo" "$(base acme-seo $https $good | jq '.title="Acme SEO Pro"')" "banned word \"pro\""
neg "title with a folded banned word" "acme-seo" "$(base acme-seo $https $good | jq '.title="тор"')" "banned word \"top\""
neg "title starting with a brand" "acme-seo" "$(base acme-seo $https $good | jq '.title="Stripe Acme SEO"')" "must not start with the brand \"stripe\""
neg "title sharing no word" "acme-seo" "$(base acme-seo $https $good | jq '.title="Lead Delivery"')" "share a word"
neg "title sharing only a number" "acme-seo-2" "$(base acme-seo-2 $https $good | jq '.title="Seven 2"')" "share a word"
neg "title with fullwidth forms" "acme-seo" "$(base acme-seo $https $good | jq '.title="Acme SEO！"')" "fullwidth"
echo "ok: $((n - ln0)) listing rule negative cases"
# The owner comparison ignores case; a brand the owner is listed for is fine (the build then
# fails at the fetch of a missing repo, which proves pass 1 accepted the manifest).
d="$work/listok"; manifest "$d" acme-seo "$(base acme-seo "file://$work/nonexistent" $good | jq '.author="ACME"')"
out=$(HATAMOTO_ALLOW_FILE_REPOS=1 "$script" "$d" "$bin" "$key" "$work/lo-pk" "$work/lo-meta.json" 2>&1) && fail "listing case built nothing"
grep -q "cannot fetch" <<<"$out" || fail "author ACME rejected: $out"
d="$work/listok2"; manifest "$d" google-notes "$(base google-notes "file://$work/zen-labs-corp/x" $good | jq '.author="zen-labs-corp" | .title="Google Notes"')"
out=$(HATAMOTO_ALLOW_FILE_REPOS=1 "$script" "$d" "$bin" "$key" "$work/lo-pk" "$work/lo-meta.json" 2>&1) && fail "owner brand built nothing"
grep -q "cannot fetch" <<<"$out" || fail "brand owner rejected: $out"
neg "brand without its owner" "google-notes" "$(base google-notes $https $good | jq '.title="Google Notes"')" "must not start with the brand \"google\""

# Policy: missing, empty, symlinked or unreadable lists stop the build before pass 1.
mkpol() { # mkpol <dir>: a registry dir with one valid manifest and a copy of the policy
  rm -rf "$1"; manifest "$1" acme-seo "$(base acme-seo $https $good)"
}
d="$work/pol-none"; rm -rf "$d"; mkdir -p "$d/manifests"; printf '%s' "$(base acme-seo $https $good)" >"$d/manifests/acme-seo.json"
refuses "no policy dir" "missing policy" "$d"
d="$work/pol-nomanifests"; rm -rf "$d"; mkdir -p "$d/manifests"
refuses "no policy dir, no manifests" "missing policy" "$d"
for f in reserved-names banned-tokens banned-words brands placeholders; do
  d="$work/pol-miss-$f"; mkpol "$d"; rm "$d/policy/$f.txt"
  refuses "missing $f" "missing policy: $f.txt" "$d"
done
for f in reserved-names banned-tokens banned-words brands; do
  d="$work/pol-empty-$f"; mkpol "$d"; : >"$d/policy/$f.txt"
  refuses "empty $f" "empty policy: $f.txt" "$d"
  printf '# only a comment\n\n   \n' >"$d/policy/$f.txt"
  refuses "comment-only $f" "empty policy: $f.txt" "$d"
done
# placeholders.txt may be empty: pass 1 then accepts the manifest and fails at the fetch.
d="$work/pol-empty-ph"; manifest "$d" acme-seo "$(base acme-seo "file://$work/nonexistent" $good)"; : >"$d/policy/placeholders.txt"
out=$(HATAMOTO_ALLOW_FILE_REPOS=1 "$script" "$d" "$bin" "$key" "$work/ph-pk" "$work/ph-meta.json" 2>&1) && fail "empty placeholders built nothing"
grep -q "cannot fetch" <<<"$out" || fail "empty placeholders: $out"
d="$work/pol-link"; mkpol "$d"; mv "$d/policy/brands.txt" "$work/brands-real.txt"; ln -s "$work/brands-real.txt" "$d/policy/brands.txt"
refuses "symlinked policy file" "missing policy: brands.txt" "$d"
d="$work/pol-linkdir"; mkpol "$d"; mv "$d/policy" "$work/policy-real"; ln -s "$work/policy-real" "$d/policy"
refuses "symlinked policy dir" "missing policy" "$d"
if [ "$(id -u)" -ne 0 ]; then
  d="$work/pol-unread"; mkpol "$d"; chmod 000 "$d/policy/banned-words.txt"
  refuses "unreadable policy file" "missing policy: banned-words.txt" "$d"
  chmod 644 "$d/policy/banned-words.txt"
fi
# A symlinked manifest is refused.
d="$work/man-link"; rm -rf "$d"; mkdir -p "$d/manifests"; printf '%s' "$(base acme-seo $https $good)" >"$work/real-manifest.json"
manifest "$d" other "{}"; rm "$d/manifests/other.json"; ln -s "$work/real-manifest.json" "$d/manifests/acme-seo.json"
refuses "symlinked manifest" "manifest is a symlink" "$d"
echo "ok: policy"

# Descriptions are judged on the built plugin.json (first-party too), before anything is packed.
# desc_neg <label> <substring> <description> [title]
desc_neg() {
  local v; dn=$((dn + 1)); v=$(variant "desc$dn" sh -c 'jq --arg d "$1" ".description=\$d" plugin.json >p.new && mv p.new plugin.json' sh "$3")
  manifest "$work/descreg$dn" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)" | jq --arg t "${4:-Acme Notes}" '.title=$t')"
  rm -f "$work/bn-meta.json"; rm -rf "$work/bn-pk"
  build_neg "$1" "$2" "$work/descreg$dn"
  [ ! -e "$work/bn-meta.json" ] && ! ls "$work"/bn-pk/*.hmp >/dev/null 2>&1 || fail "$1: published"
}
dn=0
desc_neg "description of 400 characters" "20 to 160 characters" "$(printf 'x%.0s' $(seq 400))"
desc_neg "short description" "20 to 160 characters" "too short"
desc_neg "description with a URL" "URL or domain" "Syncs notes, see https://example.com for details"
desc_neg "description with a domain" "URL or domain" "Syncs notes from example.com into the site"
desc_neg "description from the SDK example" "placeholder" "One sentence a site owner will read in the plugin directory."
desc_neg "description equal to the title" "equal the title" "Acme Notes Sync Tool Kit" "Acme Notes Sync Tool Kit"
desc_neg "description with many sentences" "sentence terminators" "Syncs notes. Fast. Simple. Done."
desc_neg "description with a symbol" "may contain only letters" "Syncs notes to the site ★ with care"
desc_neg "description with a banned word" "banned word \"best\"" "The best way to keep notes on your site"
desc_neg "description with four brands" "more than 3 brands" "Works with stripe, paypal, shopify and hubspot orders"
# Go's decoder folds key case (and U+017F to s), jq does not: such keys are refused.
v=$(variant keycase sh -c 'jq ". + {Description: \"BEST FREE SEO now for every site\"}" plugin.json >p.new && mv p.new plugin.json')
manifest "$work/keycase" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)")"
build_neg "description key with a capital" "keys must be lowercase identifiers" "$work/keycase"
v=$(variant keyfold sh -c 'jq ". + {\"deſcription\": \"BEST FREE SEO now for every site\"}" plugin.json >p.new && mv p.new plugin.json')
manifest "$work/keyfold" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)")"
build_neg "description key with U+017F" "keys must be lowercase identifiers" "$work/keyfold"
v=$(variant nodesc sh -c 'jq "del(.description)" plugin.json >p.new && mv p.new plugin.json')
manifest "$work/nodesc" acme-notes "$(base acme-notes "file://$v" "$(git -C "$v" rev-parse HEAD)")"
build_neg "missing description" "description must be a string" "$work/nodesc"
# A banned description is refused for a first-party plugin too.
v=$(variant fpdesc sh -c 'jq ".name=\"gtm\" | .description=\"The best way to tag your site\"" plugin.json >p.new && mv p.new plugin.json')
mkdir -p "$work/zen-labs-corp"; rm -rf "$work/zen-labs-corp/gtm"; cp -r "$v" "$work/zen-labs-corp/gtm"
manifest "$work/fpdesc" gtm "$(fp gtm "file://$work/zen-labs-corp/gtm" | jq --arg c "$(git -C "$v" rev-parse HEAD)" '.commit=$c | .title="Google Tag Manager"')"
if out=$(FIRST_PARTY_OWNERS="Zen-Labs-Corp" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" "$script" "$work/fpdesc" "$bin" "$key" "$work/fpd-pk" "$work/fpd-meta.json" 2>&1); then fail "first-party banned description accepted"; fi
grep -q 'banned word "best"' <<<"$out" || fail "first-party description: $out"
echo "ok: description (build)"

# The same rule holds when the package is reused from the archive: a tampered-in-place
# plugin.json inside an archived .hmp (sidecar sha256 refreshed) is refused before reuse.
if command -v zip >/dev/null; then
  arch2="$work/archive-desc"; rm -rf "$arch2"; cp -r "$arch" "$arch2"
  z="$work/zipwork"; rm -rf "$z"; mkdir "$z"
  unzip -q -o "$arch2/acme-notes/$af.hmp" plugin.json -d "$z"
  jq '.description="The best way to keep notes on your site"' "$z/plugin.json" >"$z/p.new" && mv "$z/p.new" "$z/plugin.json"
  (cd "$z" && zip -q "$arch2/acme-notes/$af.hmp" plugin.json)
  jq --arg h "$(sha256sum "$arch2/acme-notes/$af.hmp" | cut -d' ' -f1)" '.sha256=$h' "$arch2/acme-notes/$af.json" >"$z/side.json" && mv "$z/side.json" "$arch2/acme-notes/$af.json"
  offline=1
  reuse_run "$reg" "$work/ad-pk" "$work/ad-meta.json" "$arch2" && fail "archived package with a banned description reused"
  offline=
  grep -q 'banned word "best"' "$work/ar.log" || fail "archive description: $(cat "$work/ar.log")"
  [ ! -e "$work/ad-meta.json" ] && [ ! -e "$work/ad-pk/acme-notes.hmp" ] || fail "archive description: published"
  # A case-variant key in an archived plugin.json is refused too.
  arch3="$work/archive-key"; rm -rf "$arch3"; cp -r "$arch" "$arch3"
  rm -rf "$z"; mkdir "$z"
  unzip -q -o "$arch3/acme-notes/$af.hmp" plugin.json -d "$z"
  jq '. + {Description: "BEST FREE SEO now for every site"}' "$z/plugin.json" >"$z/p.new" && mv "$z/p.new" "$z/plugin.json"
  (cd "$z" && zip -q "$arch3/acme-notes/$af.hmp" plugin.json)
  jq --arg h "$(sha256sum "$arch3/acme-notes/$af.hmp" | cut -d' ' -f1)" '.sha256=$h' "$arch3/acme-notes/$af.json" >"$z/side.json" && mv "$z/side.json" "$arch3/acme-notes/$af.json"
  offline=1
  reuse_run "$reg" "$work/ak-pk" "$work/ak-meta.json" "$arch3" && fail "archived package with a capital key reused"
  offline=
  grep -q 'keys must be lowercase identifiers' "$work/ar.log" || fail "archive key: $(cat "$work/ar.log")"
  echo "ok: description (archive)"
else
  echo "skip: description (archive): zip not installed"
fi

printf '{"existing":{"tier":"first-party"}}' >"$work/pos-meta.json"
HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_SDK_REPLACE="$root/backend/pkg/sdk" \
  "$script" "$reg" "$bin" "$key" "$work/pos-pk" "$work/pos-meta.json"
[ -s "$work/pos-pk/acme-notes.hmp" ] || fail "acme-notes.hmp not built"
jq -e --arg c "$sha" '."acme-notes".commit == $c and ."acme-notes".tier == "community" and ."acme-notes".author == "acme" and ."acme-notes".title == "Acme Notes" and .existing.tier == "first-party"' "$work/pos-meta.json" >/dev/null \
  || fail "meta.json wrong: $(cat "$work/pos-meta.json")"
echo "ok: positive build"
echo "PASS"
