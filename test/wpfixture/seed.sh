#!/usr/bin/env bash
# Seeds a fresh WordPress with the fixture content the importer tests rely on.
# Used by both the docker fixture (wp-cli container) and run-local.sh.
#
# Environment:
#   WP          wp-cli command line (default: wp)
#   WP_URL      public URL of the site, no trailing slash (required)
#   HERE        fixture directory (default: this script's directory)
#   ENV_OUT     file receiving WP_FIXTURE_* variables (default: $HERE/wpfixture.env)
#   HARD_REWRITE=1   also write .htaccess (Apache); not needed with php -S
set -euo pipefail

HERE=${HERE:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}
WP=${WP:-wp}
: "${WP_URL:?WP_URL is required}"
WP_URL=${WP_URL%/}
ENV_OUT=${ENV_OUT:-$HERE/wpfixture.env}
ADMIN_USER=admin
ADMIN_PASS=${ADMIN_PASS:-fixture-admin-pass}
YOAST_VERSION=28.7
CF7_VERSION=6.2.2
EMPTY='{}'
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

wp() { command $WP "$@"; } # $WP may be several words (php + phar + flags)

if ! wp core is-installed 2>/dev/null; then
  wp core install --url="$WP_URL" --title="Acme Fixture" --admin_user="$ADMIN_USER" \
    --admin_password="$ADMIN_PASS" --admin_email=admin@example.com --skip-email
fi

# ---- application password, WXR export and the env file ----------------------
# Also run when the site was seeded earlier but its env file is missing or stale
# (an older seed wrote no WP_FIXTURE_WXR): the content is not seeded twice.
publish_env() {
  # ---- application password ----------------------------------------------------
  wp user application-password delete "$ADMIN_USER" --all >/dev/null 2>&1 || true
  APP_PASS=$(wp user application-password create "$ADMIN_USER" hatamoto-importer --porcelain)

  # ---- WXR export, for the file-upload source ---------------------------------
  out_dir=$(dirname "$ENV_OUT")
  mkdir -p "$out_dir"
  rm -f "$out_dir"/wpfixture*.wxr
  wp export --dir="$out_dir" --filename_format=wpfixture.wxr --skip_comments >/dev/null

  wp option update hm_fixture_seeded 1
  {
    echo "WP_FIXTURE_URL=$WP_URL"
    echo "WP_FIXTURE_USER=$ADMIN_USER"
    echo "WP_FIXTURE_APP_PASSWORD=$APP_PASS"
    echo "WP_FIXTURE_WXR=$out_dir/wpfixture.wxr"
  } > "$ENV_OUT"
  echo "seeded $WP_URL (credentials in $ENV_OUT)"
}

if [ "$(wp option get hm_fixture_seeded 2>/dev/null || true)" = "1" ]; then
  echo "fixture already seeded"
  if [ -f "$ENV_OUT" ] && grep -q '^WP_FIXTURE_WXR=' "$ENV_OUT" && [ -f "$(dirname "$ENV_OUT")/wpfixture.wxr" ]; then
    exit 0
  fi
  echo "env file stale: regenerating the application password and the export"
  publish_env
  exit 0
fi

# ---- clean the stock content so slugs and ids are predictable ---------------
wp post delete 1 2 3 --force >/dev/null 2>&1 || true

# ---- settings -------------------------------------------------------------
wp option update blogname "Acme Fixture"
wp option update blogdescription "Fixture tagline for importer tests"
wp option update timezone_string "UTC"
wp option update WPLANG ""
wp option update default_comment_status closed
wp rewrite structure '/%year%/%monthnum%/%postname%/' ${HARD_REWRITE:+--hard}
wp rewrite flush ${HARD_REWRITE:+--hard}

# ---- plugins: pinned versions ---------------------------------------------
# Each zip is checked against the same sha256 run-local.sh verifies.
install_verified() { # name version sha256
  local zip="$tmp/$1.$2.zip"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "https://downloads.wordpress.org/plugin/$1.$2.zip" -o "$zip"
  else
    php -r 'copy($argv[1], $argv[2]) or exit(1);' "https://downloads.wordpress.org/plugin/$1.$2.zip" "$zip"
  fi
  [ "$(sha256sum "$zip" | cut -d' ' -f1)" = "$3" ] || { echo "sha256 mismatch for $1 $2" >&2; exit 1; }
  wp plugin install "$zip"
}
wp plugin is-installed wordpress-seo   || install_verified wordpress-seo "$YOAST_VERSION" 148b0ae90d90e82fb871bb8524755331d92154d4e30978d752f078416275420e
wp plugin is-installed contact-form-7  || install_verified contact-form-7 "$CF7_VERSION" 13bfea4c2753051cd17953148f99d4e79d8c3ec63039129e54a2b9cbc4930f58
wp plugin activate wordpress-seo contact-form-7

# ---- mu-plugins: a custom post type and a local avatar ---------------------
mu=$(wp eval 'echo WPMU_PLUGIN_DIR;')
mkdir -p "$mu"
cat > "$mu/fixture.php" <<'PHP'
<?php
// Importer fixture: a public CPT with an archive, and avatars from the media library.
add_action('init', function () {
  register_post_type('portfolio', [
    'label' => 'Portfolio', 'labels' => ['singular_name' => 'Portfolio item'],
    'public' => true, 'has_archive' => true, 'show_in_rest' => true, 'rest_base' => 'portfolio',
    'supports' => ['title', 'editor', 'excerpt', 'thumbnail', 'author'],
    'rewrite' => ['slug' => 'portfolio'],
  ]);
});
add_filter('get_avatar_url', function ($url, $id_or_email) {
  $user = null;
  if (is_numeric($id_or_email)) $user = get_user_by('id', (int) $id_or_email);
  elseif (is_string($id_or_email)) $user = get_user_by('email', $id_or_email);
  elseif ($id_or_email instanceof WP_User) $user = $id_or_email;
  if ($user && ($att = (int) get_user_meta($user->ID, 'fixture_avatar_id', true))) {
    $u = wp_get_attachment_url($att);
    if ($u) return $u;
  }
  return $url;
}, 10, 2);
PHP

# ---- media library ----------------------------------------------------------
declare -A IMG_ID
for f in hero hero-copy gallery-1 gallery-2 gallery-3 avatar; do
  file=$(ls "$HERE"/assets/$f.* | head -1)
  id=$(wp media import "$file" --title="$f" --porcelain)
  IMG_ID[$f]=$id
  wp post meta update "$id" _wp_attachment_image_alt "Alt text for $f"
done
img_url() { wp eval "echo wp_get_attachment_url($1);"; }
img_small() { wp eval "echo wp_get_attachment_image_url($1, 'medium');"; }

render() { # render <content file> -> $tmp/<name> with placeholders filled
  local src=$1 out=$tmp/$(basename "$1")
  cp "$src" "$out"
  sed -i "s|{{SITE}}|$WP_URL|g; s|{{CF7_ID}}|${CF7_ID:-0}|g" "$out"
  for f in "${!IMG_ID[@]}"; do
    id=${IMG_ID[$f]}
    sed -i "s|{{IMG_${f}_ID}}|$id|g; s|{{IMG_${f}_URL}}|$(img_url "$id")|g; s|{{IMG_${f}_SMALL}}|$(img_small "$id")|g" "$out"
  done
  echo "$out"
}

CF7_ID=$(wp post list --post_type=wpcf7_contact_form --field=ID --posts_per_page=1 | head -1)

# ---- users -------------------------------------------------------------------
if ! wp user get jane >/dev/null 2>&1; then
  wp user create jane jane@example.com --role=author --display_name="Jane Guest" \
    --first_name=Jane --last_name=Guest --user_pass=jane-pass --porcelain >/dev/null
fi
JANE=$(wp user get jane --field=ID)
wp user update jane --display_name="Jane Guest" --description="Jane writes about local search and the odd video."
wp user meta update "$JANE" fixture_avatar_id "${IMG_ID[avatar]}"
wp user update "$ADMIN_USER" --display_name="Site Admin"
ADMIN_ID=$(wp user get "$ADMIN_USER" --field=ID)

# ---- terms -------------------------------------------------------------------
NEWS=$(wp term create category "News" --description="Company news" --porcelain)
UPDATES=$(wp term create category "Product Updates" --parent="$NEWS" --description="What shipped" --porcelain)
for t in seo local video; do wp term create post_tag "$t" >/dev/null; done

# ---- pages -------------------------------------------------------------------
page() { # page <slug> <title> <file> [parent] [status] [meta_json]
  wp post create "$(render "$HERE/content/$3")" --post_type=page --post_status="${5:-publish}" \
    --post_title="$2" --post_name="$1" --post_parent="${4:-0}" --post_author="$ADMIN_ID" \
    --post_date='2024-01-10 08:00:00' --meta_input="${6:-$EMPTY}" --porcelain
}
HOME_ID=$(page home "Home" page-home.html)
ABOUT=$(page about "About" page-about.html 0 publish '{"_yoast_wpseo_title":"About Acme %%sep%% %%sitename%%","_yoast_wpseo_metadesc":"All about the Acme fixture company."}')
BLOG=$(wp post create --post_type=page --post_status=publish --post_title="Blog" --post_name=blog --porcelain)
SERVICES=$(page services "Services" page-services.html)
SEO=$(page seo "SEO" page-seo.html "$SERVICES")
LOCAL=$(page local "Local" page-local.html "$SEO" publish '{"_yoast_wpseo_title":"Local SEO Services for Shops","_yoast_wpseo_metadesc":"Get found by customers near you."}')
CONTACT=$(page contact "Contact" page-contact.html)
LANDING=$(page landing "Landing" page-landing.html 0 publish '{"_elementor_edit_mode":"builder","_elementor_version":"3.21.0"}')
wp option update show_on_front page
wp option update page_on_front "$HOME_ID"
wp option update page_for_posts "$BLOG"

# ---- posts -------------------------------------------------------------------
post() { # post <slug> <title> <file> <date> <status> <author> <categories> <tags> <meta_json>
  wp post create "$(render "$HERE/content/$3")" --post_type=post --post_status="$5" --post_title="$2" \
    --post_name="$1" --post_date="$4" --post_author="$6" --post_category="$7" --tags_input="$8" \
    --meta_input="${9:-$EMPTY}" --porcelain
}
P1=$(post hello-world "Hello World" hello-world.html '2024-05-10 09:00:00' publish "$JANE" "$NEWS" "seo,video" \
  '{"_yoast_wpseo_title":"Hello World: the very first post %%sep%% %%sitename%%","_yoast_wpseo_metadesc":"The first post on the Acme fixture site.","_yoast_wpseo_canonical":"'"$WP_URL"'/2024/05/hello-world/"}')
wp post meta update "$P1" _thumbnail_id "${IMG_ID[hero]}"
P2=$(post local-seo-guide "Local SEO Guide" local-seo-guide.html '2024-06-15 10:30:00' publish "$JANE" "$NEWS,$UPDATES" "seo,local" \
  '{"_yoast_wpseo_title":"Custom SEO Title: Local SEO | Acme","_yoast_wpseo_metadesc":"A practical guide to local SEO for small shops."}')
P3=$(post gallery-showcase "Gallery Showcase" gallery-showcase.html '2024-07-01 12:00:00' publish "$JANE" "$NEWS" "" '{}')
P4=$(post video-roundup "Video Roundup" video-roundup.html '2024-08-20 15:45:00' publish "$JANE" "$UPDATES" "video" \
  '{"_yoast_wpseo_metadesc":"A YouTube and a Vimeo video."}')
P5=$(post hidden-notes "Hidden Notes" hidden-notes.html '2024-09-02 08:00:00' publish "$ADMIN_ID" "$NEWS" "" \
  '{"_yoast_wpseo_meta-robots-noindex":"1","_yoast_wpseo_title":"Hidden Notes (not for search)"}')
P6=$(post members-only-roadmap "Members Only Roadmap" members-only.html '2024-09-10 08:00:00' private "$JANE" "$NEWS" "" '{}')
P7=$(post upcoming-draft "Upcoming Draft" draft.html '2024-10-01 08:00:00' draft "$JANE" "$NEWS" "" '{}')

# ---- custom post type --------------------------------------------------------
for n in 1 2; do
  wp post create "$(render "$HERE/content/portfolio-1.html")" --post_type=portfolio --post_status=publish \
    --post_title="Portfolio Piece $n" --post_name="piece-$n" --post_date="2024-03-0$n 09:00:00" --porcelain >/dev/null
done

# ---- menus -------------------------------------------------------------------
wp menu create "Main Menu" >/dev/null
wp menu item add-post main-menu "$HOME_ID" --title="Home" >/dev/null
SV=$(wp menu item add-post main-menu "$SERVICES" --title="Services" --porcelain)
wp menu item add-post main-menu "$SEO" --title="SEO" --parent-id="$SV" >/dev/null
wp menu item add-post main-menu "$CONTACT" --title="Contact" >/dev/null
wp menu item add-custom main-menu "Partner" "https://example.org/partner" >/dev/null
wp menu create "Footer Menu" >/dev/null
wp menu item add-post footer-menu "$ABOUT" --title="About" >/dev/null
wp menu item add-term footer-menu category "$NEWS" --title="News" >/dev/null

publish_env
