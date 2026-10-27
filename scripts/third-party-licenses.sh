#!/usr/bin/env bash
# third-party-licenses.sh - write the THIRD_PARTY_LICENSES tree that ships with
# every binary distribution (release bundles and the container image).
#
# Usage: scripts/third-party-licenses.sh OUTDIR
#
# OUTDIR/go/LICENSE         the Go toolchain's licence (the runtime and standard
#                           library are compiled into the binary; BSD-3 asks for
#                           the notice in binary redistributions)
# OUTDIR/go-modules/        every compiled Go module's licence (tools/licenses)
# OUTDIR/web-vendor/        the vendored JS NOTICE and licence texts
# OUTDIR/fonts/OFL.txt      the bundled font licence
#
# The caller's environment picks the toolchain (the Makefile pins GOTOOLCHAIN).
# Fails when the Go LICENSE is missing or is not the Go BSD-3 text, so a
# toolchain layout change cannot ship a bundle without it. Needs the module
# cache (`go mod download`).
set -Eeuo pipefail

[ $# -eq 1 ] || { sed -n '2,5p' "$0" >&2; exit 64; }
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
out=$1
mkdir -p "$out"; out=$(cd "$out" && pwd)

goroot=$(cd "$root/backend" && go env GOROOT)
lic=$goroot/LICENSE
[ -s "$lic" ] || { echo "third-party-licenses: no Go LICENSE at $lic" >&2; exit 1; }
grep -q 'The Go Authors' "$lic" && grep -q 'Redistribution and use in source and binary forms' "$lic" \
  || { echo "third-party-licenses: $lic is not the Go BSD-3 licence" >&2; exit 1; }
mkdir -p "$out/go" "$out/fonts" "$out/web-vendor"
cp "$lic" "$out/go/LICENSE"

(cd "$root/backend" && go run ./tools/licenses -out "$out/go-modules" >/dev/null)
cp "$root/backend/web/vendor/NOTICE" "$out/web-vendor/NOTICE"
cp -R "$root/backend/web/vendor/licenses" "$out/web-vendor/licenses"
cp "$root/backend/internal/render/themes/default/assets/fonts/OFL.txt" "$out/fonts/OFL.txt"
