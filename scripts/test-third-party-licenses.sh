#!/usr/bin/env bash
# Tests scripts/third-party-licenses.sh: the Go toolchain's licence lands in
# OUTDIR/go/LICENSE, and a toolchain without it (or with another text) fails.
set -Eeuo pipefail
cd "$(dirname "$0")/.."
W=$(mktemp -d "${TMPDIR:-/tmp}/tpl.XXXXXX"); trap 'rm -rf "$W"' EXIT
fail() { echo "FAIL: $*"; exit 1; }

scripts/third-party-licenses.sh "$W/ok" >/dev/null || fail "script failed with the real toolchain"
cmp "$W/ok/go/LICENSE" "$(cd backend && go env GOROOT)/LICENSE" || fail "go/LICENSE is not the toolchain's"
for p in go-modules web-vendor/NOTICE fonts/OFL.txt; do [ -e "$W/ok/$p" ] || fail "missing $p"; done
echo "PASS: bundle tree has the Go licence, modules, web-vendor and fonts"

# A fake GOROOT stands in for a toolchain whose LICENSE is missing or wrong:
# a stub `go` on PATH answers `go env GOROOT` and nothing else is run.
mkdir -p "$W/bin" "$W/goroot"
printf '#!/bin/sh\nif [ "$1" = env ]; then echo "%s"; exit 0; fi\nexit 1\n' "$W/goroot" > "$W/bin/go"; chmod +x "$W/bin/go"
if out=$(PATH="$W/bin:$PATH" scripts/third-party-licenses.sh "$W/bad1" 2>&1); then fail "no Go LICENSE was accepted"; fi
grep -q 'no Go LICENSE' <<<"$out" || fail "wrong refusal: $out"
echo "PASS: missing Go LICENSE refused"
echo "some other licence" > "$W/goroot/LICENSE"
if out=$(PATH="$W/bin:$PATH" scripts/third-party-licenses.sh "$W/bad2" 2>&1); then fail "a foreign LICENSE was accepted"; fi
grep -q 'not the Go BSD-3' <<<"$out" || fail "wrong refusal: $out"
echo "PASS: foreign LICENSE refused"
echo "all third-party-licenses tests passed"
