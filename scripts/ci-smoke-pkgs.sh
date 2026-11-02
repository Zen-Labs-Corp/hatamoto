#!/usr/bin/env bash
# ci-smoke-pkgs.sh - the packages of the PG18 integration smoke, on ONE line, space
# separated: ci-integration-pkgdb.sh reads PKGDB_PKGS with `read -r -a`, which stops at
# the first newline. Run from backend/. Every package with tests, minus
# scripts/ci-smoke-exclude.txt (those get `go test -short` instead).
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
exclude=$(mktemp)
trap 'rm -f "$exclude"' EXIT
sed -e 's#^#hatamoto.dev/core/#' "$here/ci-smoke-exclude.txt" >"$exclude"
go list -tags=integration -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... |
  { grep -vxFf "$exclude" || true; } | paste -sd' ' -
