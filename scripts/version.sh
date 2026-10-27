#!/bin/sh
# version.sh - the version stamped into main builds (see
# docs/site/migrate-contribute/release-process.md).
#   on tag vX.Y.Z            -> vX.Y.Z
#   N commits after vX.Y.Z   -> vX.Y.(Z+1)-dev.N
#   N after vX.Y.Z-pre       -> vX.Y.Z-pre.dev.N
#   no semver v tag          -> v0.0.0-dev.<commit count>
# Only tags shaped vN.N.N[-pre] count; slug tags like v1-24-oss-release are ignored.
#   not a git checkout       -> v0.0.0-dev.0
set -eu
fallback() { n=$(git rev-list --count HEAD 2>/dev/null || echo 0); echo "v0.0.0-dev.$n"; exit 0; }
desc=$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --long 2>/dev/null) || fallback
# desc = vX.Y.Z[-pre]-N-gSHA
n=${desc%-g*}; n=${n##*-}; tag=${desc%-"$n"-g*}
# the glob also matches v1.2.3x or v1.2.3-foo-bar: only a canonical semver tag counts
case $tag in v[0-9]*.[0-9]*.[0-9]*) ;; *) fallback ;; esac
core=${tag#v}; core=${core%%-*}
case $core in *[!0-9.]*|*..*|.*|*.) fallback ;; esac
[ "$n" = 0 ] && { echo "$tag"; exit 0; }
case $tag in
  *-*) echo "$tag.dev.$n" ;;
  *) core=${tag#v}; maj=${core%%.*}; rest=${core#*.}; min=${rest%%.*}; pat=${rest#*.}
     echo "v$maj.$min.$((pat + 1))-dev.$n" ;;
esac
