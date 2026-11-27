#!/usr/bin/env bash
# bump-compose-image.sh vX.Y.Z [compose-file] - point docker-compose.yml at a published
# release image (ghcr.io/zen-labs-corp/hatamoto:vX.Y.Z). Idempotent. Run after image.yml
# has published the tag; release candidates are refused (compose names stable releases).
set -euo pipefail
die() { echo "bump-compose-image: $*" >&2; exit "${2:-1}"; }
[[ $# -ge 1 && $# -le 2 ]] || die "usage: $0 vX.Y.Z [compose-file]" 2
tag=$1
[[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "not a release tag vX.Y.Z: $tag" 2
f=${2:-$(git rev-parse --show-toplevel)/docker-compose.yml}
re='^    image: ghcr\.io/zen-labs-corp/hatamoto:[^[:space:]]+$'
n=$(grep -cE "$re" "$f" || true)
[[ $n == 1 ]] || die "$f: want one hatamoto image line, found $n"
want="    image: ghcr.io/zen-labs-corp/hatamoto:$tag"
if grep -qxF -- "$want" "$f"; then echo "docker-compose.yml already on $tag"; exit 0; fi
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT
sed -E "s|$re|$want|" "$f" >"$tmp" && cat "$tmp" >"$f"   # cat keeps the file mode
echo "docker-compose.yml image -> $tag"
