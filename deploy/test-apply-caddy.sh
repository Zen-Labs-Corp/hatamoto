#!/usr/bin/env bash
# Tests deploy/apply-caddy.sh against sample Caddyfiles using the REAL caddy
# for validate/adapt (reload is faked). "Other sites unchanged" is judged by
# `caddy adapt` JSON, not by the script's own block-finding rules.
# Run: deploy/test-apply-caddy.sh   (needs caddy and jq; REAL_CADDY overrides)
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

real=${REAL_CADDY:-$(command -v caddy || true)}
[[ -n $real ]] || { echo "FAIL: caddy not found (install it or set REAL_CADDY)" >&2; exit 1; }
command -v jq >/dev/null || { echo "FAIL: jq not found" >&2; exit 1; }

# Wrapper: validate/adapt go to the real caddy; reload is recorded and fails
# the first $FAIL_RELOADS times.
cat >"$work/caddy" <<FAKE
#!/usr/bin/env bash
echo "\$*" >>"\$CADDY_LOG"
case \$1 in
reload)
	n=\$(cat "\$CADDY_LOG.reloads" 2>/dev/null || echo 0)
	echo \$((n + 1)) >"\$CADDY_LOG.reloads"
	(( n >= \${FAIL_RELOADS:-0} )) ;;
*) exec "$real" "\$@" ;;
esac
FAKE
chmod +x "$work/caddy"
export CADDY_BIN="$work/caddy" CADDY_LOG="$work/calls.log"

fail() { echo "FAIL: $*" >&2; exit 1; }

adapt() { "$real" adapt --config "$1" --adapter caddyfile 2>/dev/null; }

# Everything caddy sees outside the two hatamoto hosts.
others() { adapt "$1" | jq -S --arg cfg "$1" -f "$here/caddy-others.jq"; }
# (error routes from handle_errors repeat the host matcher: left out of the count)
hosts() { adapt "$1" | jq -r 'del(.apps.http.servers[]?.errors) | [.. | objects | select(has("match")) | .match[]?.host[]?] | sort | .[]'; }

# apply FILE: run the script; check other sites are unchanged and the tracked
# hatamoto blocks are in. $2.. = extra hosts that must survive.
applies() {
	local f=$1; shift
	cp "$f" "$f.orig"
	"$here/apply-caddy.sh" "$f" >/dev/null || fail "$f: script failed"
	diff <(others "$f.orig") <(others "$f") >/dev/null || fail "$f: other sites changed"
	local h; h=$(hosts "$f")
	[[ $(grep -cx 'hatamoto\.dev' <<<"$h") == 1 ]] || fail "$f: hatamoto.dev not exactly once"
	[[ $(grep -cx 'www\.hatamoto\.dev' <<<"$h") == 1 ]] || fail "$f: www.hatamoto.dev not exactly once"
	# One plain reverse proxy to Go, nothing else, with the security headers.
	adapt "$f" | grep -q 'Strict-Transport-Security' || fail "$f: new block missing"
	if adapt "$f" | grep -q 'localhost:3001'; then fail "$f: nothing may be proxied to Next (localhost:3001) any more"; fi
	local s; for s in "$@"; do grep -qx "$s" <<<"$h" || fail "$f: site $s was lost"; done
}

# refuses FILE: the script must exit non-zero and leave the file untouched.
refuses() {
	cp "$1" "$1.orig"
	if "$here/apply-caddy.sh" "$1" >/dev/null 2>"$1.err"; then fail "$1: should have been refused"; fi
	cmp "$1" "$1.orig" || fail "$1: file changed despite refusal"
}

sample() {
	cat >"$1" <<'SAMPLE'
{
	email ops@example.com
}

www.example.org {
	redir https://example.org{uri} permanent
}

example.org {
	header {
		X-Frame-Options "SAMEORIGIN"
	}
	handle /api/* {
		reverse_proxy localhost:8080
	}
	handle {
		reverse_proxy localhost:3000
	}
}

www.hatamoto.dev {
	redir https://hatamoto.dev{uri} permanent
}

hatamoto.dev {
	handle /public.css {
		reverse_proxy localhost:3001
	}
	handle {
		reverse_proxy localhost:8090
	}
}

other.example {
	respond "hi"
}
SAMPLE
}

# 1. Replaces both blocks in place; backups and reload.
sample "$work/c1"
applies "$work/c1" example.org www.example.org other.example
grep -q '/public.css' "$work/c1" && fail "old hatamoto.dev block still present"
cmp "$work/c1.bak" "$work/c1.orig" || fail "backup missing or wrong"
ls "$work"/c1.bak.* >/dev/null || fail "timestamped backup missing"
grep -q '^reload ' "$work/calls.log" || fail "caddy was not reloaded"
grep -q '^validate .*--adapter caddyfile' "$work/calls.log" || fail "caddy was not asked to validate"

# 2. Idempotent: a second run changes nothing and does not reload.
cp "$work/c1" "$work/applied"
: >"$work/calls.log"
"$here/apply-caddy.sh" "$work/c1" | grep -q 'up to date' || fail "second run not a no-op"
cmp "$work/c1" "$work/applied" || fail "second run changed the file"
if grep -q '^reload ' "$work/calls.log"; then fail "no-op run reloaded"; fi

# 3. Blocks at the end of the file, and absent blocks (appended).
printf 'a.example {\n\trespond "a"\n}\n\nwww.hatamoto.dev {\n\tredir https://x\n}\n\nhatamoto.dev {\n\trespond "old"\n}\n' >"$work/end"
applies "$work/end" a.example
printf 'a.example {\n\trespond "a"\n}\n' >"$work/none"
applies "$work/none" a.example
cmp <(head -4 "$work/none") <(printf 'a.example {\n\trespond "a"\n}\n\n') >/dev/null || fail "append disturbed existing site"

# 4. Reviewer reproduction: a trailing comment on the closing brace.
printf 'hatamoto.dev {\n\trespond "old"\n} # end hatamoto\n\nshop.example {\n\trespond "shop"\n}\n' >"$work/cm"
applies "$work/cm" shop.example

# 5. Reviewer reproduction: CRLF line endings.
printf 'hatamoto.dev {\r\n\trespond "old"\r\n}\r\n\r\nshop.example {\r\n\trespond "shop"\r\n}\r\n' >"$work/crlf"
applies "$work/crlf" shop.example
grep -q $'^shop.example {\r$' "$work/crlf" || fail "CRLF of other sites not preserved"

# 6. Braces in strings, comments and placeholders; nested handles; snippet; import.
cat >"$work/tricky" <<'T'
{
	email ops@example.com
}

(common) {
	header X-Common "x}y"
}

# hatamoto.dev { not a block, just a comment
a.example {
	import common
	respond "x} hatamoto.dev {y"   # } {
	handle_path /x/* {
		@m path_regexp ^/{a}/(.*)$
		rewrite * /{http.regexp.m.1}
		handle {
			respond `a}b`
		}
	}
}

hatamoto.dev { # the real one
	respond "o{ld}"  # } still inside
	handle {
		respond "{x}}"
	} # closes handle
} # closes hatamoto

www.hatamoto.dev {
	redir https://hatamoto.dev{uri}
}

hatamoto.dev.evil.example {
	respond "lookalike"
}

nothatamoto.dev {
	respond "lookalike 2"
}

shop.example {
	respond "shop"
}
T
applies "$work/tricky" a.example hatamoto.dev.evil.example nothatamoto.dev shop.example
grep -q '# hatamoto.dev { not a block' "$work/tricky" || fail "comment line lost"

# 7. A hatamoto address sharing a line with others is ambiguous: refuse.
printf 'hatamoto.dev, other.example {\n\trespond "x"\n}\n\nshop.example {\n\trespond "shop"\n}\n' >"$work/amb"
refuses "$work/amb"
printf 'other.example,\nwww.hatamoto.dev {\n\trespond "x"\n}\n' >"$work/amb2"
refuses "$work/amb2"

# 8. Malformed input: unbalanced braces, junk after a closing brace.
printf 'hatamoto.dev {\n\trespond "x"\n\nshop.example {\n\trespond "s"\n}\n' >"$work/unbal"
refuses "$work/unbal"
printf 'hatamoto.dev {\n\trespond "x"\n} junk\n' >"$work/junk"
refuses "$work/junk"

# 9. A config that fails validation leaves the file untouched.
sample "$work/bad"
sed -i 's/^other.example {/other.example {\n\tnot_a_directive/' "$work/bad"
refuses "$work/bad"

# 10. A failed reload restores the previous file and reloads it again.
sample "$work/rl"
cp "$work/rl" "$work/rl.orig"
: >"$work/calls.log"; rm -f "$work/calls.log.reloads"
if FAIL_RELOADS=1 "$here/apply-caddy.sh" "$work/rl" >/dev/null 2>&1; then fail "failed reload reported success"; fi
cmp "$work/rl" "$work/rl.orig" || fail "file not restored after failed reload"
[[ $(grep -c '^reload ' "$work/calls.log") == 2 ]] || fail "restored file was not reloaded"

# 11. DRY_RUN changes nothing.
sample "$work/dry"
cp "$work/dry" "$work/dry.orig"
DRY_RUN=1 "$here/apply-caddy.sh" "$work/dry" >/dev/null
cmp "$work/dry" "$work/dry.orig" || fail "DRY_RUN wrote the file"
ls "$work"/dry.bak* >/dev/null 2>&1 && fail "DRY_RUN wrote a backup"

# 12. The script's own safety net: if its splice changed another site it must refuse.
#     (A tampered snippet that also defines a second site simulates that.)
sample "$work/net"
printf 'hatamoto.dev {\n\trespond "x"\n}\n\nsneaky.example {\n\trespond "added"\n}\n' >"$work/snip"
cp "$work/net" "$work/net.orig"
if CADDY_SNIPPET="$work/snip" "$here/apply-caddy.sh" "$work/net" >/dev/null 2>&1; then fail "changed other sites accepted"; fi
cmp "$work/net" "$work/net.orig" || fail "file written despite changed other sites"

# 13. The tracked blocks serve /releases statically with the right cache headers and still proxy the rest.
sample "$work/rel"; "$here/apply-caddy.sh" "$work/rel" >/dev/null
j=$(adapt "$work/rel")
grep -q 'public, max-age=300' <<<"$j"                  || fail "channels cache header missing"
grep -q 'public, max-age=31536000, immutable' <<<"$j"  || fail "artifact cache header missing"
grep -q '/opt/hatamoto/public' <<<"$j"                 || fail "releases root missing"
grep -q 'localhost:8090' <<<"$j"                       || fail "app proxy lost"
grep -q '/changelog#' <<<"$j"                          || fail "release notes redirect missing"
if grep -q '"browse"' <<<"$j"; then fail "directory listing enabled"; fi

# 14. Live check with the real caddy on loopback: headers, 404s, no listing, fallback to the proxy.
port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
app=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
mkdir -p "$work/pub/releases/channels" "$work/pub/releases/v0.0.0" "$work/appdir"
echo '{"c":1}' >"$work/pub/releases/channels/edge.json"
echo '{"r":1}' >"$work/pub/releases/v0.0.0/release.json"
echo 'secret' >"$work/pub/secret.txt"
mkdir -p "$work/reg/packages" "$work/reg/plugins" "$work/appdir/plugins" "$work/reg/screenshots" "$work/reg/themes" "$work/appdir/themes"
echo 'png-bytes' >"$work/reg/screenshots/shot.png"
echo 'webp-bytes' >"$work/reg/screenshots/shot.webp"
echo '<svg/>' >"$work/reg/screenshots/shot.svg"
echo '<!doctype html><title>theme-directory</title>' >"$work/reg/themes/index.html"
echo 'from-app-themes' >"$work/appdir/themes/index.html"
# A stale static page on the registry root must never be served for /plugins.
echo '<!doctype html><title>plugin-directory</title>' >"$work/reg/plugins/index.html"
echo 'from-app-plugins' >"$work/appdir/plugins/index.html"
echo '{"reg":1}' >"$work/reg/registry.json"
echo 'pkg-x' >"$work/reg/packages/x"
mkdir -p "$work/reg/archive/x"
echo 'hmp-bytes' >"$work/reg/archive/x/x-1-abc.hmp"
echo 'from-app' >"$work/appdir/index.html"
# Only the hatamoto.dev block, moved onto loopback; the app port stands in for :8090.
awk '/^hatamoto.dev \{/{f=1} f' "$here/caddy/hatamoto.dev.caddy" \
	| sed -e "s#^hatamoto.dev {#http://127.0.0.1:$port {#" \
	      -e "s#/opt/hatamoto/public#$work/pub#" -e "s#/opt/hatamoto/registry#$work/reg#" -e "s#localhost:8090#127.0.0.1:$app#" >"$work/live.caddy"
sed -i '1i {\n\tadmin off\n}\n' "$work/live.caddy"
(cd "$work/appdir" && exec python3 -m http.server "$app" --bind 127.0.0.1 >/dev/null 2>&1) &
apppid=$!
"$real" run --config "$work/live.caddy" --adapter caddyfile >"$work/live.log" 2>&1 &
cpid=$!
trap 'kill $apppid $cpid 2>/dev/null || true; rm -rf "$work"' EXIT
for _ in $(seq 50); do curl -s -o /dev/null "http://127.0.0.1:$port/" && break; sleep 0.2; done
get() { curl -s --path-as-is -D "$work/h" -o "$work/b" "http://127.0.0.1:$port$1"; head -1 "$work/h" | cut -d' ' -f2; }
[[ $(get /releases/channels/edge.json) == 200 ]] || fail "channel file not served"
grep -qi '^cache-control: public, max-age=300' "$work/h" || fail "channel Cache-Control wrong"
[[ $(get /releases/v0.0.0/release.json) == 200 ]] || fail "release file not served"
grep -qi '^cache-control: public, max-age=31536000, immutable' "$work/h" || fail "release Cache-Control wrong"
[[ $(get /registry.json) == 200 ]] && grep -q '"reg"' "$work/b" || fail "registry.json not served from the registry root"
grep -qi '^cache-control: public, max-age=300' "$work/h" || fail "registry.json Cache-Control wrong"
[[ $(get /packages/x) == 200 ]] && grep -q pkg-x "$work/b" || fail "/packages/x not served from the registry root"
grep -qi '^cache-control: public, max-age=300' "$work/h" || fail "/packages Cache-Control wrong"
[[ $(get /archive/x/x-1-abc.hmp) == 200 ]] && grep -q hmp-bytes "$work/b" || fail "/archive file not served from the registry root"
grep -qi '^cache-control: public, max-age=31536000, immutable' "$work/h" || fail "/archive Cache-Control not immutable"
# hardened <label>: the response just fetched carries exactly one of each hardening header.
hardened() {
	grep -qi '^x-content-type-options: nosniff' "$work/h" || fail "$1: nosniff missing"
	grep -qi '^referrer-policy: strict-origin-when-cross-origin' "$work/h" || fail "$1: Referrer-Policy missing"
	grep -qi "^content-security-policy: frame-ancestors 'none'" "$work/h" || fail "$1: frame-ancestors 'none' missing"
	grep -qi '^x-frame-options: DENY' "$work/h" || fail "$1: X-Frame-Options not DENY"
	[[ $(grep -ci '^x-frame-options:' "$work/h") == 1 ]] || fail "$1: X-Frame-Options sent more than once"
}
hardened "/archive file"
[[ $(get /archive/x/missing.hmp) == 404 ]] || fail "missing /archive file not 404"
hardened "/archive 404"
grep -qi '^cache-control:.*immutable' "$work/h" && fail "missing /archive file got the immutable header"
[[ $(get /archive/x/) != 200 ]] || fail "archive directory listing served"
[[ $(get /packages/x) == 200 ]] && grep -qi '^cache-control: public, max-age=300' "$work/h" || fail "/packages lost its short cache"
# /plugins is a CMS page: both forms reach the app proxy (the stub app redirects
# /plugins to /plugins/, so follow it) and never the registry root.
for u in /plugins /plugins/; do
	curl -sL --path-as-is -o "$work/b" "http://127.0.0.1:$port$u"
	grep -q from-app-plugins "$work/b" || fail "$u did not reach the app proxy"
	grep -q plugin-directory "$work/b" && fail "$u served the static page from the registry root"
done
# /themes is a CMS page too: a stale registry-root page is never served.
for u in /themes /themes/; do
	curl -sL --path-as-is -o "$work/b" "http://127.0.0.1:$port$u"
	grep -q from-app-themes "$work/b" || fail "$u did not reach the app proxy"
	grep -q theme-directory "$work/b" && fail "$u served the static page from the registry root"
done
# /screenshots/<strict name> is static, cached 5 minutes and hardened; anything else is not served from the registry.
for f in shot.png:image/png shot.webp:image/webp; do
	[[ $(get "/screenshots/${f%%:*}") == 200 ]] || fail "/screenshots/${f%%:*} not served"
	grep -qi "^content-type: ${f##*:}" "$work/h" || fail "/screenshots/${f%%:*} content-type wrong"
	grep -qi '^cache-control: public, max-age=300' "$work/h" || fail "/screenshots Cache-Control wrong"
	grep -qi '^x-content-type-options: nosniff' "$work/h" || fail "/screenshots nosniff missing"
	grep -qi '^referrer-policy: strict-origin-when-cross-origin' "$work/h" || fail "/screenshots Referrer-Policy missing"
	grep -qi "^content-security-policy: default-src 'none'; frame-ancestors 'none'" "$work/h" || fail "/screenshots CSP wrong"
	grep -qi '^x-frame-options: DENY' "$work/h" || fail "/screenshots X-Frame-Options not DENY"
	[[ $(grep -ci '^x-frame-options:' "$work/h") == 1 ]] || fail "/screenshots X-Frame-Options sent more than once"
done
# A non-image or empty name is never served from the registry root. A traversal is
# cleaned by Caddy to the path it names (/screenshots/..%2fregistry.json is /registry.json,
# which is public anyway), so it must simply not be answered by the screenshots handler.
for u in /screenshots/shot.svg /screenshots/; do
	get "$u" >/dev/null
	grep -q -e png-bytes -e webp-bytes -e '<svg' -e '"reg"' "$work/b" && fail "$u served from the registry root"
done
get "/screenshots/..%2fregistry.json" >/dev/null
grep -qi "^content-security-policy: default-src 'none'; frame-ancestors 'none'" "$work/h" && fail "a traversal was answered by the screenshots handler"
grep -q -e png-bytes -e webp-bytes -e '<svg' "$work/b" && fail "a traversal served a screenshot"
[[ $(get "/sdk?go-get=1") == 200 ]] && grep -q '<meta name="go-import" content="hatamoto.dev/sdk git https://github.com/Zen-Labs-Corp/hatamoto-sdk">' "$work/b" || fail "/sdk go-import wrong: $(cat "$work/b")"
[[ $(get "/core?go-get=1") == 200 ]] && grep -q 'content="hatamoto.dev/core git https://github.com/Zen-Labs-Corp/hatamoto backend"' "$work/b" || fail "/core go-import wrong"
[[ $(get /releases/) == 404 ]] || fail "/releases/ not 404"
[[ $(get /releases/channels/) != 200 ]] || fail "channels directory listing served"
[[ $(get /releases/v0.0.0/) != 200 ]] || fail "release directory listing served"
[[ $(get /releases/v0.0.0) == 301 ]] && grep -qi '^location: /changelog#v0.0.0' "$work/h" || fail "notes redirect wrong"
[[ $(get /releases/v0.0.0/) == 301 ]] || fail "trailing-slash redirect missing"
[[ $(get /releases/v0.0.0-rc.1) == 301 ]] && grep -qi '^location: /changelog#v0.0.0-rc.1' "$work/h" || fail "prerelease redirect wrong"
[[ $(get /releases/v0.0.0/release.json) == 200 ]] || fail "release file regressed"
[[ $(get /releases/nope) == 404 ]] || fail "unknown release path not 404"
[[ $(get /releases/x.json) == 404 ]] || fail "non-v path under /releases not 404"
[[ $(get /releases/../secret.txt) != 200 ]] || fail "path traversal served a file"
grep -q secret "$work/b" && fail "path traversal leaked a file"
[[ $(get /) == 200 ]] && grep -q from-app "$work/b" || fail "other paths no longer reach the app proxy"
kill $apppid $cpid 2>/dev/null || true
trap 'rm -rf "$work"' EXIT

# 15. An other site whose adapted JSON has an object-valued `match` (handle_response):
#     no jq error, a change to that site is detected and aborts, a jq failure aborts.
cat >"$work/objm" <<'T'
other.example {
	reverse_proxy localhost:1 {
		@err status 5xx
		handle_response @err {
			respond "{status}"
		}
	}
}

hatamoto.dev {
	respond "old"
}
T
adapt "$work/objm" | jq -e '[.. | objects | select(has("match")) | .match | type] | index("object")' >/dev/null \
	|| fail "objm: sample no longer yields an object-valued match (pick another)"
others "$work/objm" >/dev/null || fail "objm: caddy-others.jq errors on an object-valued match"
[[ -n $(others "$work/objm") ]] || fail "objm: normalised output empty"
cp "$work/objm" "$work/objm.orig"
"$here/apply-caddy.sh" "$work/objm" >/dev/null 2>"$work/objm.err" || { cat "$work/objm.err" >&2; fail "objm: apply failed"; }
diff <(others "$work/objm.orig") <(others "$work/objm") >/dev/null || fail "objm: other sites changed"
# (ii) a site with an object-valued match added by the splice must be seen and refused.
printf 'hatamoto.dev {\n\trespond "x"\n}\n\nsneaky.example {\n\treverse_proxy localhost:1 {\n\t\t@err status 5xx\n\t\thandle_response @err {\n\t\t\trespond "changed"\n\t\t}\n\t}\n}\n' >"$work/snip2"
cp "$work/objm.orig" "$work/objm"
if CADDY_SNIPPET="$work/snip2" "$here/apply-caddy.sh" "$work/objm" >/dev/null 2>"$work/objm.err"; then fail "objm: change to a site with object match not detected"; fi
cmp "$work/objm" "$work/objm.orig" || fail "objm: file written despite detected change"
grep -q "would change sites" "$work/objm.err" || { cat "$work/objm.err" >&2; fail "objm: refusal was not the change detection"; }
# (iii) a jq failure must abort, not pass vacuously.
mkdir "$work/badjq"; cp "$here/apply-caddy.sh" "$work/badjq/"; cp -r "$here/caddy" "$work/badjq/"
echo 'error("boom")' >"$work/badjq/caddy-others.jq"
cp "$work/objm.orig" "$work/objm"
if "$work/badjq/apply-caddy.sh" "$work/objm" >/dev/null 2>"$work/objm.err"; then fail "jq failure accepted"; fi
cmp "$work/objm" "$work/objm.orig" || fail "file written despite jq failure"
grep -q 'could not normalise' "$work/objm.err" || fail "jq failure not reported"
echo '.[] | empty' >"$work/badjq/caddy-others.jq"
if "$work/badjq/apply-caddy.sh" "$work/objm" >/dev/null 2>&1; then fail "empty normalised output accepted"; fi
cmp "$work/objm" "$work/objm.orig" || fail "file written despite empty output"

echo "apply-caddy: all tests passed"
