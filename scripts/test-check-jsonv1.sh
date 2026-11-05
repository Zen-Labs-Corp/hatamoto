#!/usr/bin/env bash
# test-check-jsonv1.sh - offline checks of scripts/check-jsonv1.sh (suite t-jsonv1).
set -euo pipefail
ROOT=$(git rev-parse --show-toplevel)
fail() { echo "FAIL: $*" >&2; exit 1; }
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
R="$T/repo"
mkdir -p "$R/scripts" "$R/backend/app" "$R/backend/migrations" "$R/backend/internal/jsonx" "$R/backend/pkg/sdk/guest" "$R/backend/internal/pluginhost/testdata/legacy"
git -C "$R" init -q
cp "$ROOT/scripts/check-jsonv1.sh" "$R/scripts/check-jsonv1.sh"
gr() { git -C "$R" -c user.name=t -c user.email=t@example.org "$@"; }
chk() { (cd "$R" && bash scripts/check-jsonv1.sh 2>&1); }
plant() { printf 'package x\n\nimport (\n\t%s\n)\n' "$2" > "$R/$1"; }
commit() { gr add -A && gr commit -qm "$1" --allow-empty; }

# clean tree: v2 and jsontext imports are not v1
plant backend/app/ok.go '"encoding/json/v2"'
plant backend/app/ok2.go '"encoding/json/jsontext"'
commit base
o=$(chk) || fail "clean tree failed: $o"
grep -q '^check-jsonv1: OK$' <<<"$o" || fail "clean tree not plain OK: $o"
echo "ok   clean: v2 and jsontext are not counted"

# migrations and the three allow-listed files are ignored
plant backend/migrations/old.go '"encoding/json"'
plant backend/internal/jsonx/v1opts.go '"encoding/json"'
plant backend/pkg/sdk/guest/v1opts.go '"encoding/json"'
plant backend/internal/pluginhost/testdata/legacy/main.go '"encoding/json"'
commit ignored
o=$(chk) || fail "exempt paths counted: $o"
grep -q '^check-jsonv1: OK$' <<<"$o" || fail "exempt tree not plain OK: $o"
echo "ok   exempt: backend/migrations/ and the three allow-listed files"

# default (no env): fails, names the file, catches alias, tests and near-miss paths
plant backend/app/a.go '"encoding/json"'
plant backend/app/b.go 'stdjson "encoding/json"'
printf 'package x\n\nimport "encoding/json"\n' > "$R/backend/app/c_test.go"
plant backend/internal/jsonx/other.go '"encoding/json"'
commit hits
if o=$(chk); then fail "default mode passed with hits: $o"; fi
for f in a.go b.go c_test.go; do
  grep -qxF "check-jsonv1: FAIL backend/app/$f imports encoding/json (v1); use internal/jsonx or encoding/json/v2" <<<"$o" || fail "no FAIL line for $f: $o"
done
grep -qF 'FAIL backend/internal/jsonx/other.go imports' <<<"$o" || fail "allow-list is not by exact path: $o"
if grep -qE 'FAIL backend/(migrations|internal/jsonx/v1opts|pkg/sdk/guest/v1opts|internal/pluginhost/testdata/legacy)' <<<"$o"; then fail "exempt path listed: $o"; fi
echo "ok   default: exit 1, names files, alias, _test.go, exact allow-list"
echo "check-jsonv1 tests ok"
