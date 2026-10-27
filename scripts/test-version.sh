#!/usr/bin/env bash
# test-version.sh - fixture tests for scripts/version.sh in throwaway repos,
# plus a check that `make build-api` stamps a semver. Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
REPO=$PWD
V="$REPO/scripts/version.sh"
bash -n "$V"
# Same shape as canonicalVersion in backend/internal/release/release.go.
RE='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$'

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
cd "$W"
git init -q -b main r; cd r
git config user.email t@t; git config user.name t
c() { for _ in $(seq "${1:-1}"); do git commit -q --allow-empty -m c; done; }
expect() {
  local got; got=$(sh "$V")
  [[ "$got" == "$1" ]] || { echo "FAIL: $2: got $got, want $1"; exit 1; }
  [[ "$got" =~ $RE ]] || { echo "FAIL: $2: $got is not canonical semver"; exit 1; }
  echo "PASS: $2 -> $got"
}
c 3; expect v0.0.0-dev.3 "no tag"
git tag other-tag; expect v0.0.0-dev.3 "non-v tag ignored"
git tag v0.25.0; expect v0.25.0 "on tag"
c 3; expect v0.25.1-dev.3 "3 after tag"
git tag v0.25.0-rc.1; c 2
git tag -d v0.25.0 >/dev/null
expect v0.25.0-rc.1.dev.2 "prerelease tag"
# slug-style and two-part tags are not release tags
git tag -d v0.25.0-rc.1 >/dev/null; git tag v1-24-oss-release; expect v0.0.0-dev.8 "slug tag v1-24-oss-release ignored"
git tag v0.25; expect v0.0.0-dev.8 "two-part tag v0.25 ignored"
git tag v0.25.0-rc.2; expect v0.25.0-rc.2 "prerelease tag still counts"
cd "$W"; mkdir plain; cd plain
got=$(GIT_CEILING_DIRECTORIES=$W sh "$V"); [[ "$got" == v0.0.0-dev.0 ]] || { echo "FAIL: not a checkout: $got"; exit 1; }
echo "PASS: not a checkout -> $got"

cd "$REPO"
line=$(make -n build-api | grep -o 'app.Version=v[^ ]*' | head -1)
[[ "v${line#app.Version=v}" =~ $RE ]] || { echo "FAIL: build-api stamps '$line'"; exit 1; }
echo "PASS: make build-api stamps $line"
echo "version tests ok"
