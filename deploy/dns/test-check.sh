#!/usr/bin/env bash
# Tests deploy/dns/check.sh against fixture zones, with a fake `dig` on PATH.
# Fixture files in $FIX are named <TYPE>.<name> and hold dig +short output.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/dns-check.XXXXXX"); trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
cat >"$tmp/bin/dig" <<'F'
#!/usr/bin/env bash
n=${@: -1}; t=${@: -2:1}
cat "$FIX/$t.$n" 2>/dev/null; exit 0
F
chmod +x "$tmp/bin/dig"
fails=0

google() { # populate $FIX with the live Google Workspace layout
	FIX=$1; mkdir -p "$FIX"
	printf '%s\n' '1 aspmx.l.google.com.' '5 alt1.aspmx.l.google.com.' '5 alt2.aspmx.l.google.com.' \
		'10 alt3.aspmx.l.google.com.' '10 alt4.aspmx.l.google.com.' >"$FIX/MX.x.dev"
	echo '"v=spf1 include:_spf.google.com ~all"' >"$FIX/TXT.x.dev"
	echo '"v=DKIM1; k=rsa; p=AAAA" "BBBB"' >"$FIX/TXT.google._domainkey.x.dev"
	echo '"v=DMARC1; p=none; rua=mailto:dmarc@x.dev; fo=1"' >"$FIX/TXT._dmarc.x.dev"
	printf '%s\n' '0 issue "letsencrypt.org"' '0 issuewild ";"' '0 iodef "mailto:security@x.dev"' \
		'0 issue "pki.goog; cansignhttpexchanges=yes"' >"$FIX/CAA.x.dev"
}
run() { # name want-exit want-output-regex  (fixture already in $FIX)
	local out rc; out=$(FIX=$FIX PATH="$tmp/bin:$PATH" bash "$here/check.sh" x.dev 2>&1); rc=$?
	if [[ $rc -eq $2 ]] && grep -Eq "$3" <<<"$out"; then echo "ok   $1"
	else echo "FAIL $1 (exit $rc, want $2, want /$3/)"; echo "$out" | sed 's/^/     /'; fails=1; fi
}
case_() { google "$tmp/$1"; FIX=$tmp/$1; }

case_ good;     run "Google layout, DS pending passes" 0 'note DS: pending'
echo '8 13 2 ABCDEF0123' >"$tmp/good/DS.x.dev"; run "DS present reports ok" 0 'ok   DS'
echo '"v=DMARC1; p=quarantine; rua=mailto:d@x.dev"' >"$tmp/good/TXT._dmarc.x.dev"; run "DMARC quarantine passes" 0 'ok   DMARC:'
echo '"v=spf1 include:_spf.google.com -all"' >"$tmp/good/TXT.x.dev"; run "SPF -all passes" 0 'ok   SPF'

case_ cf;       echo '1 route1.mx.cloudflare.net.' >"$FIX/MX.x.dev"; run "Cloudflare MX fails" 1 'FAIL MX'
case_ extra;    echo '10 _dc-mx.abc.x.dev.' >>"$FIX/MX.x.dev"; run "leftover origin MX fails" 1 'FAIL MX: not Google'
case_ spf1;     echo '"v=spf1 include:_spf.mx.cloudflare.net -all"' >"$FIX/TXT.x.dev"; run "SPF without Google fails" 1 'FAIL SPF'
case_ spf2;     echo '"v=spf1 include:_spf.google.com +all"' >"$FIX/TXT.x.dev"; run "SPF +all fails" 1 'FAIL SPF'
case_ nodkim;   rm "$FIX/TXT.google._domainkey.x.dev"; run "missing DKIM fails" 1 'FAIL DKIM'
case_ nodmarc;  rm "$FIX/TXT._dmarc.x.dev"; run "missing DMARC fails" 1 'FAIL DMARC'
case_ nocaa;    echo '0 issue "pki.goog"' >"$FIX/CAA.x.dev"; run "CAA without letsencrypt fails" 1 'FAIL CAA'
case_ noiodef;  grep -v iodef "$FIX/CAA.x.dev" >"$FIX/c" && mv "$FIX/c" "$FIX/CAA.x.dev"; run "CAA without iodef fails" 1 'FAIL CAA iodef'
case_ empty;    rm "$FIX"/*; run "empty zone fails" 1 'FAIL MX'
exit $fails
