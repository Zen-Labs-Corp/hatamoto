#!/usr/bin/env bash
# test-version.sh - fixture tests for scripts/version.sh in throwaway repos,
# plus checks that `make build-api` stamps a semver and passes it as data. Run by `make check`.
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

# a tag whose prerelease part has shell metacharacters falls back
cd "$W"; git init -q -b main u; cd u
git config user.email t@t; git config user.name t
git commit -q --allow-empty -m c
git tag 'v1.3.8-x";touch${IFS}marker;"'
git tag -l | grep -q touch || { echo "FAIL: fixture tag missing"; exit 1; }
got=$(sh "$V")
[[ "$got" =~ ^v0\.0\.0-dev\.[0-9]+$ ]] || { echo "FAIL: unsafe tag printed '$got'"; exit 1; }
echo "PASS: unsafe tag text falls back -> $got"

# make build-api hands the stamp to go through the environment, never as recipe text
cd "$W"; mkdir m; cd m
mkdir -p scripts backend stubs
echo 'toolchain go1.0.0' > backend/go.mod
cp "$REPO/Makefile" Makefile
git init -q -b main .; git config user.email t@t; git config user.name t
git add Makefile backend/go.mod; git commit -q -m c
printf '#!/bin/sh\nprintf "%%s\\n" "$@" > "%s/go.args"\nmkdir -p bin; : > bin/hatamoto\n' "$W" > stubs/go
printf '#!/bin/sh\necho "statically linked"\n' > stubs/file
chmod +x stubs/go stubs/file
build() { PATH="$W/m/stubs:$PATH" make build-api >/dev/null; }
ldflags() { grep -A1 -x -e '-ldflags' "$W/go.args" | tail -1; }

cp "$REPO/scripts/version.sh" scripts/version.sh
git tag -a v1.2.3 -m v1.2.3
git commit -q --allow-empty -m c
build
got=$(ldflags)
ver=${got#*app.Version=}; ver=${ver%% *}
[[ "$ver" =~ $RE ]] || { echo "FAIL: build-api stamps '$ver'"; exit 1; }
[[ "$got" == *"app.Commit=$(git rev-parse HEAD)"* ]] || { echo "FAIL: build-api commit stamp: $got"; exit 1; }
echo "PASS: make build-api stamps $ver"

bad='v1.3.8-x";touch marker;"'
printf '#!/bin/sh\necho '"'"'%s'"'"'\n' "$bad" > scripts/version.sh
rm -f "$W/go.args"; build
[[ -z $(find "$W/m" -name marker) ]] || { echo "FAIL: stamp text was executed"; exit 1; }
[[ "$(ldflags)" == *"app.Version=$bad "* ]] || { echo "FAIL: stamp not passed intact: $(ldflags)"; exit 1; }
echo "PASS: stamp text is passed as data"
echo "version tests ok"
