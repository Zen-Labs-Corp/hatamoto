#!/usr/bin/env bash
# Checks deploy/well-known/security.txt against RFC 9116: required fields,
# single Expires in the future (and under a year), https Canonical, mailto/https
# Contact. Also checks a few broken inputs are rejected.
# Run: deploy/test-security-txt.sh [FILE]
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# validate FILE -> prints problems, returns non-zero if any.
validate() {
	local f=$1 bad=0
	field() { grep -iE "^$1:[[:space:]]" "$f" | sed -E "s/^[^:]+:[[:space:]]*//" | tr -d '\r'; }
	err() { echo "  $*"; bad=1; }
	local n
	grep -qE '^Contact:[[:space:]]+(mailto:[^@[:space:]]+@[^[:space:]]+|https://[^[:space:]]+)$' "$f" || err "Contact missing or not mailto:/https:"
	n=$(grep -ciE '^Expires:' "$f" || true); [[ $n == 1 ]] || err "Expires must appear exactly once (found $n)"
	local exp; exp=$(field Expires)
	if [[ $exp =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$ ]]; then
		local e now; e=$(date -u -d "$exp" +%s 2>/dev/null) || e=0; now=$(date -u +%s)
		(( e > now )) || err "Expires is in the past or unparsable: $exp"
		(( e < now + 366*86400 )) || err "Expires is more than a year away: $exp"
	else err "Expires is not an ISO 8601 date-time: '$exp'"; fi
	n=$(grep -ciE '^Canonical:' "$f" || true); (( n >= 1 )) || err "Canonical missing"
	grep -iE '^Canonical:' "$f" | grep -qvE '^Canonical:[[:space:]]+https://' && err "Canonical must be https"
	grep -qE '^Preferred-Languages:[[:space:]]+[a-z]{2}' "$f" || err "Preferred-Languages missing"
	grep -qE '^Policy:[[:space:]]+https://' "$f" || err "Policy missing or not https"
	grep -qE '^Canonical:[[:space:]]+https://hatamoto\.dev/\.well-known/security\.txt$' "$f" || err "Canonical is not hatamoto.dev's own URL"
	return $bad
}

target=${1:-$here/well-known/security.txt}
validate "$target" || { echo "FAIL: $target"; exit 1; }

w=$(mktemp -d); trap 'rm -rf "$w"' EXIT
mk() { sed -E "$1" "$target" >"$w/$2"; }
mk 's/^Expires:.*/Expires: 2020-01-01T00:00:00Z/' expired
mk 's/^Expires:.*/Expires: soon/' badexp
mk '/^Contact:/d' nocontact
mk 's/^Canonical:.*/Canonical: http:\/\/hatamoto.dev\/x/' httpcanon
for c in expired badexp nocontact httpcanon; do
	if validate "$w/$c" >/dev/null; then echo "FAIL: broken fixture '$c' accepted"; exit 1; fi
done
echo "ok: security.txt valid; 4 broken variants rejected"
