#!/usr/bin/env bash
# bump-compose-image.sh vX.Y.Z [compose-file [readme-file]] - point docker-compose.yml at a
# published release image (ghcr.io/zen-labs-corp/hatamoto:vX.Y.Z) and the README's quoted
# tag with it. Idempotent. Run after image.yml
# has published the tag; release candidates are refused (compose names stable releases).
set -euo pipefail
die() { echo "bump-compose-image: $*" >&2; exit "${2:-1}"; }
[[ $# -ge 1 && $# -le 3 ]] || die "usage: $0 vX.Y.Z [compose-file [readme-file]]" 2
tag=$1
[[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "not a release tag vX.Y.Z: $tag" 2
top=$(git rev-parse --show-toplevel)
f=${2:-$top/docker-compose.yml}
readme=${3:-$top/README.md}
re='^    image: ghcr\.io/zen-labs-corp/hatamoto:[^[:space:]]+$'
n=$(grep -cE "$re" "$f" || true)
[[ $n == 1 ]] || die "$f: want one hatamoto image line, found $n"
want="    image: ghcr.io/zen-labs-corp/hatamoto:$tag"
# The README quotes the tag (prose and snippet); the quoted lines are rewritten with the compose file.
rre='ghcr\.io/zen-labs-corp/hatamoto:v[0-9]+\.[0-9]+\.[0-9]+'
rwant="ghcr.io/zen-labs-corp/hatamoto:$tag"
[[ -f $readme ]] || die "$readme: not found"
grep -qE "$rre" "$readme" || die "$readme: no hatamoto image tag to bump"
if grep -qxF -- "$want" "$f" && ! grep -oE "$rre" "$readme" | grep -qvxF -- "$rwant"; then
  echo "$f and $readme already on $tag"; exit 0
fi
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT
sed -E "s|$re|$want|" "$f" >"$tmp" || die "$f: rewrite failed"
cat "$tmp" >"$f"   # cat keeps the file mode
sed -E "s|$rre|$rwant|g" "$readme" >"$tmp" || die "$readme: rewrite failed"
cat "$tmp" >"$readme"
echo "$f image -> $tag"
echo "$readme image -> $tag"
