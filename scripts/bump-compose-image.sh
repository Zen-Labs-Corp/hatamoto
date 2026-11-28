#!/usr/bin/env bash
# bump-compose-image.sh vX.Y.Z [compose-file [readme-file [render-file [do-file]]]] - point
# docker-compose.yml at a published release image (ghcr.io/zen-labs-corp/hatamoto:vX.Y.Z), the
# README's quoted tag, render.yaml and .do/deploy.template.yaml with it. Idempotent. Run after
# image.yml has published the tag; release candidates are refused (compose names stable releases).
# The render and DO files default to the ones next to the compose file and are skipped when
# missing, so a compose file elsewhere (the tests) never touches the repository's files. An
# explicitly given path must exist. Every file is checked before any is written.
set -euo pipefail
die() { echo "bump-compose-image: $*" >&2; exit "${2:-1}"; }
[[ $# -ge 1 && $# -le 5 ]] || die "usage: $0 vX.Y.Z [compose-file [readme-file [render-file [do-file]]]]" 2
tag=$1
[[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "not a release tag vX.Y.Z: $tag" 2
top=$(git rev-parse --show-toplevel)
f=${2:-$top/docker-compose.yml}
readme=${3:-$top/README.md}
dir=$(dirname "$f")
# optional_file EXPLICIT DEFAULT: the explicit path (must exist), else the default when present, else empty.
optional_file() {
  if [[ -n $1 ]]; then [[ -f $1 ]] || die "$1: not found"; echo "$1"
  elif [[ -f $2 ]]; then echo "$2"
  fi
}
render=$(optional_file "${4:-}" "$dir/render.yaml")
dospec=$(optional_file "${5:-}" "$dir/.do/deploy.template.yaml")
re='^    image: ghcr\.io/zen-labs-corp/hatamoto:[^[:space:]]+$'
want="    image: ghcr.io/zen-labs-corp/hatamoto:$tag"
render_re='^      url: ghcr\.io/zen-labs-corp/hatamoto:[^[:space:]]+$'
render_want="      url: ghcr.io/zen-labs-corp/hatamoto:$tag"
do_re='^        tag: [^[:space:]]+$'
do_want="        tag: $tag"
# The README quotes the tag (prose and snippet); the quoted lines are rewritten with the compose file.
rre='ghcr\.io/zen-labs-corp/hatamoto:v[0-9]+\.[0-9]+\.[0-9]+'
rwant="ghcr.io/zen-labs-corp/hatamoto:$tag"

# one_line FILE REGEX WHAT: exactly one line must match.
one_line() {
  local n; n=$(grep -cE "$2" "$1" || true)
  [[ $n == 1 ]] || die "$1: want one $3 line, found $n"
}
one_line "$f" "$re" "hatamoto image"
[[ -f $readme ]] || die "$readme: not found"
grep -qE "$rre" "$readme" || die "$readme: no hatamoto image tag to bump"
if [[ -n $render ]]; then one_line "$render" "$render_re" "hatamoto image url"; fi
if [[ -n $dospec ]]; then
  one_line "$dospec" "$do_re" "image tag"
  grep -qxF '        repository: hatamoto' "$dospec" || die "$dospec: not the hatamoto image (no 'repository: hatamoto' line)"
fi

if grep -qxF -- "$want" "$f" && ! grep -oE "$rre" "$readme" | grep -qvxF -- "$rwant" \
  && { [[ -z $render ]] || grep -qxF -- "$render_want" "$render"; } \
  && { [[ -z $dospec ]] || grep -qxF -- "$do_want" "$dospec"; }; then
  echo "all files already on $tag"; exit 0
fi
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT
rewrite() { # FILE SED-EXPRESSION
  sed -E "$2" "$1" >"$tmp" || die "$1: rewrite failed"
  cat "$tmp" >"$1"   # cat keeps the file mode
  echo "$1 image -> $tag"
}
rewrite "$f" "s|$re|$want|"
rewrite "$readme" "s|$rre|$rwant|g"
if [[ -n $render ]]; then rewrite "$render" "s|$render_re|$render_want|"; fi
if [[ -n $dospec ]]; then rewrite "$dospec" "s|$do_re|$do_want|"; fi
