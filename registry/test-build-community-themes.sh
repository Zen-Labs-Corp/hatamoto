#!/usr/bin/env bash
# Offline tests for registry/build-community-themes.sh. No network, no Go
# toolchain for the builder itself (go is used here only to build the CLI and a
# tiny fixture helper).
# Run: registry/test-build-community-themes.sh   (HATAMOTO_BIN reuses a built binary)
set -euo pipefail
export LC_ALL=C.UTF-8

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(dirname "$here")
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
script="$here/build-community-themes.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }

if [ -n "${HATAMOTO_BIN:-}" ]; then bin=$HATAMOTO_BIN; else
  (cd "$root/backend" && go build -o "$work/hatamoto" ./cmd/hatamoto)
  bin=$work/hatamoto
fi
"$bin" keys generate --name t --dir "$work" >/dev/null
"$bin" keys generate --name t2 --dir "$work" >/dev/null
key=$work/t.key key2=$work/t2.key
[ -f "$key" ] && [ -f "$key2" ] || fail "throwaway keys not created"

# Fixture helper: png <out> <w> <h> | zip <out> <name=file>... (names may be hostile).
mkdir -p "$work/helper"
cat >"$work/helper/main.go" <<'GO'
package main

import (
	"archive/zip"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"
)

func main() {
	switch os.Args[1] {
	case "png":
		w, _ := strconv.Atoi(os.Args[3])
		h, _ := strconv.Atoi(os.Args[4])
		f, _ := os.Create(os.Args[2])
		png.Encode(f, image.NewGray(image.Rect(0, 0, w, h)))
		f.Close()
	case "zip":
		f, _ := os.Create(os.Args[2])
		zw := zip.NewWriter(f)
		for _, a := range os.Args[3:] {
			n, src, _ := strings.Cut(a, "=")
			b, _ := os.ReadFile(src)
			w, _ := zw.Create(n)
			w.Write(b)
		}
		zw.Close()
		f.Close()
	}
}
GO
(cd "$work/helper" && go build -o "$work/fx" main.go)
fx=$work/fx
"$fx" png "$work/shot.png" 800 450
"$fx" png "$work/small.png" 100 100

# mktheme <dir> <name> [version] [shot-file|none]: a theme source tree.
mktheme() {
  local d=$1 n=$2 v=${3:-1.0.0} s=${4:-$work/shot.png}
  mkdir -p "$d/layouts" "$d/assets"
  if [ "$s" = none ]; then jq -n --arg n "$n" --arg v "$v" '{name:$n,version:$v}' >"$d/theme.json"
  else
    jq -n --arg n "$n" --arg v "$v" '{name:$n,version:$v,screenshot:"assets/shot.png"}' >"$d/theme.json"
    cp "$s" "$d/assets/shot.png"
  fi
  printf '<html>{{ template "content" . }}</html>' >"$d/layouts/base.html"
}
gitinit() { git -C "$1" init -q; git -C "$1" add -A; git -C "$1" -c user.name=t -c user.email=t@example.com commit -q -m init; git -C "$1" rev-parse HEAD; }

good=0123456789abcdef0123456789abcdef01234567
goodsha=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
https=https://example.com/acme/theme
manifest() { mkdir -p "$1/manifests/themes"; printf '%s' "$3" >"$1/manifests/themes/$2.json"; }
base() { # base <name> <repo> <commit> [subdir]
  jq -n --arg n "$1" --arg r "$2" --arg c "$3" --arg s "${4:-}" \
    '{name:$n,repo:$r,commit:$c,author:"acme",license:"MIT",archive_grant:true} + (if $s != "" then {subdir:$s} else {} end)'
}
abase() { # abase <name> <archive-url> <sha256>
  jq -n --arg n "$1" --arg a "$2" --arg h "$3" '{name:$n,archive:$a,sha256:$h,author:"acme",license:"MIT",archive_grant:true}'
}

# run <repo-dir> [extra env...]: all output dirs live under $work/o and start empty for negatives.
run() { local d=$1; shift; env "$@" "$script" "$d" "$bin" "${KEY:-$key}" "$work/o/pk" "$work/o/meta.json" "$work/o/shots" "${ARCH:-$work/o/arch}" 2>&1; }
untouched() { [ ! -e "$work/o" ] || fail "$1: wrote output: $(find "$work/o" | head)"; }
refuses() { # refuses <label> <substring> <repo-dir> [env...]
  local out l=$1 s=$2 d=$3; shift 3
  rm -rf "$work/o"
  if out=$(run "$d" "$@"); then fail "$l: accepted"; fi
  grep -q -- "$s" <<<"$out" || fail "$l: wrong error: $out"
  untouched "$l"
}
n=0
neg() { # neg <label> <file-name> <json> <substring> [env...]
  n=$((n + 1)); local d="$work/neg$n" l=$1 f=$2 j=$3 s=$4; shift 4
  manifest "$d" "$f" "$j"
  refuses "$l" "$s" "$d" "$@"
}

neg "traversal name" x "$(base ../x $https $good)" "bad name"
neg "uppercase name" Acme "$(base Acme $https $good)" "bad name"
neg "name vs file" other "$(base acme-seo $https $good)" "must equal the file name"
neg "default name" default "$(base default $https $good)" "reserved"
neg "reserved hello" hello "$(base hello $https $good)" "reserved"
neg "reserved gtm" gtm "$(base gtm $https $good)" "reserved"
neg "reserved searchconsole" searchconsole "$(base searchconsole $https $good)" "reserved"
neg "unknown field" acme-seo "$(base acme-seo $https $good | jq '.extra=1')" "unknown field"
neg "tier key" acme-seo "$(base acme-seo $https $good | jq '.tier="first-party"')" "unknown field"
neg "missing grant" acme-seo "$(base acme-seo $https $good | jq 'del(.archive_grant)')" "archive_grant must be true"
neg "string grant" acme-seo "$(base acme-seo $https $good | jq '.archive_grant="true"')" "archive_grant must be true"
neg "bad license" acme-seo "$(base acme-seo $https $good | jq '.license="WTFPL"')" "license not allowed"
neg "bad author" acme-seo "$(base acme-seo $https $good | jq '.author="-x"')" "bad author"
neg "empty title" acme-seo "$(base acme-seo $https $good | jq '.title=""')" "bad title"
neg "padded title" acme-seo "$(base acme-seo $https $good | jq '.title="a "')" "bad title"
neg "control title" acme-seo "$(base acme-seo $https $good | jq '.title="a\u0007b"')" "bad title"
# about and features (optional): one negative per rule (same rules and wording as build-community.sh).
an0=$n
longabout=$(printf 'x%.0s' $(seq 1501))
elevenp=$(printf 'p\n%.0s' $(seq 11))
f9=$(jq -nc '[range(9) | "item \(.)"]')
f121=$(jq -nc --arg s "$(printf 'x%.0s' $(seq 121))" '[$s]')
aneg() { # aneg <label> <jq-filter> <substring>
  neg "$1" acme-seo "$(base acme-seo $https $good | jq --arg la "$longabout" --arg ep "$elevenp" --argjson f9 "$f9" --argjson f121 "$f121" "$2")" "$3"
}
aneg "about too long" '.about=$la' "about must be 1 to 1500"
aneg "about empty" '.about=""' "about must be 1 to 1500"
aneg "about not a string" '.about=5' "about must be a string"
aneg "about control char" '.about="a\u0007b"' "about must not contain control"
aneg "about tab" '.about="a\tb"' "about must not contain control"
aneg "about carriage return" '.about="a\r\nb"' "about must not contain control"
aneg "about url" '.about="see https://example.test/x"' "about must not contain a URL"
aneg "about www" '.about="see www.example.test"' "about must not contain a URL"
aneg "about angle bracket" '.about="a <b> c"' "about must not contain < or >"
aneg "about empty paragraph" '.about="one\n\ntwo"' "about must not have an empty paragraph"
aneg "about edge space" '.about=" one"' "about must not start or end with whitespace"
aneg "about eleven paragraphs" '.about=$ep' "about must have at most 10 paragraphs"
aneg "about emoji" '.about="Great \ud83d\ude80 theme"' "about must not contain emoji"
aneg "features not a list" '.features="a"' "features must be a list of strings"
aneg "features non-string item" '.features=["a",5]' "features must be a list of strings"
aneg "features empty list" '.features=[]' "features must list 1 to 8"
aneg "features nine items" '.features=$f9' "features must list 1 to 8"
aneg "features long item" '.features=$f121' "each feature must be 1 to 120"
aneg "features empty item" '.features=["a",""]' "each feature must be 1 to 120"
aneg "features newline" '.features=["a\nb"]' "a feature must not contain control"
aneg "features url" '.features=["see https://example.test"]' "a feature must not contain a URL"
aneg "features angle bracket" '.features=["<b>"]' "a feature must not contain < or >"
aneg "features edge space" '.features=["a "]' "a feature must not start or end with whitespace"
aneg "features duplicate" '.features=["Fast","fast"]' "features must not repeat an item"
aneg "features emoji" '.features=["Fast \ud83d\ude80"]' "a feature must not contain emoji"
neg "still unknown field" acme-seo "$(base acme-seo $https $good | jq '.about="ok" | .abstract="x"')" "unknown field"
echo "ok: $((n - an0)) about/features negative cases"
neg "http repo" acme-seo "$(base acme-seo http://example.com/a/b $good)" "repo must be"
neg "ip repo" acme-seo "$(base acme-seo https://10.0.0.1/a/b $good)" "repo must be"
neg "localhost repo" acme-seo "$(base acme-seo https://localhost/a/b $good)" "repo must be"
neg "port repo" acme-seo "$(base acme-seo https://example.com:8443/a/b $good)" "repo must be"
neg "credentials repo" acme-seo "$(base acme-seo https://u@example.com/a/b $good)" "repo must be"
neg "internal host repo" acme-seo "$(base acme-seo https://git.corp/a/b $good)" "repo must be"
neg "file repo without opt-in" acme-seo "$(base acme-seo file:///tmp/x $good)" "repo must be"
neg "short commit" acme-seo "$(base acme-seo $https abc123)" "40 lowercase hex"
neg "subdir dotdot" acme-seo "$(base acme-seo $https $good ../etc)" "bad subdir"
neg "subdir absolute" acme-seo "$(base acme-seo $https $good /etc)" "bad subdir"
neg "no source" acme-seo "$(base acme-seo $https $good | jq 'del(.repo,.commit)')" "missing source"
neg "both sources" acme-seo "$(base acme-seo $https $good | jq --arg h $goodsha '.archive="https://example.com/a.zip"|.sha256=$h')" "not both"
neg "http archive" acme-seo "$(abase acme-seo http://example.com/a.zip $goodsha)" "archive must be"
neg "ip archive" acme-seo "$(abase acme-seo https://192.168.1.1/a.zip $goodsha)" "archive must be"
neg "file archive without opt-in" acme-seo "$(abase acme-seo file:///tmp/a.zip $goodsha)" "archive must be"
neg "bad sha" acme-seo "$(abase acme-seo https://example.com/a.zip ABC)" "sha256 must be"
neg "archive with subdir" acme-seo "$(abase acme-seo https://example.com/a.zip $goodsha | jq '.subdir="x"')" "subdir is not allowed"
neg "skip name not found" acme-seo "$(base acme-seo $https $good)" "no theme manifest named" HATAMOTO_THEMES_SKIP=nope
echo "ok: $n negative cases"

# A plugin manifest of the same name collides (either direction).
d="$work/coll"; manifest "$d" acme-seo "$(base acme-seo $https $good)"
mkdir -p "$d/manifests"; printf '{}' >"$d/manifests/acme-seo.json"
refuses "plugin manifest collision" "already used by a plugin" "$d"
d="$work/coll2"; manifest "$d" acme-seo "$(base acme-seo $https $good)"
mkdir -p "$work/o/pk"; touch "$work/o/pk/acme-seo.hmp"
if out=$(run "$d"); then fail "packages collision accepted"; fi
grep -q "name collision" <<<"$out" || fail "packages collision: $out"
rm -rf "$work/o"; mkdir -p "$work/o"; printf '{"acme-seo":{"tier":"first-party"}}' >"$work/o/meta.json"
if out=$(run "$d"); then fail "meta collision accepted"; fi
grep -q "name collision" <<<"$out" || fail "meta collision: $out"
[ "$(cat "$work/o/meta.json")" = '{"acme-seo":{"tier":"first-party"}}' ] || fail "meta collision changed meta"
rm -rf "$work/o"
echo "ok: collisions"

# No themes dir: success, nothing written.
d="$work/nothemes"; mkdir -p "$d/manifests"
out=$(run "$d") || fail "no themes dir failed: $out"; untouched "no themes dir"

# --- Positives: git, git+subdir (with title), archive. ---
src1="$work/src1"; mktheme "$src1" aurora 1.0.0; sha1=$(gitinit "$src1")
src2="$work/src2"; mkdir -p "$src2/sub/deep"; mktheme "$src2/sub/deep" boreal 2.1.0; sha2=$(gitinit "$src2")
mktheme "$work/arcsrc" cirrus 0.3.0
"$fx" zip "$work/cirrus.zip" "theme.json=$work/arcsrc/theme.json" "layouts/base.html=$work/arcsrc/layouts/base.html" "assets/shot.png=$work/shot.png"
arcsha=$(sha256sum "$work/cirrus.zip" | cut -d' ' -f1)
reg="$work/reg"
manifest "$reg" aurora "$(base aurora "file://$src1" "$sha1")"
manifest "$reg" boreal "$(base boreal "file://$src2" "$sha2" sub/deep | jq '.title="Boreal Lights" | .about="Calm colours.\nDark mode too." | .features=["Dark mode","Wide layout"]')"
manifest "$reg" cirrus "$(abase cirrus "file://$work/cirrus.zip" "$arcsha")"
printf '# readme\n' >"$reg/manifests/themes/README.md"
printf '{"existing":{"tier":"first-party"}}' >"$work/meta.in"
rm -rf "$work/o"; mkdir -p "$work/o"; cp "$work/meta.in" "$work/o/meta.json"
out=$(run "$reg" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1) || fail "build: $out"
grep -q "built 3 theme(s)" <<<"$out" || fail "build output: $out"
for t in aurora boreal cirrus; do
  [ -s "$work/o/pk/$t.hmp" ] || fail "no package $t"
  [ -s "$work/o/shots/$t.png" ] || fail "no screenshot $t"
  cmp -s "$work/o/shots/$t.png" "$work/shot.png" || fail "screenshot $t differs"
  "$bin" theme check --package "$work/o/pk/$t.hmp" --registry --expect-name "$t" >/dev/null || fail "$t package does not check"
done
kid=$(jq -r .key_id "$key")
jq -e --arg c "$sha1" '.existing.tier == "first-party" and .aurora == {author:"acme",source:"file:///'"${src1#/}"'",commit:$c,tier:"community",screenshot:"screenshots/aurora.png"}' "$work/o/meta.json" >/dev/null || fail "aurora meta: $(cat "$work/o/meta.json")"
jq -e '.boreal.title == "Boreal Lights" and .boreal.about == "Calm colours.\nDark mode too." and .boreal.features == ["Dark mode","Wide layout"] and .boreal.screenshot == "screenshots/boreal.png" and .boreal.commit == "'"$sha2"'"' "$work/o/meta.json" >/dev/null || fail "boreal meta"
jq -e --arg u "file://$work/cirrus.zip" '.cirrus == {author:"acme",source:$u,tier:"community",screenshot:"screenshots/cirrus.png"}' "$work/o/meta.json" >/dev/null || fail "cirrus meta (archive: no commit)"
# Archive layout and sidecars.
af="aurora-1.0.0-${sha1:0:12}-$kid"
[ -s "$work/o/arch/aurora/$af.hmp" ] || fail "no archived hmp: $(find "$work/o/arch")"
cmp "$work/o/arch/aurora/$af.hmp" "$work/o/pk/aurora.hmp" || fail "archived hmp differs"
jq -e --arg c "$sha1" --arg k "$kid" --arg h "$(sha256sum "$work/o/pk/aurora.hmp" | cut -d' ' -f1)" \
  '. == {name:"aurora",kind:"theme",version:"1.0.0",commit:$c,id12:($c[:12]),key_id:$k,sha256:$h}' "$work/o/arch/aurora/$af.json" >/dev/null || fail "aurora sidecar: $(cat "$work/o/arch/aurora/$af.json")"
acf="cirrus-0.3.0-${arcsha:0:12}-$kid"
jq -e --arg c "$arcsha" '.archive_sha256 == $c and (has("commit") | not) and .id12 == ($c[:12])' "$work/o/arch/cirrus/$acf.json" >/dev/null || fail "cirrus sidecar"
[ -s "$work/o/arch/boreal/boreal-2.1.0-${sha2:0:12}-$kid.hmp" ] || fail "boreal archive"
echo "ok: git, subdir, archive builds"
cp -r "$work/o" "$work/first"

# --- Reuse: the sources are gone; nothing may be fetched or built. ---
mv "$src1" "$src1.gone"; mv "$src2" "$src2.gone"; mv "$work/cirrus.zip" "$work/cirrus.zip.gone"
rm -rf "$work/o/pk" "$work/o/shots" "$work/o/meta.json"
# The pinned sources are unreachable, so reuse needs the offline override (checked in its own section below).
out=$(run "$reg" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1 HATAMOTO_ARCHIVE_OFFLINE=1) || fail "reuse: $out"
[ "$(grep -c "reusing archived" <<<"$out")" = 3 ] || fail "reuse not logged for all: $out"
for t in aurora boreal cirrus; do
  cmp "$work/o/pk/$t.hmp" "$work/first/pk/$t.hmp" || fail "reuse changed $t"
  cmp "$work/o/shots/$t.png" "$work/shot.png" || fail "reuse screenshot $t"
done
jq -e '.aurora.screenshot and .boreal.title and .cirrus.screenshot' "$work/o/meta.json" >/dev/null || fail "reuse meta"
# Reuse refuses when the pin changed in the archive (sidecar full pin differs from the manifest): rebuild is needed, source gone => fetch fails.
manifest "$work/reg2" aurora "$(base aurora "file://$src1" "${sha1:0:12}0000000000000000000000000000")"
refuses "same id12, different pin" "cannot fetch" "$work/reg2" HATAMOTO_ALLOW_FILE_REPOS=1
# A different signing key does not reuse (no source => it must try to build).
rm -rf "$work/o"; mkdir -p "$work/o"; cp -r "$work/first/arch" "$work/o/arch"
if out=$(KEY=$key2 run "$reg" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1); then fail "other key reused or built without a source"; fi
grep -q "cannot" <<<"$out" || fail "other key: $out"
echo "ok: reuse without fetch"

# Tampered archived .hmp refused.
tamper() { # tamper <label> <substring> <mutator on $work/o/arch>
  local l=$1 s=$2; shift 2
  rm -rf "$work/o"; mkdir -p "$work/o"; cp -r "$work/first/arch" "$work/o/arch"
  "$@"
  if out=$(run "$reg" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1 HATAMOTO_ARCHIVE_OFFLINE=1); then fail "$l: accepted"; fi
  grep -q -- "$s" <<<"$out" || fail "$l: wrong error: $out"
  [ ! -e "$work/o/pk" ] && [ ! -e "$work/o/meta.json" ] && [ ! -e "$work/o/shots" ] || fail "$l: published something"
}
a=$work/o/arch/aurora
tamper "tampered hmp" "does not match its sidecar sha256" bash -c "printf x >> '$a/$af.hmp'"
# Sidecar version mismatch: sha is right, but the file name / inner version disagree.
side_version() { jq '.version="9.9.9"' "$a/$af.json" >"$a/$af.json.n" && mv "$a/$af.json.n" "$a/$af.json"; }
tamper "sidecar version" "different name or version" side_version
# A package of another theme under this sidecar.
swap_pkg() { cp "$work/first/arch/boreal/boreal-2.1.0-${sha2:0:12}-$kid.hmp" "$a/$af.hmp"; jq --arg h "$(sha256sum "$a/$af.hmp" | cut -d' ' -f1)" '.sha256=$h' "$a/$af.json" >"$a/$af.json.n" && mv "$a/$af.json.n" "$a/$af.json"; }
tamper "swapped package" "is refused" swap_pkg
echo "ok: tampered archive refused"

# Append-only: a fresh build whose archived name already exists with other content.
rm -rf "$work/o"; mkdir -p "$work/o"; cp -r "$work/first/arch" "$work/o/arch"
mv "$src1.gone" "$src1"
manifest "$work/reg3" aurora "$(base aurora "file://$src1" "$sha1")"
# a different key id cannot collide, so corrupt by emptying the sidecar of the same pin and key.
printf '{"name":"aurora","kind":"theme","commit":"%s","key_id":"%s"}' "$sha1" "$kid" >"$work/o/arch/aurora/$af.json"
# the broken sidecar matches the pin, so reuse is attempted and refused loudly (sha missing).
if out=$(run "$work/reg3" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1); then fail "broken sidecar accepted"; fi
grep -q "sidecar sha256" <<<"$out" || fail "broken sidecar: $out"
# A build under a rewritten archive file (different bytes, same name, no matching sidecar pin) is refused.
rm -rf "$work/o"; mkdir -p "$work/o/arch/aurora"
printf junk >"$work/o/arch/aurora/$af.hmp"
if out=$(run "$work/reg3" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1); then fail "append-only violated"; fi
grep -q "append-only" <<<"$out" || fail "append-only: $out"
[ ! -e "$work/o/pk" ] && [ "$(cat "$work/o/arch/aurora/$af.hmp")" = junk ] || fail "append-only: wrote"
echo "ok: append-only"

# --- Reuse is authenticated against the pinned source (sources reachable again). ---
mv "$src2.gone" "$src2"; mv "$work/cirrus.zip.gone" "$work/cirrus.zip"
rm -rf "$work/o"; mkdir -p "$work/o"; cp -r "$work/first/arch" "$work/o/arch"
out=$(run "$reg" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1) || fail "authenticated reuse: $out"
[ "$(grep -c "reusing archived" <<<"$out")" = 3 ] || fail "authenticated reuse not logged for all: $out"
! grep -q "NOT checked" <<<"$out" || fail "reachable source warned unchecked: $out"
# Rollback: the genuinely signed 1.0.0 builds are renamed under a new pin whose source says 2.0.0.
cp -r "$src1" "$work/src1b"; jq '.version="2.0.0"' "$work/src1b/theme.json" >"$work/tj" && mv "$work/tj" "$work/src1b/theme.json"
sha1b=$(git -C "$work/src1b" -c user.name=t -c user.email=t@example.com commit -qam v2 && git -C "$work/src1b" rev-parse HEAD)
mktheme "$work/arcsrc2" cirrus 0.4.0
"$fx" zip "$work/cirrus2.zip" "theme.json=$work/arcsrc2/theme.json" "layouts/base.html=$work/arcsrc2/layouts/base.html" "assets/shot.png=$work/shot.png"
arcsha2=$(sha256sum "$work/cirrus2.zip" | cut -d' ' -f1)
manifest "$work/rb" aurora "$(base aurora "file://$work/src1b" "$sha1b")"
manifest "$work/rb" cirrus "$(abase cirrus "file://$work/cirrus2.zip" "$arcsha2")"
rollback_arch() { # rename the archived old builds to the new pins
  rm -rf "$work/o"; mkdir -p "$work/o/arch/aurora" "$work/o/arch/cirrus"
  local o=$work/first/arch d=$work/o/arch
  cp "$o/aurora/$af.hmp" "$d/aurora/aurora-1.0.0-${sha1b:0:12}-$kid.hmp"
  jq --arg c "$sha1b" '.commit=$c|.id12=$c[:12]' "$o/aurora/$af.json" >"$d/aurora/aurora-1.0.0-${sha1b:0:12}-$kid.json"
  cp "$o/cirrus/$acf.hmp" "$d/cirrus/cirrus-0.3.0-${arcsha2:0:12}-$kid.hmp"
  jq --arg c "$arcsha2" '.archive_sha256=$c|.id12=$c[:12]' "$o/cirrus/$acf.json" >"$d/cirrus/cirrus-0.3.0-${arcsha2:0:12}-$kid.json"
}
rb_run() { run "$work/rb" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1 "$@"; }
rm -f "$work/rb/manifests/themes/cirrus.json"
rollback_arch
if out=$(rb_run); then fail "git rollback accepted"; fi
grep -q "rolled back" <<<"$out" || fail "git rollback: $out"
[ ! -e "$work/o/pk" ] || fail "git rollback published"
manifest "$work/rb" cirrus "$(abase cirrus "file://$work/cirrus2.zip" "$arcsha2")"
rm -f "$work/rb/manifests/themes/aurora.json"
rollback_arch
if out=$(rb_run); then fail "archive rollback accepted"; fi
grep -q "rolled back" <<<"$out" || fail "archive rollback: $out"
# Unreachable pinned source: refused, unless the offline override (loud warning).
mv "$work/src1b" "$work/src1b.gone"; mv "$work/cirrus2.zip" "$work/cirrus2.zip.gone"
manifest "$work/rb" aurora "$(base aurora "file://$work/src1b" "$sha1b")"
rollback_arch
if out=$(rb_run); then fail "unreachable source accepted"; fi
grep -q "cannot reach" <<<"$out" || fail "unreachable source: $out"
rollback_arch
out=$(rb_run HATAMOTO_ARCHIVE_OFFLINE=1) || fail "offline override: $out"
[ "$(grep -c "NOT checked" <<<"$out")" = 2 ] || fail "offline override not loud: $out"
[ -s "$work/o/pk/aurora.hmp" ] && [ -s "$work/o/pk/cirrus.hmp" ] || fail "offline override published nothing"
echo "ok: reuse authenticated against the pinned source"

# --- Skip list: skipped manifests are left out, loudly. ---
rm -rf "$work/o"
out=$(run "$reg" HATAMOTO_ALLOW_FILE_REPOS=1 HATAMOTO_ALLOW_FILE_ARCHIVES=1 "HATAMOTO_THEMES_SKIP=boreal cirrus") || fail "skip: $out"
grep -q "SKIPPING boreal" <<<"$out" || fail "skip not loud"
[ -s "$work/o/pk/aurora.hmp" ] && [ ! -e "$work/o/pk/boreal.hmp" ] && jq -e 'has("aurora") and (has("boreal")|not)' "$work/o/meta.json" >/dev/null || fail "skip result"
echo "ok: skip list"

# --- Refusals from the CLI (each leaves everything untouched). ---
srcn="$work/srcn"; mktheme "$srcn" nosnap 1.0.0 none; shn=$(gitinit "$srcn")
manifest "$work/r1" nosnap "$(base nosnap "file://$srcn" "$shn")"
refuses "missing screenshot" "screenshot" "$work/r1" HATAMOTO_ALLOW_FILE_REPOS=1
srcs="$work/srcs"; mktheme "$srcs" smallshot 1.0.0 "$work/small.png"; shs=$(gitinit "$srcs")
manifest "$work/r2" smallshot "$(base smallshot "file://$srcs" "$shs")"
refuses "small screenshot" "screenshot" "$work/r2" HATAMOTO_ALLOW_FILE_REPOS=1
srcm="$work/srcm"; mktheme "$srcm" other-name 1.0.0; shm=$(gitinit "$srcm")
manifest "$work/r3" wanted "$(base wanted "file://$srcm" "$shm")"
refuses "theme.json name differs" "expected" "$work/r3" HATAMOTO_ALLOW_FILE_REPOS=1
manifest "$work/r4" cirrus "$(abase cirrus "file://$work/cirrus.zip" "$goodsha")"
refuses "wrong archive sha" "sha256" "$work/r4" HATAMOTO_ALLOW_FILE_ARCHIVES=1
manifest "$work/r4b" cirrus "$(abase cirrus "file://$work/nonexistent.zip" "$goodsha")"
refuses "missing archive file" "cannot download" "$work/r4b" HATAMOTO_ALLOW_FILE_ARCHIVES=1
mktheme "$work/evil" evil 1.0.0
"$fx" zip "$work/evil.zip" "theme.json=$work/evil/theme.json" "layouts/base.html=$work/evil/layouts/base.html" "assets/shot.png=$work/shot.png" "../escape.txt=$work/shot.png"
manifest "$work/r5" evil "$(abase evil "file://$work/evil.zip" "$(sha256sum "$work/evil.zip" | cut -d' ' -f1)")"
refuses "../ zip entry" "pack failed" "$work/r5" HATAMOTO_ALLOW_FILE_ARCHIVES=1
# A wasm file in a theme.
srcw="$work/srcw"; mktheme "$srcw" wasmy 1.0.0; printf '\0asm' >"$srcw/plugin.wasm"; shw=$(gitinit "$srcw")
manifest "$work/r6" wasmy "$(base wasmy "file://$srcw" "$shw")"
refuses "wasm in a theme" "pack failed" "$work/r6" HATAMOTO_ALLOW_FILE_REPOS=1
# Symlink in the source and a subdir that is a symlink.
srcl="$work/srcl"; mktheme "$srcl" linky 1.0.0; ln -s /etc/passwd "$srcl/assets/link"; shl=$(gitinit "$srcl")
manifest "$work/r7" linky "$(base linky "file://$srcl" "$shl")"
refuses "symlink in source" "symlink" "$work/r7" HATAMOTO_ALLOW_FILE_REPOS=1
srcd="$work/srcd"; mkdir -p "$srcd/real"; mktheme "$srcd/real" dirlink 1.0.0; ln -s real "$srcd/alias"; shd=$(gitinit "$srcd")
manifest "$work/r8" dirlink "$(base dirlink "file://$srcd" "$shd" alias)"
refuses "symlinked subdir" "symlink" "$work/r8" HATAMOTO_ALLOW_FILE_REPOS=1
manifest "$work/r9" nosubdir "$(base nosubdir "file://$srcd" "$shd" nope)"
refuses "missing subdir" "subdir not found" "$work/r9" HATAMOTO_ALLOW_FILE_REPOS=1
manifest "$work/r10" gone "$(base gone "file:///nonexistent/repo" "$good")"
refuses "unfetchable commit" "cannot fetch" "$work/r10" HATAMOTO_ALLOW_FILE_REPOS=1
# One bad manifest among good ones publishes nothing.
mkdir -p "$work/mix/manifests/themes"
cp "$reg/manifests/themes/aurora.json" "$work/mix/manifests/themes/"
cp "$work/r1/manifests/themes/nosnap.json" "$work/mix/manifests/themes/"
refuses "mixed run" "screenshot" "$work/mix" HATAMOTO_ALLOW_FILE_REPOS=1
echo "ok: CLI refusals"

# The usage line.
if "$script" 2>"$work/usage"; then fail "no args accepted"; fi
grep -q "usage: .*<screenshots-dir> <archive-dir>" "$work/usage" || fail "usage"
echo "ALL OK"
