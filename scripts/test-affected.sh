#!/usr/bin/env bash
# test-affected.sh - offline test of .githooks/lib/affected.sh (affected-package set for
# pre-push) and the R1 guard: every file a Go test reads by relative path is covered by a
# force-full rule, an embed, or a row of scripts/affected-readers.txt. Run by `make check`.
set -euo pipefail
top=$(git rev-parse --show-toplevel)
fail() { echo "FAIL: $*" >&2; exit 1; }
# shellcheck source=../.githooks/lib/affected.sh
source "$top/.githooks/lib/affected.sh"
export GO_QUEUE_FORCE_LOCAL=1

T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
g() { git -C "$T" -c commit.gpgsign=false -c core.hooksPath=/dev/null -c user.name=t -c user.email=t@example.com "$@"; }
mkdir -p "$T/backend" "$T/scripts" "$T/docs"
cd "$T"
g init -q -b main
printf 'module example.test/backend\n\ngo 1.21\n' >backend/go.mod
pkg() { mkdir -p "backend/$1"; printf 'package %s\n%s\n' "$1" "${2:-}" >"backend/$1/$1.go"; }
pkg b
pkg a 'import _ "embed"
import _ "example.test/backend/b"
//go:embed tpl.txt
var tpl string'
echo tpl >backend/a/tpl.txt
pkg c
printf 'package c\nimport (\n"testing"\n_ "example.test/backend/b"\n)\nfunc TestX(t *testing.T) {}\n' >backend/c/c_test.go
for p in d e f g; do pkg $p; done
mkdir -p backend/h; printf 'package h\nimport "testing"\nfunc TestH(t *testing.T) {}\n' >backend/h/h_test.go
printf '# rows\ndocs/read.md backend ./d\ndocs/tree/** backend ./e\n' >scripts/affected-readers.txt
echo old >docs/read.md
g add -A; g commit -q -m base

# change <path> [<content>]: commit one change and run hm_affected on it.
change() { mkdir -p "$(dirname "$1")"; echo "${2:-// x$RANDOM}" >>"$1"; g add -A; g commit -q -m "change $1"; hm_affected "$T" HEAD~1..HEAD; }
pkgs() { hm_affected_pkgs backend | tr '\n' ' '; }
want() { # <label> <mode> [pkgs]
  local got
  [[ $HM_MODE == "$2" ]] || fail "$1: mode $HM_MODE ($HM_REASON), want $2"
  [[ $2 == full ]] && return 0
  got=$(pkgs); got=${got% }
  [[ $got == "${3% }" ]] || fail "$1: packages '$got', want '${3% }'"
}

# reverse deps: b is imported by a (code) and c (test code); d..h are not
change backend/b/b.go;               want "reverse deps" affected "./a ./b ./c "
[[ $(awk -F'\t' '{print $2}' <<<"$HM_SEEDS") == ./b ]] || fail "seeds: $HM_SEEDS"
change backend/a/a.go;               want "leaf" affected "./a "
change backend/h/h_test.go;          want "test-only package" affected "./h "
change backend/a/tpl.txt;            want "embedded file" affected "./a "
change docs/read.md;                 want "reader row" affected "./d "
change docs/tree/deep/x.md;          want "reader row, nested" affected "./e "
change docs/other.md;                want "docs nobody reads" affected ""
change scripts/notes.txt;            want "script nobody reads" affected ""
change backend/b/b.go; change backend/c/c_test.go; want "two changes, range" affected "./c "
hm_affected "$T" HEAD~2..HEAD;       want "range of two commits" affected "./a ./b ./c "

# unmappable: a non-Go file under backend/ that nothing embeds or lists
change backend/a/unknown.txt;        want "unmapped file" full
grep -q 'no test mapping for' <<<"$HM_REASON" || fail "unmapped reason: $HM_REASON"
change backend/testdata/x.json;      want "testdata not in the table" full

# every force-full trigger (reset afterwards: some of these files break the scratch module)
base=$(g rev-parse HEAD)
for f in backend/go.mod backend/sub/go.sum go.work backend/migrations/001.sql backend/pkg/sdk/x.go \
         examples/plugins/p/main.go .githooks/pre-push Makefile .gitleaks.toml .github/workflows/ci.yml \
         deploy/sql/r.sql scripts/affected-readers.txt scripts/check-suites.txt scripts/go-test-flaky.sh \
         scripts/integration-gate.sh scripts/pipeline-gate.sh scripts/ship.sh scripts/test-hooks.sh \
         backend/d/d_linux.go backend/d/d_windows_amd64_test.go; do
  change "$f";                       want "force-full $f" full
  [[ -n $HM_REASON ]] || fail "force-full $f: no reason"
done
g reset -q --hard "$base"
printf '//go:build integration\n\npackage d\n' >backend/d/tagged.go; g add -A; g commit -q -m tag
change backend/d/tagged.go "// more"; want "tagged file, body only" affected "./d "
git -C "$T" rm -q backend/d/tagged.go; g commit -q -m untag; hm_affected "$T" HEAD~1..HEAD
want "removed tagged file (constraint line gone)" full
grep -q 'build constraint' <<<"$HM_REASON" || fail "build-tag reason: $HM_REASON"
change backend/d/d.go "//go:build linux"; want "added //go:build line" full
git -C "$T" rm -q backend/g/g.go; g commit -q -m rmpkg; hm_affected "$T" HEAD~1..HEAD
want "removed package" full
pkg g; g add -A; g commit -q -m restore; hm_affected "$T" HEAD~1..HEAD

# threshold: 3 of 8 packages is affected-only at 60%, full at 30%
change backend/b/b.go;               want "under the threshold" affected "./a ./b ./c "
HATAMOTO_AFFECTED_MAX_PCT=30 hm_affected "$T" HEAD~1..HEAD; want "over the threshold" full
grep -q 'over 30%' <<<"$HM_REASON" || fail "threshold reason: $HM_REASON"

# the cache context: a full run is never an affected run, and the list is part of it
change backend/b/b.go;               a1=$(hm_affected_ctx backend)
change backend/a/a.go;               a2=$(hm_affected_ctx backend)
change backend/go.mod;               f1=$(hm_affected_ctx backend)
[[ $a1 != "$a2" ]] || fail "different package lists share a ctx: $a1"
[[ $f1 == mode:full && $a1 != "$f1" && $a2 != "$f1" ]] || fail "a full ctx must differ from every affected ctx: $f1"

# go list failing never narrows
printf 'package a\nimport _ "example.test/nothere"\n' >backend/a/bad.go
change backend/b/b.go;               want "go list failure" full
grep -q 'go list failed' <<<"$HM_REASON" || fail "go list reason: $HM_REASON"
change backend/b/b.go;               want "go list failure persists" full
rm backend/a/bad.go; g add -A; g commit -q -m fix

# whole-tree guards: any Go change or any added file also runs internal/release (it scans every tracked file)
mkdir -p backend/internal/release; printf 'package release\n' >backend/internal/release/release.go; g add -A; g commit -q -m release
change backend/a/a.go;               want "Go change adds the release guard" affected "./a ./internal/release "
change docs/brand-new.md;            want "an added file adds the release guard" affected "./internal/release "
echo more >>docs/brand-new.md; g add -A; g commit -q -m edit; hm_affected "$T" HEAD~1..HEAD
want "an edited (not added) doc adds nothing" affected ""

# an unreadable range
hm_affected "$T" nosuchref..HEAD;    want "bad range" full

# --- R1 guard: every relative path a test reads is covered ---
cd "$top"
rows() { hm_readers_match scripts/affected-readers.txt "$1" | sort | tr '\t\n' ': '; }
[[ $(rows design/homepage/page_attributes.json) == 'backend:./internal/render ' ]] || fail "design/** must map to internal/render"
[[ $(rows backend/internal/wasmhost/testdata/hostile/x.go) == 'backend:./internal/pluginhost backend:./internal/wasmhost ' ]] || fail "wasmhost testdata must map to pluginhost and wasmhost"
grep -E '^t-publish-rel ' scripts/check-suites.txt | grep -q 'backend/tools/release/\*\*' || fail "t-publish-rel must run for a change under backend/tools/release"
uncovered=()
# Reads that are not a file dependency, with the reason (the ship-time full run still covers them):
#   backend, backend/internal, backend/tools  tests that build or `go list` the whole binary
#     (release link/seam guards) and URL-like literals ("../") that happen to name a directory.
#   .  the repo root: only page_sections_conversion_test walks it (checked by name below), over backend
#     (Go files map by package, any other file there forces a full run) and design (the design/** row).
#     A second root reader fails this guard until it is covered.
accepted=' backend backend/internal backend/tools '
root_reader=backend/internal/render/page_sections_conversion_test.go
covered() { # <repo-relative path to a file or dir> <the test file that reads it>
  local p=$1 probe
  [[ $p == . && $2 == "$root_reader" ]] && return 0
  [[ $accepted == *" $p "* ]] && return 0
  grep -qE "^$p/" scripts/affected-readers.txt && return 0   # a row for files beneath the root it reads
  for probe in "$p" "$p/x"; do
    hm_force_reason "$probe" >/dev/null && return 0
    [[ -n $(hm_readers_match scripts/affected-readers.txt "$probe") ]] && return 0
  done
  return 1
}
resolve() { # <test file> <literal>: repo-relative path, or nothing when it is not a real path
  local dir=$1 lit=$2 abs
  dir=$(dirname "$dir")
  lit=${lit%/}
  if [[ -d $dir/$lit ]]; then abs=$(cd "$dir/$lit" && pwd -P)
  elif [[ -f $dir/$lit ]]; then abs=$(cd "$(dirname "$dir/$lit")" && pwd -P)/$(basename "$lit")
  else return 0; fi
  if [[ $abs == "$top" ]]; then echo .
  elif [[ $abs == "$top"/* ]]; then printf '%s\n' "${abs#"$top"/}"; fi
  return 0
}
while IFS= read -r tf; do
  lits=$( { grep -oE '"(\.\./)+[^"]*"' "$tf" | tr -d '"' || true
            grep -E 'Join\(' "$tf" | grep -E '"\.\."' | grep -oE 'Join\(([^()]|\([^()]*\))*\)' |
              while IFS= read -r j; do
                echo "$j" | grep -oE '"[^"]*"' | tr -d '"' | grep -v '^$' | paste -sd/
              done | grep -E '^(\.\./)+' || true; } | sort -u)
  while IFS= read -r lit; do
    [[ -n $lit ]] || continue
    rp=$(resolve "$tf" "$lit")
    [[ -n $rp ]] || continue
    # a package reading its own tree or files it embeds is handled by the embed/go list rules
    covered "$rp" "$tf" || uncovered+=("$tf reads $rp")
  done <<<"$lits"
done < <(git ls-files -- 'backend/*_test.go' | sed "s|^|$top/|" | sed "s|^$top/||")
if ((${#uncovered[@]})); then
  printf 'uncovered test reader: %s\n' "${uncovered[@]}" | sort -u >&2
  fail "a test reads a path that neither a force-full rule nor scripts/affected-readers.txt covers"
fi
# the real table maps files below deploy/install to the installer-key test
for f in deploy/install/src/install/20-trust.sh deploy/install/build-install.sh; do
  [[ $(hm_readers_match "$top/scripts/affected-readers.txt" "$f") == $'backend\t./internal/release' ]] \
    || fail "affected-readers.txt has no ./internal/release row for $f"
done
echo "test-affected: ok"
