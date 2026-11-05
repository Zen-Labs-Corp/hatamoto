#!/usr/bin/env bash
# test-check-jsonv1.sh - offline checks of scripts/check-jsonv1.sh (suite t-jsonv1).
set -euo pipefail
ROOT=$(git rev-parse --show-toplevel)
fail() { echo "FAIL: $*" >&2; exit 1; }
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
R="$T/repo"
mkdir -p "$R/scripts" "$R/backend/app" "$R/backend/migrations" "$R/backend/internal/jsonx"
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

# migrations and the single allow-listed file are ignored
plant backend/migrations/old.go '"encoding/json"'
plant backend/internal/jsonx/v1opts.go '"encoding/json"'
commit ignored
o=$(JSONV1_STRICT=1 chk) || fail "exempt paths counted: $o"
echo "ok   exempt: backend/migrations/ and v1opts.go"

# warn-only: counts, exit 0, aliased import counted, tests counted separately
plant backend/app/a.go '"encoding/json"'
plant backend/app/b.go 'stdjson "encoding/json"'
printf 'package x\n\nimport "encoding/json"\n' > "$R/backend/app/c_test.go"
plant backend/internal/jsonx/other.go '"encoding/json"'
commit hits
o=$(chk) || fail "warn-only mode exited non-zero: $o"
grep -q 'WARN 3 non-test files and 1 test files' <<<"$o" || fail "wrong counts: $o"
grep -q 'warn-only' <<<"$o" || fail "no warn-only note: $o"
if grep -q 'app/a.go' <<<"$o"; then fail "file list printed without JSONV1_VERBOSE"; fi
o=$(JSONV1_VERBOSE=1 chk) || fail "verbose failed"
grep -q '^backend/app/b.go:4$' <<<"$o" || fail "verbose did not name backend/app/b.go:4: $o"
if grep -qE '^backend/(migrations|internal/jsonx/v1opts)' <<<"$o"; then fail "exempt file listed: $o"; fi
echo "ok   warn-only: counts, alias, test split, verbose list"

# strict: fails
if o=$(JSONV1_STRICT=1 chk); then fail "strict mode passed with hits"; fi
grep -q 'FAIL' <<<"$o" || fail "strict mode printed no FAIL: $o"
echo "ok   strict: exit 1"
echo "check-jsonv1 tests ok"
