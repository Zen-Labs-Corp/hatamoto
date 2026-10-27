#!/usr/bin/env bash
# check-neutral.sh - core must carry no site-specific, fork-specific or private
# names. Run by `make check`.
#
# It scans exactly the files that ship: every tracked file that is NOT marked
# export-ignore in .gitattributes (the set `git archive` and
# scripts/public-snapshot.sh export). Working notes, plans and design mockups
# are export-ignored, so they are never scanned and never shipped.
#
# Banned (case-insensitive): generic fork words, free-mail addresses,
# home-directory paths, the IDE workspace name "GolandProjects", the systemd
# home specifier "%h/" and the notes path ".pi/plans" (below), plus the names of the agency site, its private
# repos, hosts and people from .neutral-private (export-ignored, so the public
# tree never names them). A few historical hits are allowed, each pinned to
# one file AND one line pattern; add no more without a reason.
#
# Locale rule (v1-09: one site, one language). Shipped files may not contain
#   * a locale/country code as a path or quoted literal: /au, /au/..., "au", 'us'
#     (au us uk nz sg ae cn at de ca). The patterns need a path start or a quote on
#     both sides, so ordinary words and hyphenated names ("Cafe au Lait", "x-uk-cta2",
#     "audit", "/blog/us-visa") never match;
#   * the retired {locale} token or the dropped schema names (hatamoto_locales,
#     locale_code, locale_scope, enabled_locales, hatamoto_entity_locale_overrides);
#   The word "locale" itself is allowed (content locale, a BCP 47 column and field).
# Exempt from the locale rule (each is applied history, a fixture or a test of the old URLs):
#   backend/migrations/                  applied history, the prod-like seed and the tests that pin the migration
#   backend/internal/entity/testdata/    sanitiser HTML corpora (real-world markup)
#   backend/web/vendor/                  third-party minified code (hash-pinned, never edited)
#   backend/internal/server/retired_prefix_integration_test.go   asserts the old /xx/... URLs redirect
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# shipped_files: tracked files minus export-ignore, NUL-separated.
shipped_files() {
  git ls-files -z \
    | git check-attr -z --stdin export-ignore \
    | awk -v RS='\0' -v ORS='\0' 'NR%3==1{p=$0} NR%3==0 && $0!="set"{print p}'
}

# Generic banned terms (case-insensitive). The names of the private site, its
# repos, hosts and people are NOT written here, since this script ships: they
# live in .neutral-private, which is export-ignored (never in the public tree)
# and read when present.
BANNED='overlay|core_ref|@(gmail|googlemail|yahoo|hotmail|outlook|icloud|proton(mail)?)\.|GolandProjects|%h/|\.pi/plans'
# Home-directory paths are matched case-sensitively ("Home/End" is a key name).
HOME_RE='(^|[^[:alnum:]_])/(home|Users)/[a-z][a-z0-9_-]*/'
# Allowed hits: "file<TAB>ERE that the matching line must contain". Only these
# exact (file, line) pairs pass; any other banned word in the same file fails.
ALLOW=$(printf '%s\t%s\n' \
  'scripts/check-neutral.sh'                 '.' \
  'scripts/test-public-snapshot.sh'          'refuse "free-mail" .*someone@gmail\.com' \
  'scripts/test-public-snapshot.sh'          '\$R/\.pi/plans|\.pi/plans/plan\.md' \
  'backend/migrations/005_retention_and_cleanup.sql' '^-- never edited\. See \.pi/plans/2026-09-29-db-retention/' \
  'deploy/channels/README.md'                'ExecStart=%h/\.local/bin/hatamoto-check-channel-freshness --min-days 99' \
  'deploy/channels/freshness.service'        '^(EnvironmentFile=-%h/\.config/hatamoto/freshness\.env|Environment=PATH=%h/\.local/bin:|ExecStart=%h/\.local/bin/hatamoto-check-channel-freshness )' \
  'docs/CHANGELOG_RELEASE_HOOKS.md'          '^(WorkingDirectory=%h/src/hatamoto/backend|ExecStartPre=/usr/bin/git -C %h/src/hatamoto fetch )')
# .neutral-private: "ban <ERE>" adds banned terms; "allow <file> <ERE>" adds an
# allowed (file, line) pair; blank lines and # comments are skipped.
PRIVATE_BANNED=""
# HATAMOTO_NEUTRAL_NO_PRIVATE=1 only turns the dangling-link exit 2 into a notice (used by
# replay-branch.sh --no-private-check). A readable .neutral-private is always read. Refused in CI.
if [[ -n "${HATAMOTO_NEUTRAL_NO_PRIVATE:-}" && -n "${CI:-}" ]]; then
  echo "check-neutral: HATAMOTO_NEUTRAL_NO_PRIVATE is not allowed when CI is set" >&2
  exit 2
fi
if [[ -L .neutral-private && ! -e .neutral-private ]]; then
  if [[ "${HATAMOTO_NEUTRAL_NO_PRIVATE:-}" == 1 ]]; then
    echo "check-neutral: HATAMOTO_NEUTRAL_NO_PRIVATE=1: .neutral-private is a dangling link and is NOT read" >&2
  else
    echo "check-neutral: .neutral-private is a dangling link (run .pi/bin/notes-link.sh)" >&2
    exit 2
  fi
fi
if [[ -f .neutral-private ]]; then
  while read -r kind rest; do
    case "$kind" in
      ban) PRIVATE_BANNED+="${PRIVATE_BANNED:+|}$rest" ;;
      allow) ALLOW+=$'\n'"${rest%% *}"$'\t'"${rest#* }" ;;
      ''|'#'*) ;;
      *) echo "check-neutral: bad line in .neutral-private: $kind" >&2; exit 2 ;;
    esac
  done < .neutral-private
  BANNED+="|$PRIVATE_BANNED"
fi
fail=0

hits=$(shipped_files \
  | grep -zvE '^(backend/web/vendor/|scripts/admin-qa/vendor/)' \
  | xargs -0 grep -nIE -i -e "$BANNED" 2>/dev/null || true)
hits+=$'\n'$(shipped_files | grep -zvE '^(backend/web/vendor/|scripts/admin-qa/vendor/)' \
  | xargs -0 grep -nIE -e "$HOME_RE" 2>/dev/null || true)
bad=""
while IFS= read -r h; do
  [[ -n "$h" ]] || continue
  f=${h%%:*}; line=${h#*:}; line=${line#*:}
  ok=0
  while IFS=$'\t' read -r af are; do
    [[ "$f" == "$af" ]] && grep -qE -- "$are" <<<"$line" && { ok=1; break; }
  done <<<"$ALLOW"
  ((ok)) || bad+="$h"$'\n'
done <<<"$hits"
# Vendored third-party code: only the site/host names, not the generic words.
vhits=$(shipped_files | grep -zE '^(backend/web/vendor/|scripts/admin-qa/vendor/)' \
  | xargs -0 grep -liIE -e "${PRIVATE_BANNED:-x^}" -e "$HOME_RE" 2>/dev/null || true)
[[ -z "$vhits" ]] || bad+="vendored files name a private site or path: $vhits"$'\n'
if [[ -n "$bad" ]]; then
  echo "FAIL neutral: site-specific or private names in shipped files:"
  printf '%s' "$bad"
  fail=1
else
  echo "ok   neutral: no banned names in the shipped files"
fi

# The canvas editor and render engine stay agency/site neutral.
dirs=()
for d in backend/internal/render; do
  [[ -d "$d" ]] && dirs+=("$d")
done
canvas_hits=""
if ((${#dirs[@]})); then
  canvas_hits=$(grep -rniIE 'awards_policy|lead_form|integrated_agency|methodology|service_checklist|Get Free Audit' "${dirs[@]}" \
    --exclude-dir=__tests__ --exclude-dir=testdata --exclude='*.spec.*' --exclude='*.test.*' || true)
fi
if [[ -n "$canvas_hits" ]]; then
  echo "FAIL neutral: site-specific section names in canvas/render:"
  printf '%s\n' "$canvas_hits"
  fail=1
else
  echo "ok   neutral: canvas and render are site neutral"
fi

# --- locale literals (see the header for the rules and exemptions) ---
CODES='au|us|uk|nz|sg|ae|cn|at|de|ca'
locale_files() {
  shipped_files \
    | grep -zvE '^(backend/migrations/|backend/web/vendor/|backend/internal/entity/testdata/)' \
    | grep -zvE '^(scripts/check-neutral\.sh|backend/internal/server/retired_prefix_integration_test\.go)$'
}
locale_hits=$(locale_files | xargs -0 grep -nIE \
    -e "(^|[^[:alnum:]_.-])/($CODES)(/|[\"'\`)[:space:]?]|\$)" \
    -e "[\"'\`]($CODES)[\"'\`]" \
    -e '\{locale\}|hatamoto_locales|hatamoto_entity_locale_overrides|locale_code|locale_scope|enabled_locales' 2>/dev/null || true)
if [[ -n "$locale_hits" ]]; then
  echo "FAIL neutral: retired v1-09 locale names, the brace-locale token or locale path segments:"
  printf '%s\n' "$locale_hits" | sed '/^$/d'
  fail=1
else
  echo "ok   neutral: no locale literals outside the exempt paths"
fi

# --- admin templates stay eval-free (strict CSP, htmx allowEval:false) ---
# htmx evaluates hx-on:*, js: values (hx-vals/hx-vars/hx-headers) and trigger
# filters (hx-trigger="click[ctrlKey]") with eval; the CSP forbids eval, so
# these features would silently do nothing. Behaviour lives in js/ modules.
# HM_ADMIN_TEMPLATES overrides the scanned directory (the failing-fixture test).
tpl_dir="${HM_ADMIN_TEMPLATES:-backend/web/admin}"
htmx_hits=""
if [[ -d "$tpl_dir" ]]; then
  htmx_hits=$(grep -rnIE --include='*.html' \
      -e '[[:space:]]hx-on([:-]|=)' \
      -e 'hx-(vals|vars|headers)=["'"'"'][[:space:]]*(js|javascript):' \
      -e 'hx-trigger=("[^"]*\[|'"'"'[^'"'"']*\[)' \
      "$tpl_dir" || true)
fi
if [[ -n "$htmx_hits" ]]; then
  echo "FAIL neutral: admin templates use hx-on, js: values or hx-trigger filters (eval, forbidden by the CSP):"
  printf '%s\n' "$htmx_hits"
  fail=1
else
  echo "ok   neutral: admin templates have no hx-on, js: values or trigger filters"
fi

((fail == 0)) || { echo "check-neutral: FAILED"; exit 1; }
echo "check-neutral: PASSED"
