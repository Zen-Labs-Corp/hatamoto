#!/usr/bin/env bash
# Offline test of the Server/Via rules in scripts/verify-cutover.sh, against a stub
# curl that serves canned headers. Run: bash scripts/test-verify-cutover.sh
set -euo pipefail
top=$(git rev-parse --show-toplevel)
if [[ ! -e "$top/scripts/verify-cutover.sh" ]]; then
  echo "test-verify-cutover: SKIP (scripts/verify-cutover.sh absent: operator tooling, not in the public tree)"
  exit 0
fi
w=$(mktemp -d); trap 'rm -rf "$w"' EXIT
cat >"$w/curl" <<'S'
#!/usr/bin/env bash
# prints $STUB_HEADERS for -D -, "ok" for everything else
for a in "$@"; do [[ $a == "-D" ]] && { printf '%b' "$STUB_HEADERS"; exit 0; }; done
echo ok
S
chmod +x "$w/curl"
run() { PATH="$w:$PATH" STUB_HEADERS="$1" bash "$top/scripts/verify-cutover.sh" http://x 2>&1 || true; }
fail() { echo "FAIL: $*" >&2; exit 1; }
for h in 'HTTP/2 200\r\n\r\n' 'HTTP/2 200\r\nServer: cloudflare\r\n\r\n' 'HTTP/2 200\r\nserver: Cloudflare\r\n\r\n'; do
  run "$h" | grep -q '^ok   server header absent or cloudflare' || fail "should pass: $h"
done
for h in 'HTTP/2 200\r\nServer: Caddy\r\n\r\n' 'HTTP/2 200\r\nServer: cloudflare-x\r\n\r\n'; do
  run "$h" | grep -q '^FAIL server header' || fail "should fail: $h"
done
run 'HTTP/2 200\r\nVia: 1.1 Caddy\r\n\r\n' | grep -q '^FAIL no via header' || fail "Via must fail"
run 'HTTP/2 200\r\n\r\n' | grep -q '^ok   no via header' || fail "absent Via must pass"
echo "test-verify-cutover: ok"
