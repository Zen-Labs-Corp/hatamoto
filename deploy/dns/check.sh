#!/usr/bin/env bash
# Read-only DNS check for hatamoto.dev (needs dig). Prints each record and exits
# non-zero when the mail records (Google Workspace: MX, SPF, DKIM, DMARC) or the CAA
# records are wrong, or when any MX other than a Google Workspace host exists. DNSSEC is
# reported only: the DS record is added at the registrar after Cloudflare signs the zone,
# so a missing DS prints "pending", not FAIL. Run before and after applying
# deploy/dns/hatamoto.dev.zone-additions.txt. Tested by deploy/dns/test-check.sh.
# Usage: deploy/dns/check.sh [domain]   (resolver: RESOLVER=1.1.1.1)
set -uo pipefail
d=${1:-hatamoto.dev}; r=${RESOLVER:-1.1.1.1}
q() { dig +short +time=5 +tries=2 "@$r" "$@"; }
bad=0
need() { # label value-regex output
	if grep -Eq "$2" <<<"$3"; then echo "ok   $1: $(tr '\n' ' ' <<<"$3")"; else echo "FAIL $1: missing (got: ${3:-nothing})"; bad=1; fi
}
# MX: only Google Workspace hosts may receive mail. Any other MX (a Cloudflare route*, a
# leftover _dc-mx pointing at the origin) fails.
gmx='^[0-9]+ (aspmx\.l|alt[1-4]\.aspmx\.l|smtp)\.google\.com\.$'
mx=$(q MX "$d")
need MX "$gmx" "$mx"
other=$(grep -Evi "$gmx" <<<"$mx" | grep . || true)
[[ -z $other ]] || { echo "FAIL MX: not Google Workspace (delete it): $(tr '\n' ' ' <<<"$other")"; bad=1; }
spf=$(q TXT "$d" | grep -i 'v=spf1')
need SPF '^"v=spf1 .*include:_spf\.google\.com .*[~-]all"$' "$spf"
[[ $(grep -c . <<<"$spf") -le 1 ]] || { echo "FAIL SPF: more than one SPF record"; bad=1; }
need DKIM 'v=DKIM1' "$(q TXT "google._domainkey.$d")"
dmarc=$(q TXT "_dmarc.$d")
need DMARC 'v=DMARC1; p=(none|quarantine|reject)' "$dmarc"
grep -q 'p=none' <<<"$dmarc" && echo "note DMARC is p=none (monitoring): move to quarantine after 2 weeks of clean reports"
need "DMARC rua" 'rua=mailto:' "$dmarc"
# Cloudflare adds its own CAs for its edge certificates, so extra issue lines are fine.
caa=$(q CAA "$d")
need CAA 'issue "letsencrypt.org"' "$caa"
need "CAA iodef" 'iodef "mailto:[^"]+@' "$caa"
ds=$(q DS "$d")
if grep -Eq '^[0-9]+ [0-9]+ [0-9]+ [0-9A-Fa-f]+$' <<<"$ds"; then echo "ok   DS: $(tr '\n' ' ' <<<"$ds")"
else echo "note DS: pending (enable DNSSEC at Cloudflare, then add its DS at the registrar)"; fi
exit $bad
