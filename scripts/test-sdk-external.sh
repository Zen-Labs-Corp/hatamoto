#!/usr/bin/env bash
# Build examples/plugins/hello the way an outside plugin author would: from a
# temp directory outside the checkout, as its own module, for GOOS=wasip1.
#
#   scripts/test-sdk-external.sh            local mode: hatamoto.dev/sdk is a file
#                                           replace to backend/pkg/sdk (works offline
#                                           when the module cache has go-pdk and the
#                                           toolchain the plugin's go.mod declares, e.g.
#                                           hello needs go >= 1.27). Always tests the
#                                           working tree; a version argument is refused.
#   SDK_SOURCE=proxy SDK_VERSION=v0.12.0 scripts/test-sdk-external.sh
#                                           proxy mode: no replace, the module is
#                                           fetched through the vanity path and the Go
#                                           proxy from the public SDK repository. The
#                                           version (default: backend/pkg/sdk/version.go,
#                                           or the first argument) must already be
#                                           published there (scripts/publish-sdk.sh --push).
# GO_QUEUE_REMOTE=off keeps a queueing `go` wrapper from building on another host (local
# mode needs this checkout's files).
# PLUGIN_DIR (relative to the repo root) picks another plugin, e.g. examples/plugin-template.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
mode=${SDK_SOURCE:-local}
[ "$mode" = proxy ] || [ $# -eq 0 ] || { echo "a version argument needs SDK_SOURCE=proxy; local mode always builds the working tree" >&2; exit 1; }
version=${1:-${SDK_VERSION:-}}; version=${version#v}
[ -n "$version" ] || version=$(sed -n 's/^const Version = "\(.*\)"/\1/p' "$root/backend/pkg/sdk/version.go")
[ -n "$version" ] || { echo "cannot read the SDK version" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp -r "$root/${PLUGIN_DIR:-examples/plugins/hello}/." "$work/"
cd "$work"
rm -rf build

go mod edit -module example.org/external-hello -dropreplace hatamoto.dev/sdk "-require=hatamoto.dev/sdk@v$version"
case "$mode" in
local)
	go mod edit "-replace=hatamoto.dev/sdk=$root/backend/pkg/sdk"
	export GOFLAGS="${GOFLAGS:+$GOFLAGS }-mod=mod" GOPROXY=off
	;;
proxy)
	# SDK releases before 0.12.0 declare go 1.27, above the 1.26 of the examples.
	[ "$(printf '%s\n0.12.0\n' "$version" | sort -V | head -n1)" = 0.12.0 ] || go mod edit -go=1.27
	export GOFLAGS="${GOFLAGS:+$GOFLAGS }-mod=mod"
	;;
*)
	echo "SDK_SOURCE must be local or proxy" >&2
	exit 1
	;;
esac

GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
[ -s plugin.wasm ] || { echo "plugin.wasm is empty" >&2; exit 1; }
if grep -q 'replace' go.mod && [ "$mode" = proxy ]; then
	echo "proxy mode left a replace in go.mod" >&2
	exit 1
fi
echo "ok: external build of $(basename "${PLUGIN_DIR:-hello}") against hatamoto.dev/sdk v$version ($mode), $(wc -c <plugin.wasm) bytes"
