#!/usr/bin/env bash
# apply-caddy.sh - splice deploy/caddy/hatamoto.dev.caddy into a Caddyfile.
#
# Replaces ONLY the site blocks whose address is exactly `hatamoto.dev` or
# `www.hatamoto.dev` (the first one found is replaced in place with the tracked
# blocks, the other is dropped; if neither exists the blocks are appended).
# Every other line of the file is kept byte for byte.
#
# Blocks are found by a brace-depth parser that follows the Caddyfile lexer:
# `{` and `}` only count as standalone tokens, so placeholders such as {uri},
# braces inside "quoted"/`raw` strings, comments (`} # note`), heredocs, CRLF
# line endings, nested matchers/handle blocks, snippets `(name) {`, the global
# options block and `import` lines are all handled. The script refuses (and
# changes nothing) when a hatamoto address shares a line with other addresses,
# when something follows a block's closing `}`, or when braces do not balance.
#
# Safety nets, in order:
#   1. `caddy validate` on the spliced file (written next to the target so
#      relative `import` globs resolve the same way).
#   2. `caddy adapt` of the file before and after, with the hatamoto hosts
#      removed, must be identical JSON: no other site may change.
#   3. The old file is backed up (<Caddyfile>.bak keeps the first original; each
#      change also writes <Caddyfile>.bak.<timestamp>) and restored + reloaded if
#      the reload fails.
#
# Usage:  sudo deploy/apply-caddy.sh [--dry-run] [/etc/caddy/Caddyfile]
# Flags:  --dry-run  validate and print the diff, change nothing
# Env:    CADDY_BIN  caddy binary (default: caddy)
#         DRY_RUN=1  same as --dry-run (sudo drops the environment: write
#                    `sudo DRY_RUN=1 deploy/apply-caddy.sh`, or use the flag)
# Needs:  caddy, jq, awk
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
src=${CADDY_SNIPPET:-$here/caddy/hatamoto.dev.caddy}
dry=${DRY_RUN:-}
target=""
for a in "$@"; do
	case $a in
	--dry-run) dry=1 ;;
	-h | --help) sed -n '2,/^set /{/^set /!p}' "$0"; exit 0 ;;
	-*) echo "apply-caddy: unknown flag: $a" >&2; exit 64 ;;
	*)
		[[ -z $target ]] || { echo "usage: apply-caddy.sh [--dry-run] [Caddyfile]" >&2; exit 64; }
		target=$a
		;;
	esac
done
target=${target:-/etc/caddy/Caddyfile}
caddy=${CADDY_BIN:-caddy}

[[ -f $src ]] || { echo "missing $src" >&2; exit 1; }
[[ -f $target ]] || { echo "missing $target" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

tmp=$(mktemp "$(dirname "$target")/.Caddyfile.XXXXXX")
snippet=$(mktemp)
trap 'rm -f "$tmp" "$snippet"' EXIT

# The tracked file starts with a comment header; only the site blocks are spliced.
awk '/^(www\.)?hatamoto\.dev[ \t]*\{/ {on=1} on {print}' "$src" >"$snippet"
[[ -s $snippet ]] || { echo "no site blocks in $src" >&2; exit 1; }

awk -v snippet="$snippet" '
function die(msg) { print "apply-caddy: line " NR ": " msg > "/dev/stderr"; failed = 1; exit 1 }

# token(): one lexer token. quoted tokens never act as braces.
function token(t, quoted) {
	if (!quoted && t == "{") {
		if (depth == 0 && !inhdr) { inhdr = 1; hdrstart = NR; naddr = 0; hdrfirst = "{" }
		depth++
		return
	}
	if (!quoted && t == "}") {
		if (depth == 0) die("unbalanced }")
		depth--
		if (depth == 0) { finish(NR); closedat = NR }
		return
	}
	if (depth == 0) {
		if (closedat == NR) die("unexpected text after the closing }")
		if (!inhdr) { inhdr = 1; hdrstart = NR; naddr = 0; hdrfirst = t }
		addr[++naddr] = t
		lastaddr = t
	}
}

# finish(): a top-level entry just ended on line endl.
function finish(endl,   i, n, a, k, hit, names) {
	inhdr = 0
	if (hdrfirst == "{" || hdrfirst ~ /^\(/) { return }   # global options / snippet
	hit = 0; n = 0
	for (i = 1; i <= naddr; i++) {
		k = addr[i]
		gsub(/,+$/, "", k); gsub(/^,+/, "", k)
		if (k == "") continue
		n++
		if (tolower(k) == "hatamoto.dev" || tolower(k) == "www.hatamoto.dev") hit++
		else if (index(k, ",") && k ~ /(^|,)(www\.)?hatamoto\.dev(,|$)/) hit++
	}
	if (hit == 0) return
	if (n != 1 || hit != 1) die("hatamoto.dev shares an address line with other addresses; refusing")
	nblocks++
	bstart[nblocks] = hdrstart; bend[nblocks] = endl
}

{
	raw[NR] = $0
	line = $0
	if (NR == 1 && line ~ /\r$/) crlf = 1
	sub(/\r$/, "", line)

	if (heredoc != "") {
		t = line; gsub(/^[ \t]+|[ \t]+$/, "", t)
		if (t == heredoc) heredoc = ""
		next
	}

	i = 1; n = length(line)
	while (i <= n) {
		if (q != "") {              # inside a quoted string from an earlier line
			j = i
			while (j <= n) {
				c = substr(line, j, 1)
				if (q == "\"" && c == "\\") { j += 2; continue }
				if (c == q) break
				j++
			}
			if (j > n) { i = n + 1; break }
			q = ""; i = j + 1
			if (qtoken) { token("q", 1); qtoken = 0 }
			continue
		}
		c = substr(line, i, 1)
		if (c == " " || c == "\t") { i++; continue }
		if (c == "#") break
		if (c == "\"" || c == "`") {
			q = c; qtoken = 1; i++
			continue
		}
		j = i
		while (j <= n && substr(line, j, 1) !~ /[ \t]/) j++
		t = substr(line, i, j - i)
		i = j
		if (t ~ /^<<[A-Za-z0-9_]+$/ && substr(line, i) ~ /^[ \t]*$/) {
			heredoc = substr(t, 3); token("heredoc", 1); break
		}
		token(t, 0)
	}

	# End of line at the top level without a `{`: a braceless entry.
	if (q == "" && heredoc == "" && depth == 0 && inhdr) {
		if (lastaddr ~ /,$/) next                   # address list continues
		if (hdrfirst == "import") { inhdr = 0; next }
		die("site address without a { block: " hdrfirst)
	}
}

END {
	if (failed) exit 1
	if (depth != 0 || q != "" || heredoc != "") {
		print "apply-caddy: unbalanced braces/quotes at end of file" > "/dev/stderr"; exit 1
	}
	nl = NR
	eol = crlf ? "\r" : ""
	b = 1
	for (ln = 1; ln <= nl; ln++) {
		if (b <= nblocks && ln == bstart[b]) {
			if (!done) {
				flush()
				while ((getline sl < snippet) > 0) print sl eol
				done = 1
			} else {
				nblank = 0                      # blank lines only separated the dropped block
			}
			ln = bend[b]; b++
			continue
		}
		if (raw[ln] ~ /^[ \t\r]*$/) { nblank++; blanks[nblank] = raw[ln]; continue }
		flush()
		print raw[ln]
	}
	if (!done) {
		if (nl > 0) print eol
		while ((getline sl < snippet) > 0) print sl eol
	}
	flush()
}
function flush(   i) { for (i = 1; i <= nblank; i++) print blanks[i]; nblank = 0 }
' "$target" >"$tmp"

"$caddy" validate --config "$tmp" --adapter caddyfile >/dev/null 2>&1 ||
	{ "$caddy" validate --config "$tmp" --adapter caddyfile >&2; exit 1; }

# Independent check: caddy itself must see every other site unchanged.
# Fails closed: if adapting or normalising fails, or yields nothing, the
# comparison would pass vacuously, so abort instead.
others() {
	local adapted out
	adapted=$("$caddy" adapt --config "$1" --adapter caddyfile 2>/dev/null) || return 1
	out=$(jq -S --arg cfg "$1" -f "$here/caddy-others.jq" <<<"$adapted") || return 1
	[[ -n $out ]] || return 1
	printf '%s\n' "$out"
}
before=$(others "$target") && after=$(others "$tmp") ||
	{ echo "apply-caddy: refusing: could not normalise the adapted config to compare other sites" >&2; exit 1; }
if [[ $before != "$after" ]]; then
	echo "apply-caddy: refusing: the splice would change sites other than hatamoto.dev:" >&2
	diff <(echo "$before") <(echo "$after") >&2 || true
	exit 1
fi

if cmp -s "$tmp" "$target"; then
	echo "$target already up to date"
	exit 0
fi
diff -u "$target" "$tmp" || true
if [[ $dry == 1 ]]; then
	echo "DRY_RUN: $target left untouched"
	exit 0
fi

backup="$target.bak.$(date +%Y%m%d%H%M%S)"
cp -p "$target" "$backup"
[[ -e $target.bak ]] || cp -p "$target" "$target.bak"
cat "$tmp" >"$target"
if ! "$caddy" reload --config "$target" --adapter caddyfile; then
	cat "$backup" >"$target"
	if ! "$caddy" reload --config "$target" --adapter caddyfile; then
		echo "reload failed, and reloading the restored $target failed too" >&2
		exit 1
	fi
	echo "reload failed; $target restored from $backup and reloaded" >&2
	exit 1
fi
echo "applied to $target (previous version: $backup)"
