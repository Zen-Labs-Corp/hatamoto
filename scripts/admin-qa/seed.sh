#!/usr/bin/env bash
# Seed a local instance for the admin QA harness. Afterwards the site has: an
# owner and an editor, the finished setup wizard with starter content (3 pages,
# header and footer menus, a contact form), 57 published posts, 1 media file, a
# header menu of 6 items (one nested), redirects (301, 302, 307) and 3 form
# submissions. Users, posts and redirects use the CLI; the wizard, media and
# menu have no CLI, so they go through the admin HTTP API with the owner
# session and the CSRF token from the page.
# The owner and the editor complete their profile (name and photo) first: the
# admin sends anyone without one to "Complete your profile", which would hide
# every other screen from the harness. A third user, newcomer@qa.test, is left
# incomplete so that page can be visited.
# Usage: QA_USE_DATABASE_URL=1 DATABASE_URL=...  BASE=http://127.0.0.1:8191 HATAMOTO_BIN=/tmp/hm-qa-bin scripts/admin-qa/seed.sh
# Redirect 301/302 use the CLI. Meant for a fresh database; reruns skip what exists but add posts and submissions again.
set -u
BIN=${HATAMOTO_BIN:-hatamoto}
BASE=${BASE:-http://127.0.0.1:8090}
PASS=${QA_PASSWORD:-qa-password-123}
OWNER=${QA_EMAIL:-owner@qa.test}
: "${DATABASE_URL:?set DATABASE_URL to the QA database}"
. "$(dirname "${BASH_SOURCE[0]}")/qa-env.sh"
qa_guard_database_url || exit 2

printf '%s' "$PASS" | "$BIN" user create --email "$OWNER" --name "QA Owner" --role owner --password-stdin 2>&1 | tail -1
EDITOR=${QA_EDITOR_EMAIL:-editor@qa.test}
NEWCOMER=${QA_NEWCOMER_EMAIL:-newcomer@qa.test}
printf '%s' "$PASS" | "$BIN" user create --email "$EDITOR" --name "QA Editor" --role editor --password-stdin 2>&1 | tail -1
printf '%s' "$PASS" | "$BIN" user create --email "$NEWCOMER" --name "QA Newcomer" --role owner --password-stdin 2>&1 | tail -1

jar=$(mktemp); trap 'rm -f "$jar" "${png:-}"' EXIT
# The admin serves documents only to what a browser navigation sends, and a token that lives in the page, not in a cookie.
NAV=(-H 'Sec-Fetch-Site: same-origin' -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document')
# token PATH: GET a navigated admin page with the jar (it keeps hm_pre, then the session) and print its csrf meta token.
token() { curl -fsL "${NAV[@]}" -b "$jar" -c "$jar" "$BASE$1" | sed -n 's/.*<meta name="csrf-token" content="\([^"]*\)".*/\1/p' | head -1; }
# csrf: the token the next POST carries, read once per sign-in state (the sign-in screens are rate limited, so not per POST).
T=""
csrf() { printf '%s' "$T"; }
# signin EMAIL: a fresh browser signs in; the token comes from the sign-in page before, from the admin after (the session's).
signin() { : >"$jar"; T=$(token /admin/login); form /admin/login "email=$1" "password=$PASS" >/dev/null; T=$(token /admin); }
# post PATH curl-args...: a multipart admin POST (the token goes in a header), prints "PATH status".
post() { local p=$1; shift; curl -s -o /dev/null -w "$p %{http_code}\n" -c "$jar" -b "$jar" "${NAV[@]}" -H "Origin: $BASE" -H "X-CSRF-Token: $(csrf)" "$@" "$BASE$p"; }
form() { local p=$1; shift; local a=(); for kv in "$@"; do a+=(--data-urlencode "$kv"); done; curl -s -o /dev/null -w "$p %{http_code}\n" -c "$jar" -b "$jar" "${NAV[@]}" -H "Origin: $BASE" --data-urlencode "_csrf=$(csrf)" "${a[@]}" "$BASE$p"; }

# One tiny PNG (1x1) for the media library and the profile photos.
png=$(mktemp --suffix=.png)
printf 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg==' | base64 -d > "$png"

# complete_profile EMAIL NAME: sign in, upload the pixel and read its address from the picker (both exempt from the profile
# gate; the media list is gated, which is why the old lookup found nothing and stored a 404 address) and complete the profile with it. Fails loudly: a gated seed would run
# every later step against the complete-profile page.
complete_profile() {
  local email=$1 name=$2 photo code
  signin "$email"
  post /admin/media/upload -F "file=@$png;filename=qa-pixel.png;type=image/png" >/dev/null
  photo=$(curl -s "${NAV[@]}" -b "$jar" "$BASE/admin/media/picker" | grep -oE 'data-hm-pick="[^"]*"' | head -1 | cut -d'"' -f2)
  : "${photo:=/media/qa-pixel.png}"
  code=$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -b "$jar" "${NAV[@]}" -H "Origin: $BASE" --data-urlencode "_csrf=$(csrf)" \
    --data-urlencode "name=$name" --data-urlencode "photo=$photo" --data-urlencode "public=false" "$BASE/admin/profile/complete")
  [ "$code" = 303 ] || { echo "seed: completing the profile of $email failed ($code)" >&2; exit 1; }
  echo "profile complete: $email"
}
complete_profile "$EDITOR" "QA Editor"
complete_profile "$OWNER" "QA Owner"

# Finish the setup wizard with starter content (a 404 means it was already finished).
form /admin/setup/site "name=QA Site" "url=" "language=en" "timezone=UTC"
form /admin/setup/theme "colour=#0060df"
form /admin/setup/content "content=starter"

n=0
for i in $(seq 1 57); do
  "$BIN" entity create --schema post --name "QA Post $i" --publish --as "$OWNER" >/dev/null 2>&1 && n=$((n + 1))
done
echo "seeded $n posts"

# The product supports 301, 302, 307 and 308 only (no 410).
"$BIN" redirect create --from /qa-moved --to /about --status 301 >/dev/null 2>&1 && echo "redirect 301"
"$BIN" redirect create --from /qa-temp --to /contact --status 302 >/dev/null 2>&1 && echo "redirect 302"
"$BIN" redirect create --from /qa-preview --to /blog --status 307 >/dev/null 2>&1 && echo "redirect 307"

# The media library already has the pixel the profiles uploaded. Add a 1200x675 image (builder-settings.mjs: its media row
# has width and height, so the public page writes them on the <img> and the layout is reserved before the file loads).
wide=$(mktemp --suffix=.png)
node "$(dirname "${BASH_SOURCE[0]}")/make-png.mjs" 1200 675 "$wide" && post /admin/media/upload -F "file=@$wide;filename=qa-wide.png;type=image/png"
rm -f "$wide"

# The header menu: 6 items, "Team" nested under "About".
menu=$(curl -s "${NAV[@]}" -b "$jar" "$BASE/admin/theme/menus" | grep -oE 'menus/[0-9a-f-]{36}">Header' | head -1 | cut -d'"' -f1)
if [ -n "$menu" ]; then
  items=("name=Header" "location=header" "_op=save")
  i=0
  for row in "Home|0|/" "About|0|/about" "Team|1|/about/team" "Blog|0|/blog" "Contact|0|/contact" "Docs|0|https://example.com/docs"; do
    IFS='|' read -r label depth url <<<"$row"
    items+=("i.$i.label=$label" "i.$i.type=custom" "i.$i.depth=$depth" "i.$i.url=$url")
    i=$((i + 1))
  done
  form "/admin/theme/$menu" "${items[@]}"
fi

# Three public submissions on the starter contact form.
for who in Ada Grace Linus; do
  curl -s -o /dev/null -w "submit $who %{http_code}\n" -X POST -H 'Content-Type: application/json' \
    -d "{\"name\":\"$who\",\"email\":\"${who,,}@qa.test\",\"message\":\"Hello from $who\"}" "$BASE/api/v1/public/forms/contact/submit"
done

# A few public hits on addresses that do not exist: the 404 log, so the dashboard's Missing pages card has rows.
for u in /old-pricing /old-pricing /about-us.html; do curl -s -o /dev/null -w "404 hit $u %{http_code}\n" "$BASE$u"; done

# A page cache size limit (100 MB), so Settings > Performance shows the Cache use bar.
# The lifetime is posted at its default, which the page stores as "server default".
form /admin/settings/performance "enabled=on" "ttl=300" "max_mb=100"
