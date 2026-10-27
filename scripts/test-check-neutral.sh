#!/usr/bin/env bash
# test-check-neutral.sh - offline checks of the locale rule in scripts/check-neutral.sh.
# Run by `make check`.
#
#   * the word locale is allowed in backend Go code, SQL and docs (content locale);
#   * the retired v1-09 names, the brace-locale token, locale code path segments and
#     quoted country codes still fail, and the report names file and line.
# Banned strings are built at run time so this file does not trip check-neutral itself.
set -euo pipefail
ROOT=$(git rev-parse --show-toplevel)
fail() { echo "FAIL: $*" >&2; exit 1; }
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
R="$T/repo"
mkdir -p "$R/scripts" "$R/backend/internal/entity" "$R/docs"
git -C "$R" init -q
cp "$ROOT/scripts/check-neutral.sh" "$R/scripts/check-neutral.sh"
gr() { git -C "$R" -c user.name=t -c user.email=t@example.org "$@"; }
neutral() { (cd "$R" && bash scripts/check-neutral.sh 2>&1); }

# --- allowed: the word locale and ordinary text near country-code letters ---
cat > "$R/backend/internal/entity/model.go" <<'G'
package entity

type Entity struct {
	Locale string `json:"locale"`
}
G
printf 'ALTER TABLE t ADD COLUMN locale TEXT NOT NULL;\n' > "$R/docs/schema.sql"
printf 'See /blog/us-visa and the locale field.\n' > "$R/docs/page.md"
gr add -A && gr commit -qm base
o=$(neutral) || fail "check-neutral rejected allowed locale text: $o"
echo "ok   allowed: the word locale, a hyphenated slug"

# --- refused: each banned string must fail and be named ---
tok=$(printf '{%s}' locale)
old1=$(printf 'hatamoto_%s' locales)
old2=$(printf 'locale_%s' code)
old3=$(printf 'locale_%s' scope)
old4=$(printf 'enabled_%s' locales)
old5=$(printf 'hatamoto_entity_locale_%s' overrides)
seg=$(printf '"/%s/blog"' au)
quoted=$(printf "'%s'" us)
for bad in "$tok" "$old1" "$old2" "$old3" "$old4" "$old5" "$seg" "$quoted"; do
  printf 'x %s y\n' "$bad" > "$R/docs/leak.md"
  gr add -A && gr commit -qm "plant" --allow-empty
  o=$(neutral) && fail "check-neutral passed a file with $bad"
  grep -q 'docs/leak.md:1:' <<<"$o" || fail "check-neutral did not name docs/leak.md:1 for $bad: $o"
done
echo "ok   refused: retired names, brace token, path segment, quoted code"
echo "check-neutral tests ok"
