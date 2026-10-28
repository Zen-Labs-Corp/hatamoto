#!/usr/bin/env bash
# test-shellcheck.sh - shellcheck over every installer script and the assembled outputs.
# Without shellcheck it skips loudly; HATAMOTO_REQUIRE_SHELLCHECK=1 makes that a failure.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"

if ! command -v shellcheck >/dev/null 2>&1; then
  echo "SKIPPED: shellcheck not installed" >&2
  [ "${HATAMOTO_REQUIRE_SHELLCHECK:-}" = 1 ] && { echo "FAIL: HATAMOTO_REQUIRE_SHELLCHECK=1 and shellcheck is missing" >&2; exit 1; }
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
bash "$here/build-install.sh" --out "$tmp" >/dev/null

sh_files=("$tmp/install.sh" "$tmp/firstboot.sh")
while IFS= read -r f; do sh_files+=("$f"); done < <(find "$here/src" -name '*.sh' | sort)
shellcheck -s sh "${sh_files[@]}"

bash_files=()
for f in "$here"/*.sh "$here"/testlib/*.sh "$repo/scripts/publish-install.sh" "$repo/scripts/test-publish-install.sh"; do
  [ -f "$f" ] && bash_files+=("$f")
done
shellcheck -s bash -x "${bash_files[@]}"

echo "ok: test-shellcheck"
