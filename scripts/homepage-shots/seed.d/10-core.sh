# Sourced by seed.sh. Users, profiles, the setup wizard, the header menu.
printf '%s' "$PASS" | "$BIN" user create --email "$OWNER" --name "Mika Lindqvist" --role owner --password-stdin 2>&1 | tail -1

# complete_profile EMAIL NAME AVATAR: sign in, upload the avatar (exempt from the
# profile gate) and complete the profile; the admin hides every other screen until then.
complete_profile() {
  local email=$1 name=$2 avatar=$3 photo code
  signin "$email"
  photo=$(upload "$avatar")
  code=$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -b "$jar" -H "Origin: $BASE" --data-urlencode "_csrf=$(csrf)" \
    --data-urlencode "name=$name" --data-urlencode "photo=$photo" --data-urlencode "public=true" "$BASE/admin/profile/complete")
  [ "$code" = 303 ] || { echo "seed: completing the profile of $email failed ($code)" >&2; return 1; }
  echo "profile complete: $email"
}
EDITOR_EMAIL=aiko.tanaka@example.com
printf '%s' "$PASS" | "$BIN" user create --email "$EDITOR_EMAIL" --name "Aiko Tanaka" --role editor --password-stdin 2>&1 | tail -1
complete_profile "$EDITOR_EMAIL" "Aiko Tanaka" "$ASSETS/avatar-2.png" || return 1
complete_profile "$OWNER" "Mika Lindqvist" "$ASSETS/avatar-1.png" || return 1

form /admin/setup/site "name=Northwind Studio" "url=" "language=en" "timezone=UTC"
form /admin/setup/theme "colour=#0060df"
form /admin/setup/content "content=starter"

# The header menu: exactly Home, About, Blog, Contact.
menu=$(curl -fs -b "$jar" "$BASE/admin/theme/menus" | grep -oE 'menus/[0-9a-f-]{36}">Header' | head -1 | cut -d'"' -f1)
[ -n "$menu" ] || { echo "seed: header menu not found" >&2; return 1; }
items=("name=Header" "location=header" "_op=save")
i=0
for row in "Home|/" "About|/about" "Blog|/blog" "Contact|/contact"; do
  IFS='|' read -r label url <<<"$row"
  items+=("i.$i.label=$label" "i.$i.type=custom" "i.$i.depth=0" "i.$i.url=$url")
  i=$((i + 1))
done
form "/admin/theme/$menu" "${items[@]}"
