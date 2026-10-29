#!/usr/bin/env bash
# test-docs-examples.sh - structure checks for the fenced launch examples on the provider examples page.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
page="${DOCS_EXAMPLES_PAGE:-$here/../../docs/site/getting-started/vps-provider-examples.md}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

[ -f "$page" ] || fail "missing page: $page"

# Split every fenced block into $tmp/block-N.txt (fence lines dropped).
awk -v dir="$tmp" '
  /^```/ { if (inb) { inb = 0; close(f) } else { inb = 1; n++; f = sprintf("%s/block-%02d.txt", dir, n) } next }
  inb { print > f }
' "$page"

blocks=("$tmp"/block-*.txt)
[ -e "${blocks[0]}" ] || fail "no fenced blocks found"
[ "${#blocks[@]}" -ge 5 ] || fail "expected at least 5 examples, found ${#blocks[@]}"

allowed='HATAMOTO_(DOMAIN|VERSION|ADMIN_EMAIL|SETUP_CODE|BACKUPS|TLS|FIREWALL|PUBLIC_IP)'
yaml_ok=0
python3 -c 'import yaml' 2>/dev/null && yaml_ok=1
[ "$yaml_ok" = 1 ] || echo "SKIP: python3 yaml module missing, #cloud-config blocks are not parsed" >&2

for b in "${blocks[@]}"; do
  name="$(basename "$b" .txt)"
  first="$(head -n1 "$b")"
  case "$first" in
    '#cloud-config' | '#!/bin/sh') ;;
    *) fail "$name: first line must be #cloud-config or #!/bin/sh, got: $first" ;;
  esac
  if grep -Eq 'curl[^|]*\|[^|]*sh' "$b"; then fail "$name: pipes curl into a shell (a failed download would pass): download to a file first"; fi
  grep -q 'https://hatamoto.dev/install.sh' "$b" || fail "$name: does not reference the installer URL"
  while read -r var; do
    [[ "$var" =~ ^$allowed$ ]] || fail "$name: unknown input $var"
  done < <(grep -o 'HATAMOTO_[A-Z_]*' "$b" | sort -u)
  if grep -q 'HATAMOTO_SETUP_CODE=' "$b" && ! grep -q 'HATAMOTO_SETUP_CODE=CHANGE-ME' "$b"; then
    fail "$name: setup code must be the CHANGE-ME placeholder"
  fi
  if [ "$first" = '#cloud-config' ] && [ "$yaml_ok" = 1 ]; then
    python3 - "$b" <<'PY' || fail "$name: yaml structure invalid"
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
assert isinstance(d, dict), "top level must be a mapping"
rc = d.get("runcmd")
assert isinstance(rc, list) and rc, "runcmd must be a non-empty list"
assert any("install.sh" in str(c) for c in rc), "runcmd must run install.sh"
PY
  fi
  pass "$name ($first)"
done

# The page must not carry a literal setup code outside the placeholder.
if grep -Eq 'SETUP_CODE=[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}' "$page"; then
  fail "page contains a literal setup code"
fi
pass "no literal setup code"

# The banner in the install guide is the text first boot prints (link, code and domain values aside).
guide="${DOCS_GUIDE_PAGE:-$here/../../docs/site/getting-started/install-vps.md}"
[ -f "$guide" ] || fail "missing page: $guide"
norm() {
  sed -e 's/^[[:space:]]*//' -e '/^$/d' \
    -e 's#^https://.*#LINK#' -e 's#^Setup code:.*#Setup code: CODE#' \
    -e 's#^Domain:.*#Domain: DOMAIN#'
}
awk '/^```text$/ { blk = ""; inb = 1; next } /^```$/ { if (inb && blk ~ /Hatamoto is ready\./) { printf "%s", blk; exit } inb = 0; next } inb { blk = blk $0 "\n" }' "$guide" | norm > "$tmp/doc-banner"
[ -s "$tmp/doc-banner" ] || fail "no banner block in the install guide"
bash "$here/build-install.sh" --out "$tmp/asm" >/dev/null
HATAMOTO_INSTALL_SOURCE_ONLY=1 sh -c '. "$1"; fb_banner https://x.example/setup "K7M2" example.com 203.0.113.7' _ "$tmp/asm/firstboot.sh" | norm > "$tmp/real-banner"
diff -u "$tmp/real-banner" "$tmp/doc-banner" >&2 || fail "the banner in the install guide differs from the one first boot prints"
pass "install guide banner matches fb_banner"
