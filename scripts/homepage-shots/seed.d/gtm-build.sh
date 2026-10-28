#!/usr/bin/env bash
# Builds the gtm plugin from the plugins/gtm folder of a sibling hatamoto-first-party
# checkout and packs it into a .hmp signed with a local demo key, so the install prompt can show "Signed by <key id>". Only a key you
# generated is used; nothing is published.
# Usage: gtm-build.sh OUT.hmp KEY_FILE   (env: HATAMOTO_BIN, GTM_REPO)
set -Eeuo pipefail
out=${1:?usage: gtm-build.sh OUT.hmp KEY_FILE}; key=${2:?usage: gtm-build.sh OUT.hmp KEY_FILE}
BIN=${HATAMOTO_BIN:-hatamoto}
repo=${GTM_REPO:-$(cd "$(dirname "$0")/../../.." && pwd)/../hatamoto-first-party/plugins/gtm}
[ -f "$repo/plugin.json" ] || { echo "gtm-build: plugin folder not found at $repo (set GTM_REPO)" >&2; exit 2; }
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
# Build in a copy so the sibling checkout stays untouched.
cp -r "$repo"/. "$work/src" && rm -rf "$work/src/build" "$work/src/.git"
(cd "$work/src" && mkdir build && CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm . \
  && cp plugin.json build/ && cp -r assets build/assets)
"$BIN" plugin pack "$work/src/build" --out "$(realpath -m "$out")" --key "$key" >/dev/null
echo "gtm-build: $out"
