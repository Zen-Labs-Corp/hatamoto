#!/usr/bin/env bash
# check-jsonv1.sh - counts Go files that import encoding/json (v1) under backend/.
# Run as suite jsonv1 (scripts/check-suites.txt) and by scripts/check-changed.sh.
#
# Warn-only by default: it prints the counts and exits 0, so the migration to
# encoding/json/v2 can proceed in steps. With JSONV1_STRICT=1 any hit fails (exit 1).
# JSONV1_VERBOSE=1 also lists every hit as file:line.
#
# Matched: one-line and block imports, with or without an alias. Not matched:
# encoding/json/v2 and encoding/json/jsontext.
# Exempt:  backend/migrations/ (applied history, never rewritten).
# Allowed: exactly backend/internal/jsonx/v1opts.go (the host's reserved v1 bridge),
#          backend/pkg/sdk/guest/v1opts.go (its guest-side mirror, options only) and
#          backend/internal/pluginhost/testdata/legacy/main.go (a frozen v1 guest
#          built into legacy.wasm; it must keep the v1 encoder).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

ALLOW='backend/internal/jsonx/v1opts.go
backend/pkg/sdk/guest/v1opts.go
backend/internal/pluginhost/testdata/legacy/main.go'
IMPORT_RE='^[[:space:]]*(import[[:space:]]+)?([A-Za-z_.][A-Za-z0-9_]*[[:space:]]+)?"encoding/json"'

# One pass: tracked backend Go files minus the exemption and the allow-listed file.
hits=$(git ls-files -z -- 'backend/*.go' \
  | { grep -zvE '^backend/migrations/' || true; } \
  | { grep -zvxF "$ALLOW" || true; } \
  | { xargs -0 -r grep -nE "$IMPORT_RE" /dev/null || true; })

src=0; tst=0
if [ -n "$hits" ]; then
  tst=$(grep -cE '^[^:]*_test\.go:' <<<"$hits" || true)
  total=$(grep -c . <<<"$hits")
  src=$((total - tst))
fi

if [ $((src + tst)) -eq 0 ]; then
  echo "check-jsonv1: OK"
  exit 0
fi

echo "check-jsonv1: WARN $src non-test files and $tst test files import encoding/json (v1)"
echo "check-jsonv1: exempt: backend/migrations/"
if [ "${JSONV1_VERBOSE:-}" = 1 ]; then
  cut -d: -f1,2 <<<"$hits"
fi
if [ "${JSONV1_STRICT:-}" = 1 ]; then
  echo "check-jsonv1: FAIL (JSONV1_STRICT=1)"
  exit 1
fi
echo "check-jsonv1: OK (warn-only mode; set JSONV1_STRICT=1 to fail)"
