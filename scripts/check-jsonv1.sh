#!/usr/bin/env bash
# check-jsonv1.sh - fails on Go files that import encoding/json (v1) under backend/.
# Run as suite jsonv1 (scripts/check-suites.txt) and by scripts/check-changed.sh.
#
# Fails (exit 1) on any hit and names each file; prints OK otherwise.
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

if [ -z "$hits" ]; then
  echo "check-jsonv1: OK"
  exit 0
fi

while IFS= read -r f; do
  echo "check-jsonv1: FAIL $f imports encoding/json (v1); use internal/jsonx or encoding/json/v2"
done < <(cut -d: -f1 <<<"$hits" | sort -u)
exit 1
