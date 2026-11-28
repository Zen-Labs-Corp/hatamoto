#!/usr/bin/env bash
# test-bump-compose-image.sh - offline test of scripts/bump-compose-image.sh. Run by `make check`.
set -euo pipefail
S="$(git rev-parse --show-toplevel)/scripts/bump-compose-image.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
F=$T/compose.yml
R=$T/README.md
mk() {
  cat >"$R" <<'Y'
`docker-compose.yml` runs `ghcr.io/zen-labs-corp/hatamoto:v1.0.0`:

    image: ghcr.io/zen-labs-corp/hatamoto:v1.0.0
Y
  cat >"$F" <<'Y'
services:
  db:
    image: postgres:16
  hatamoto:
    image: ghcr.io/zen-labs-corp/hatamoto:v1.0.0
  mailpit:
    image: axllent/mailpit:v1.34
Y
}
mk; cp "$F" "$T/orig"; cp "$R" "$T/rorig"
bash "$S" v1.4.6 "$F" "$R" >/dev/null || fail "bump to v1.4.6 failed"
grep -qxF '    image: ghcr.io/zen-labs-corp/hatamoto:v1.4.6' "$F" || fail "image line not rewritten"
[[ $(grep -c 'hatamoto:v1.4.6' "$R") == 2 ]] || fail "README tags not rewritten"
! grep -q 'v1.0.0' "$R" || fail "README still has the old tag"
[[ $(diff "$T/orig" "$F" | grep -c '^>') == 1 ]] || fail "more than one line changed"
cp "$F" "$T/once"; cp "$R" "$T/ronce"
bash "$S" v1.4.6 "$F" "$R" >/dev/null || fail "second run failed"
cmp -s "$T/once" "$F" && cmp -s "$T/ronce" "$R" || fail "second run changed the files"
for bad in 1.4.6 v1.4 v1.4.6-rc.1 v01.4.6 vX.Y.Z; do
  mk; rc=0; bash "$S" "$bad" "$F" "$R" >/dev/null 2>&1 || rc=$?
  [[ $rc == 2 ]] || fail "$bad: exit $rc, want 2"
  cmp -s "$T/orig" "$F" && cmp -s "$T/rorig" "$R" || fail "$bad: file changed"
done
grep -v 'hatamoto:v1' "$T/orig" >"$F"; cp "$F" "$T/zero"
rc=0; bash "$S" v1.4.6 "$F" "$R" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "zero image lines: exit $rc, want 1"
cmp -s "$T/zero" "$F" || fail "zero image lines: file changed"
mk; echo '    image: ghcr.io/zen-labs-corp/hatamoto:v1.1.0' >>"$F"; cp "$F" "$T/two"
rc=0; bash "$S" v1.4.6 "$F" "$R" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "two image lines: exit $rc, want 1"
cmp -s "$T/two" "$F" || fail "two image lines: file changed"
mk; echo 'no tag here' >"$R"
rc=0; bash "$S" v1.4.6 "$F" "$R" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "README without a tag: exit $rc, want 1"
cmp -s "$T/orig" "$F" || fail "README without a tag: compose changed"
# render.yaml and the DO template next to the compose file are bumped with it.
mkspecs() {
  mk; mkdir -p "$T/.do"
  cat >"$T/render.yaml" <<'Y'
services:
  - type: web
    image:
      url: ghcr.io/zen-labs-corp/hatamoto:v1.0.0
Y
  cat >"$T/.do/deploy.template.yaml" <<'Y'
spec:
  services:
    - name: hatamoto
      image:
        registry_type: GHCR
        repository: hatamoto
        tag: v1.0.0
Y
  cp "$T/render.yaml" "$T/render.orig"; cp "$T/.do/deploy.template.yaml" "$T/do.orig"
}
unchanged() { # label: nothing was written (compose, README, render, DO)
  cmp -s "$T/orig" "$F" && cmp -s "$T/rorig" "$R" && cmp -s "$T/render.orig" "$T/render.yaml" \
    && cmp -s "$T/do.orig" "$T/.do/deploy.template.yaml" || fail "$1: a file changed"
}
mkspecs; cp "$F" "$T/orig"; cp "$R" "$T/rorig"
bash "$S" v1.4.6 "$F" "$R" >/dev/null || fail "bump with specs failed"
grep -qxF '      url: ghcr.io/zen-labs-corp/hatamoto:v1.4.6' "$T/render.yaml" || fail "render url not rewritten"
grep -qxF '        tag: v1.4.6' "$T/.do/deploy.template.yaml" || fail "DO tag not rewritten"
[[ $(diff "$T/render.orig" "$T/render.yaml" | grep -c '^>') == 1 ]] || fail "render: more than one line changed"
[[ $(diff "$T/do.orig" "$T/.do/deploy.template.yaml" | grep -c '^>') == 1 ]] || fail "DO: more than one line changed"
cp "$T/render.yaml" "$T/render.once"; cp "$T/.do/deploy.template.yaml" "$T/do.once"
bash "$S" v1.4.6 "$F" "$R" >/dev/null || fail "second run with specs failed"
cmp -s "$T/render.once" "$T/render.yaml" && cmp -s "$T/do.once" "$T/.do/deploy.template.yaml" || fail "second run changed the specs"
# a spec on an old tag alone makes the run a bump again
cp "$T/render.orig" "$T/render.yaml"
bash "$S" v1.4.6 "$F" "$R" >/dev/null || fail "run with stale render failed"
cmp -s "$T/render.once" "$T/render.yaml" || fail "stale render not rewritten"
# explicit paths in another directory
mkspecs; mkdir "$T/other"; mv "$T/render.yaml" "$T/other/r.yaml"; mv "$T/.do/deploy.template.yaml" "$T/other/d.yaml"
bash "$S" v1.4.6 "$F" "$R" "$T/other/r.yaml" "$T/other/d.yaml" >/dev/null || fail "explicit 5-arg bump failed"
grep -q 'hatamoto:v1.4.6' "$T/other/r.yaml" && grep -q 'tag: v1.4.6' "$T/other/d.yaml" || fail "explicit files not rewritten"
# no partial bump: zero or two url lines, a missing explicit path, a DO file for another image
mkspecs; : >"$T/render.yaml"; cp "$T/render.yaml" "$T/render.orig"
rc=0; bash "$S" v1.4.6 "$F" "$R" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "zero url lines: exit $rc, want 1"; unchanged "zero url lines"
mkspecs; echo '      url: ghcr.io/zen-labs-corp/hatamoto:v1.1.0' >>"$T/render.yaml"; cp "$T/render.yaml" "$T/render.orig"
rc=0; bash "$S" v1.4.6 "$F" "$R" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "two url lines: exit $rc, want 1"; unchanged "two url lines"
mkspecs
rc=0; bash "$S" v1.4.6 "$F" "$R" "$T/missing.yaml" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "missing explicit render: exit $rc, want 1"; unchanged "missing explicit render"
mkspecs; sed -i 's/repository: hatamoto/repository: other/' "$T/.do/deploy.template.yaml"; cp "$T/.do/deploy.template.yaml" "$T/do.orig"
rc=0; bash "$S" v1.4.6 "$F" "$R" >/dev/null 2>&1 || rc=$?
[[ $rc == 1 ]] || fail "foreign DO image: exit $rc, want 1"; unchanged "foreign DO image"
echo "bump-compose-image tests ok"
