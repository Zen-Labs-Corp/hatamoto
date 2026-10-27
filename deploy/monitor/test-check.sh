#!/usr/bin/env bash
# Tests check.sh against a local static server serving fixture responses:
# healthy, an expiring manifest, an expired manifest, malformed JSON, a missing
# registry, a bad go-import and an expiring security.txt; and that the alert
# webhook fires on failure.
# Run: deploy/monitor/test-check.sh   (needs caddy, curl, jq; REAL_CADDY overrides)
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
real=${REAL_CADDY:-$(command -v caddy || true)}
[[ -n $real ]] || { echo "FAIL: caddy not found (install it or set REAL_CADDY)" >&2; exit 1; }
w=$(mktemp -d); pids=()
trap 'kill "${pids[@]}" 2>/dev/null || true; rm -rf "$w"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
port() { while :; do p=$((20000 + RANDOM % 20000)); (: >/dev/tcp/127.0.0.1/$p) 2>/dev/null || { echo $p; return; }; done; }

site=$w/site; hook=$w/hook
iso() { date -u -d "$1" +%Y-%m-%dT%H:%M:%SZ; }

# healthy: manifests valid 14 days, security.txt a year
healthy() {
	rm -rf "$site"; mkdir -p "$site/releases/channels" "$site/.well-known"
	local c; for c in stable edge canary; do
		jq -n --arg c "$c" --arg e "$(iso '+14 days')" '{algorithm:"ed25519",signer:"k",signature:"x",index:{channel:$c,expires_at:$e,releases:[]}}' >"$site/releases/channels/$c.json"
	done
	echo '{"algorithm":"ed25519","signer":"k","signature":"x","index":{"version":1,"packages":[]}}' >"$site/registry.json"
	echo '<meta name="go-import" content="127.0.0.1/sdk git https://example.test/sdk">' >"$site/sdk"
	printf 'Contact: mailto:security@example.test\nExpires: %s\n' "$(iso '+300 days')" >"$site/.well-known/security.txt"
}

hookport=$(port); port=$(port)
# Webhook stub: a Caddy site that logs request bodies to a file.
cat >"$w/hook.caddy" <<H
{
	admin off
	auto_https off
}
http://127.0.0.1:$hookport {
	log {
		output file $w/hook.log
	}
	respond 200
}
H
cat >"$w/site.caddy" <<S
{
	admin off
	auto_https off
}
http://127.0.0.1:$port {
	root * $site
	file_server
}
S
healthy
"$real" run --config "$w/hook.caddy" --adapter caddyfile >"$w/hook.out" 2>&1 & pids+=($!)
"$real" run --config "$w/site.caddy" --adapter caddyfile >"$w/site.out" 2>&1 & pids+=($!)
for _ in $(seq 50); do curl -sf "http://127.0.0.1:$port/registry.json" >/dev/null 2>&1 && break; sleep 0.1; done

run() { BASE_URL="http://127.0.0.1:$port" bash "$here/check.sh" 2>"$w/err"; }
expect_ok() { run >/dev/null || { cat "$w/err"; fail "$1: expected healthy"; }; echo "ok $1"; }
expect_fail() { # name pattern
	if run >/dev/null; then fail "$1: expected failure"; fi
	grep -q "$2" "$w/err" || { cat "$w/err"; fail "$1: message lacks '$2'"; }
	echo "ok $1"
}

expect_ok "fresh fixtures"

healthy; jq --arg e "$(iso '+3 days')" '.index.expires_at=$e' "$site/releases/channels/stable.json" >"$w/t" && mv "$w/t" "$site/releases/channels/stable.json"
expect_fail "stable expires in 3 days" 'stable.json: expires in [23] days'

healthy; jq --arg e "$(iso '-1 day')" '.index.expires_at=$e' "$site/releases/channels/canary.json" >"$w/t" && mv "$w/t" "$site/releases/channels/canary.json"
expect_fail "canary expired" 'canary.json: manifest EXPIRED'

healthy; jq --arg e "$(iso '-2 hours')" '.index.expires_at=$e' "$site/releases/channels/canary.json" >"$w/t" && mv "$w/t" "$site/releases/channels/canary.json"
expect_fail "canary expired 2 hours ago (not '0 days')" 'canary.json: manifest EXPIRED'

healthy; echo '{"index": ' >"$site/releases/channels/edge.json"
expect_fail "edge malformed JSON" 'edge.json: not valid JSON'

healthy; echo '{"index":{"expires_at":"next week"}}' >"$site/releases/channels/edge.json"
expect_fail "edge bad date" 'expires_at is not a date'

healthy; rm "$site/registry.json"
expect_fail "registry missing" 'registry.json: HTTP 404'

healthy; echo '<html>nope</html>' >"$site/sdk"
expect_fail "go-import missing" 'no go-import meta tag'

healthy; printf 'Contact: mailto:a@b.test\nExpires: %s\n' "$(iso '+10 days')" >"$site/.well-known/security.txt"
expect_fail "security.txt expiring" 'security.txt: Expires in [0-9]* days'

# Unreachable origin
if BASE_URL="http://127.0.0.1:1" bash "$here/check.sh" >/dev/null 2>"$w/err"; then fail "unreachable: expected failure"; fi
grep -q 'request failed' "$w/err" || fail "unreachable: message"
echo "ok unreachable origin"

# Threshold knob: fresh fixtures (14 days) fail with EXPIRY_DAYS=30 (the way to test an alert on the real site).
healthy
if BASE_URL="http://127.0.0.1:$port" EXPIRY_DAYS=30 bash "$here/check.sh" >/dev/null 2>"$w/err"; then fail "EXPIRY_DAYS=30: expected failure"; fi
echo "ok EXPIRY_DAYS knob"

# Alert webhook: a failure must reach it.
sleep 0.2
rm "$site/registry.json"
BASE_URL="http://127.0.0.1:$port" ALERT_WEBHOOK_URL="http://127.0.0.1:$hookport/alert" bash "$here/check.sh" >/dev/null 2>&1 && fail "webhook run: expected failure"
sleep 0.3
grep -q '/alert' "$w/hook.log" || fail "alert webhook was not called"
echo "ok alert webhook fired"
