#!/usr/bin/env bash
# Runs the real hatamoto.dev.caddy (over plain HTTP, paths and port rewritten)
# in front of a stub "app" that sets the same security headers the Go server
# does, and asserts each response carries exactly one of every security
# header, with the HSTS value from Caddy, that no response carries a Server or
# Via header, that /security redirects to /docs/operate/security, and that security.txt is served.
# Rows cover proxied, static, go-get, Caddy-generated 404 and 502 (app stopped) responses.
# CADDYFILE=path tests another version of the Caddy file. Run: deploy/test-caddy-headers.sh   (needs caddy and curl; REAL_CADDY overrides)
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
real=${REAL_CADDY:-$(command -v caddy || true)}
[[ -n $real ]] || { echo "FAIL: caddy not found (install it or set REAL_CADDY)" >&2; exit 1; }
w=$(mktemp -d); pids=()
trap 'kill "${pids[@]}" 2>/dev/null || true; rm -rf "$w"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

port() { while :; do p=$((20000 + RANDOM % 12000)); (: >/dev/tcp/127.0.0.1/$p) 2>/dev/null || { echo $p; return; }; done; }
app=$(port); front=$(port)

mkdir -p "$w/public/.well-known" "$w/public/releases/channels" "$w/registry"
cp "$here/well-known/security.txt" "$w/public/.well-known/"
echo '{}' >"$w/public/releases/channels/stable.json"
echo '{}' >"$w/registry/registry.json"

# Stub app: same headers as backend/internal/server securityHeadersMiddleware.
cat >"$w/app.caddy" <<STUB
{
	admin off
	auto_https off
}
http://127.0.0.1:$app {
	header X-Content-Type-Options nosniff
	header X-Frame-Options DENY
	header Referrer-Policy strict-origin-when-cross-origin
	header Strict-Transport-Security "max-age=31536000; includeSubDomains"
	respond "app" 200
}
STUB

{
	printf '{\n\tadmin off\n\tauto_https off\n}\n'
	sed -e "s#^hatamoto\.dev {#http://127.0.0.1:$front {#" \
	    -e "s#localhost:8090#127.0.0.1:$app#" \
	    -e "s#/opt/hatamoto/public#$w/public#" \
	    -e "s#/opt/hatamoto/registry#$w/registry#" \
	    -e "s#^www\.hatamoto\.dev {#http://www.invalid:$front {#" "${CADDYFILE:-$here/caddy/hatamoto.dev.caddy}"
} >"$w/front.caddy"

"$real" run --config "$w/app.caddy" --adapter caddyfile >"$w/app.log" 2>&1 & pids+=($!)
"$real" run --config "$w/front.caddy" --adapter caddyfile >"$w/front.log" 2>&1 & pids+=($!)
for _ in $(seq 50); do curl -sf "http://127.0.0.1:$front/" >/dev/null 2>&1 && break; sleep 0.1; done

printf '%-38s %-6s %-5s %-5s %-5s %-5s %-6s %-4s\n' PATH STATUS HSTS XCTO XFO REFPOL SERVER VIA
bad=0
check() { # path expected-status   (prints one table row; any count != 1 or wrong HSTS fails at the end)
	local h; h=$(curl -s -D- -o /dev/null "http://127.0.0.1:$front$1" | tr -d '\r')
	local st; st=$(head -1 <<<"$h" | awk '{print $2}')
	local row; row=$(printf '%-38s %-6s' "$1" "$st")
	local f n; for f in Strict-Transport-Security X-Content-Type-Options X-Frame-Options Referrer-Policy; do
		n=$(grep -ci "^$f:" <<<"$h" || true)
		row+=$(printf ' %-5s' "$n")
		[[ $n == 1 ]] || bad=1
	done
	n=$(grep -ci '^Server:' <<<"$h" || true)
	row+=$(printf ' %-6s' "$n")
	[[ $n == 0 ]] || { row+=" (Server header present)"; bad=1; }
	n=$(grep -ci '^Via:' <<<"$h" || true)
	row+=$(printf ' %-4s' "$n")
	[[ $n == 0 ]] || { row+=" (Via header present)"; bad=1; }
	[[ $st == "$2" ]] || { row+=" (want $2)"; bad=1; }
	grep -qi '^Strict-Transport-Security: max-age=63072000; includeSubDomains; preload$' <<<"$h" || { row+=" wrong/missing HSTS value"; bad=1; }
	echo "$row"
}
check / 200                              # proxied: app's headers, Caddy's HSTS
check /.well-known/other 200             # proxied (only security.txt is Caddy's)
check /registry.json 200                 # static: Caddy's headers
check /releases/channels/stable.json 200
check /.well-known/security.txt 200
check /security 301                      # Caddy redirect to the docs page
check /security/ 301
check '/sdk?go-get=1' 200                # answered by Caddy (respond)
check '/core/x?go-get=1' 200
check /packages/missing.json 404         # Caddy-generated errors
check /releases/v9.9.9/x.json 404
check /releases/nope 404
check /archive/nope 404
loc=$(curl -s -D- -o /dev/null "http://127.0.0.1:$front/security" | tr -d '\r' | awk 'tolower($1)=="location:" {print $2}')
[[ $loc == /docs/operate/security || $loc == */docs/operate/security ]] || fail "/security Location is '$loc', want /docs/operate/security"
echo "ok /security redirects to $loc"
kill "${pids[0]}"; wait "${pids[0]}" 2>/dev/null || true   # app down
check / 502
check /admin 502
[[ $bad == 0 ]] || fail "header counts, HSTS value, Server or Via header wrong (table above)"
echo "ok: exactly one of each security header and no Server or Via header on every row"
curl -s "http://127.0.0.1:$front/.well-known/security.txt" | cmp - "$here/well-known/security.txt" || fail "security.txt differs"
curl -sI "http://127.0.0.1:$front/.well-known/security.txt" | grep -qi '^content-type: text/plain' || fail "security.txt not text/plain"
echo "ok security.txt served as text/plain"
