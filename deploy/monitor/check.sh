#!/usr/bin/env bash
# Black-box check of the public update channel, registry and Go import path.
# Needs only bash, curl, jq and GNU date. Exits 0 when everything is healthy,
# 1 (with one "FAIL:" line per problem on stderr) otherwise.
#
# Checks:
#   * /releases/channels/{stable,edge,canary}.json: HTTP 200, JSON, .index.expires_at
#     parses and is more than EXPIRY_DAYS (7) away (signed manifests live 14
#     days and are re-signed daily, so <7 days means signing has stopped)
#   * /registry.json: HTTP 200, signed JSON whose .index (or top level) has .packages
#   * /sdk?go-get=1: HTTP 200 and a go-import meta tag for <host>/sdk
#   * /.well-known/security.txt: HTTP 200, Contact present, Expires more than
#     SECURITY_TXT_DAYS (30) away
#
# Environment:
#   BASE_URL            default https://hatamoto.dev
#   EXPIRY_DAYS         default 7
#   SECURITY_TXT_DAYS   default 30
#   ALERT_WEBHOOK_URL   optional: on failure, POST {"text": "..."} here
#                       (Slack/Discord-compatible incoming webhook)
#   HEALTHCHECK_URL     optional: pinged on success, "$HEALTHCHECK_URL/fail" on
#                       failure (healthchecks.io style dead-man switch)
set -uo pipefail

BASE_URL=${BASE_URL:-https://hatamoto.dev}; BASE_URL=${BASE_URL%/}
EXPIRY_DAYS=${EXPIRY_DAYS:-7}
SECURITY_TXT_DAYS=${SECURITY_TXT_DAYS:-30}
host=${BASE_URL#*://}; host=${host%%/*}; host=${host%%:*}

command -v curl >/dev/null && command -v jq >/dev/null || { echo "FAIL: curl and jq are required" >&2; exit 1; }

now=$(date -u +%s)
problems=()
fail() { problems+=("$*"); }

# get URL: sets $body and $code; empty $code means the connection failed.
get() {
	local out
	out=$(curl -sS -m 20 -w $'\n%{http_code}' "$1" 2>&1) || { code=""; body=$out; return 1; }
	code=${out##*$'\n'}; body=${out%$'\n'*}
}

# days_left TIMESTAMP -> whole days until TIMESTAMP (negative if past); empty if unparsable.
days_left() {
	[[ $1 =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$ ]] || return 1
	local t; t=$(date -u -d "$1" +%s 2>/dev/null) || return 1
	echo $(( (t - now) / 86400 ))
}

ok_get() { # path label
	get "$BASE_URL$1" || { fail "$2: request failed: $body"; return 1; }
	[[ $code == 200 ]] || { fail "$2: HTTP $code (want 200)"; return 1; }
}

for c in stable edge canary; do
	p=/releases/channels/$c.json
	ok_get "$p" "$p" || continue
	exp=$(jq -er '.index.expires_at | strings' <<<"$body" 2>/dev/null) || { fail "$p: not valid JSON or .index.expires_at missing"; continue; }
	d=$(days_left "$exp") || { fail "$p: expires_at is not a date: $exp"; continue; }
	if (( $(date -u -d "$exp" +%s) < now )); then fail "$p: manifest EXPIRED at $exp (channel signing has stopped; clients refuse it)"
	elif (( d < EXPIRY_DAYS )); then fail "$p: expires in $d days ($exp), threshold $EXPIRY_DAYS; check the channels workflow"; fi
done

if ok_get /registry.json /registry.json; then
	jq -e '(.index // .) | type == "object" and (.packages | type == "array" or type == "object")' <<<"$body" >/dev/null 2>&1 \
		|| fail "/registry.json: not valid JSON with a .packages member (under .index)"
fi

if ok_get '/sdk?go-get=1' '/sdk?go-get=1'; then
	grep -qE "name=\"go-import\" content=\"$host/sdk git https://[^\"]+\"" <<<"$body" \
		|| fail "/sdk?go-get=1: no go-import meta tag for $host/sdk"
fi

if ok_get /.well-known/security.txt /.well-known/security.txt; then
	grep -qiE '^Contact:[[:space:]]+\S' <<<"$body" || fail "/.well-known/security.txt: no Contact field"
	exp=$(sed -nE 's/^[Ee]xpires:[[:space:]]*([^[:space:]]+).*/\1/p' <<<"$body" | head -1)
	if [[ -z $exp ]]; then fail "/.well-known/security.txt: no Expires field"
	elif d=$(days_left "$exp"); then
		(( d >= SECURITY_TXT_DAYS )) || fail "/.well-known/security.txt: Expires in $d days ($exp), renew it"
	else fail "/.well-known/security.txt: Expires is not a date: $exp"; fi
fi

if (( ${#problems[@]} == 0 )); then
	echo "ok: $BASE_URL channels, registry, go-import and security.txt healthy"
	[[ -n ${HEALTHCHECK_URL:-} ]] && curl -fsS -m 10 -o /dev/null "$HEALTHCHECK_URL" || true
	exit 0
fi

msg="hatamoto monitor: ${#problems[@]} problem(s) at $BASE_URL"
printf 'FAIL: %s\n' "${problems[@]}" >&2
if [[ -n ${ALERT_WEBHOOK_URL:-} ]]; then
	text=$(printf '%s\n' "$msg" "${problems[@]}")
	jq -n --arg text "$text" '{text: $text}' \
		| curl -fsS -m 10 -o /dev/null -H 'Content-Type: application/json' -d @- "$ALERT_WEBHOOK_URL" \
		|| echo "WARN: could not post the alert webhook" >&2
fi
[[ -n ${HEALTHCHECK_URL:-} ]] && curl -fsS -m 10 -o /dev/null "${HEALTHCHECK_URL%/}/fail" || true
exit 1
