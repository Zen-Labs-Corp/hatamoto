#!/usr/bin/env bash
# demo-seed.sh - seed the content of the public demo (HATAMOTO_DEMO=1) into a
# FRESH, local database that a Hatamoto server is serving. Run it once to build
# the template database the hourly reset copies (deploy/demo/reset.sh).
#
# What it creates: the public owner login, the finished setup wizard with starter
# content (Home, About and Contact pages, two posts, a contact form, header and
# footer menus, the built-in default theme), a few more pages and posts, a
# redirect, three form submissions and, when .hmp files are given, first-party
# plugins. The login is shown on the sign-in page only while HATAMOTO_DEMO=1.
#
# Environment (nothing is built in; the login comes from you):
#   DATABASE_URL                    the database the server uses (required; local host only)
#   HATAMOTO_DEMO_LOGIN_EMAIL       the owner's email (required)
#   HATAMOTO_DEMO_LOGIN_PASSWORD    the owner's password, at least 12 characters (required)
#   BASE                            the server's address, default http://127.0.0.1:8090
#   HATAMOTO_BIN                    the hatamoto binary, default "hatamoto"
#   DEMO_SITE_NAME                  default "Hatamoto demo"
#   DEMO_PLUGINS                    space-separated .hmp files of first-party plugins; each is
#                                   installed with every permission it asks for. Plugins need
#                                   a signed package, which only the first-party key can make,
#                                   so without this the step is skipped with a note.
# Usage: DATABASE_URL=... HATAMOTO_DEMO_LOGIN_EMAIL=... HATAMOTO_DEMO_LOGIN_PASSWORD=... scripts/demo-seed.sh
set -u
BIN=${HATAMOTO_BIN:-hatamoto}
BASE=${BASE:-http://127.0.0.1:8090}
SITE=${DEMO_SITE_NAME:-Hatamoto demo}
die() { echo "demo-seed: $*" >&2; exit 2; }
: "${DATABASE_URL:?set DATABASE_URL to the demo database}"
: "${HATAMOTO_DEMO_LOGIN_EMAIL:?set HATAMOTO_DEMO_LOGIN_EMAIL}"
: "${HATAMOTO_DEMO_LOGIN_PASSWORD:?set HATAMOTO_DEMO_LOGIN_PASSWORD}"
[ "${#HATAMOTO_DEMO_LOGIN_PASSWORD}" -ge 12 ] || die "HATAMOTO_DEMO_LOGIN_PASSWORD must be at least 12 characters"
EMAIL=$HATAMOTO_DEMO_LOGIN_EMAIL
PASS=$HATAMOTO_DEMO_LOGIN_PASSWORD

# The seed adds an owner with a public password: only ever to a database on this machine.
host=${DATABASE_URL#*://}; host=${host#*@}; host=${host%%[/?]*}
case "$host" in
  localhost|localhost:*|127.0.0.1|127.0.0.1:*|"[::1]"|"[::1]:"*|"") ;;
  *) die "refusing to seed a non-local database (host '$host')" ;;
esac
case "$BASE" in
  http://127.0.0.1*|http://localhost*|http://\[::1\]*) ;;
  *) die "BASE must be a local address (http://127.0.0.1:PORT), got $BASE" ;;
esac
command -v "$BIN" >/dev/null 2>&1 || [ -x "$BIN" ] || die "hatamoto binary not found ($BIN); set HATAMOTO_BIN"
curl -fsS -o /dev/null "$BASE/health" || die "no Hatamoto server answers at $BASE/health; start one on this database first"

# An existing owner means this database was seeded already: stop, do not duplicate.
if printf '%s' "$PASS" | "$BIN" user create --email "$EMAIL" --name "Demo Owner" --role owner --password-stdin >/dev/null 2>"${TMPDIR:-/tmp}/demo-seed.$$.err"; then
  echo "owner created: $EMAIL"
else
  cat "${TMPDIR:-/tmp}/demo-seed.$$.err" >&2; rm -f "${TMPDIR:-/tmp}/demo-seed.$$.err"
  die "could not create the owner (is the database already seeded?)"
fi
rm -f "${TMPDIR:-/tmp}/demo-seed.$$.err"

jar=$(mktemp); png=$(mktemp --suffix=.png); trap 'rm -f "$jar" "$png"' EXIT
# The admin serves documents only to what a browser navigation sends, and a token that lives in the page, not in a cookie.
NAV=(-H 'Sec-Fetch-Site: same-origin' -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document')
# token PATH: GET a navigated admin page with the jar (it keeps hm_pre, then the session) and print its csrf meta token.
token() { curl -fsL "${NAV[@]}" -b "$jar" -c "$jar" "$BASE$1" | sed -n 's/.*<meta name="csrf-token" content="\([^"]*\)".*/\1/p' | head -1; }
# csrf: the token the next POST carries, read once per sign-in state (the sign-in screens are rate limited, so not per POST).
T=""
csrf() { printf '%s' "$T"; }
# signin EMAIL: a fresh browser signs in; the token comes from the sign-in page before, from the admin after (the session's).
signin() { : >"$jar"; T=$(token /admin/login); form /admin/login "email=$1" "password=$PASS" >/dev/null; T=$(token /admin); }
post() { local p=$1; shift; curl -s -o /dev/null -w "$p %{http_code}\n" -c "$jar" -b "$jar" "${NAV[@]}" -H "Origin: $BASE" -H "X-CSRF-Token: $(csrf)" "$@" "$BASE$p"; }
form() { local p=$1; shift; local a=(); for kv in "$@"; do a+=(--data-urlencode "$kv"); done; curl -s -o /dev/null -w "$p %{http_code}\n" -c "$jar" -b "$jar" "${NAV[@]}" -H "Origin: $BASE" --data-urlencode "_csrf=$(csrf)" "${a[@]}" "$BASE$p"; }
printf 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg==' | base64 -d > "$png"

# Sign in and complete the owner's profile: the admin sends anyone without a
# name and photo to "Complete your profile" and would hide every other screen.
signin "$EMAIL"
post /admin/media/upload -F "file=@$png;filename=demo-pixel.png;type=image/png" >/dev/null
photo=$(curl -s "${NAV[@]}" -b "$jar" "$BASE/admin/settings/media" | grep -oE '(src|href)="[^"]*demo-pixel[^"]*"' | head -1 | cut -d'"' -f2)
code=$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -b "$jar" "${NAV[@]}" -H "Origin: $BASE" --data-urlencode "_csrf=$(csrf)" \
  --data-urlencode "name=Demo Owner" --data-urlencode "photo=${photo:-/media/demo-pixel.png}" --data-urlencode "public=false" "$BASE/admin/profile/complete")
[ "$code" = 303 ] || die "completing the profile failed ($code): is HATAMOTO_DEMO_LOGIN_* the owner just created?"

# The wizard: site name and colour, then the starter content (pages, posts, form, menus).
form /admin/setup/site "name=$SITE" "url=" "language=en" "timezone=UTC"
form /admin/setup/theme "colour=#0060df"
form /admin/setup/content "content=starter"

# A few more pages and posts, published.
mk() { # mk SCHEMA SLUG NAME JSON
  "$BIN" entity create --schema "$1" --slug "$2" --name "$3" --attributes "$4" --publish --as "$EMAIL" >/dev/null 2>&1 \
    && echo "created $1 $2" || echo "demo-seed: could not create $1 $2" >&2
}
mk page features "Features" '{"description":"What the demo shows","content":"<p>Pages, posts, menus, forms, redirects and themes: sign in to /admin and try them. This demo resets every hour.</p>"}'
mk page privacy "Privacy" '{"description":"What this demo keeps","content":"<p>This is a public demo. Anything you type is visible to other visitors and is deleted at the next hourly reset. Do not enter personal data.</p>"}'
mk post what-to-try "What to try in the demo" '{"tags":["demo"],"excerpt":"Edit a page, publish a post, send the contact form.","content":"<p>Sign in with the login on the sign-in page, then edit a page, write a post or change the menu. Uploads of plugins and themes, email, webhooks and updates are switched off here.</p>"}'
mk post resets-every-hour "This demo resets every hour" '{"tags":["demo"],"excerpt":"Your changes do not last.","content":"<p>The database and uploaded files are replaced from a template once an hour, so experiment freely.</p>"}'
mk post a-third-post "Writing a post" '{"tags":["guide"],"excerpt":"The editor in one minute.","content":"<h2>Headings</h2><p>Use the editor to write, then publish.</p>"}'
"$BIN" redirect create --from /try --to /features --status 301 >/dev/null 2>&1 && echo "redirect /try"

# Three submissions on the starter contact form.
for who in Ada Grace Linus; do
  curl -s -o /dev/null -w "submit $who %{http_code}\n" -X POST -H 'Content-Type: application/json' \
    -d "{\"name\":\"$who\",\"email\":\"$who@example.invalid\",\"message\":\"Hello from $who\"}" "$BASE/api/v1/public/forms/contact/submit"
done

# First-party plugins, only from .hmp files you provide (the registry is not reached from here).
if [ -n "${DEMO_PLUGINS:-}" ]; then
  for f in $DEMO_PLUGINS; do
    if "$BIN" package install "$f" --grant all >/dev/null 2>&1; then echo "plugin installed: $f"; else echo "demo-seed: plugin $f was not installed" >&2; fi
  done
else
  echo "note: no DEMO_PLUGINS given, so no plugins were installed (first-party plugins are signed packages; point DEMO_PLUGINS at their .hmp files, or install them from Admin > Plugins > Browse while HATAMOTO_DEMO is off)"
fi

# Verify the result: the pages answer.
for p in / /about /contact /features /blog /blog/what-to-try; do
  s=$(curl -s -o /dev/null -w '%{http_code}' "$BASE$p"); echo "GET $p $s"
  [ "$s" = 200 ] || die "$p answered $s"
done
echo "demo-seed: done. Sign in at $BASE/admin/login as $EMAIL"
