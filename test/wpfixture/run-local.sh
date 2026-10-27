#!/usr/bin/env bash
# Runs the WordPress fixture without root and without Docker.
#
#   test/wpfixture/run-local.sh [up|down|reset|env]
#
# up     download pinned tools (once), install + seed WordPress, serve it on a free
#        local port with `php -S`, and print the WP_FIXTURE_* variables
# down   stop the server (data is kept)
# reset  stop the server and delete the site (downloads are kept)
# env    print the variables of a running fixture
# wp ... run wp-cli against the running fixture
#
# Everything lives in $WPFIXTURE_CACHE (default ~/.cache/hatamoto-wpfixture).
# Every download is pinned by version and sha256; a mismatch aborts.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
CACHE=${WPFIXTURE_CACHE:-$HOME/.cache/hatamoto-wpfixture}
DL=$CACHE/downloads
SITE=$CACHE/site
WEBROOT=$SITE/wordpress
PHP_BIN=$CACHE/php/php
WPCLI=$DL/wp-cli.phar
PIDFILE=$SITE/php.pid
ENVFILE=$SITE/wpfixture.env

# name|url|sha256
PHP_PKG='php-8.4.8-cli-linux-x86_64.tar.gz|https://dl.static-php.dev/static-php-cli/common/php-8.4.8-cli-linux-x86_64.tar.gz|a1445830e2ae2f29472400692576e1f85bc2df1b6c83e804bcfd56fcd24b8709'
WP_PKG='wordpress-7.1.3.zip|https://downloads.wordpress.org/release/wordpress-7.1.3.zip|eb58a90ed55147f90f7bd55d0d3334960a69b7e8a4cc142c8ddfea8965218227'
SQLITE_PKG='sqlite-database-integration.3.1.0.zip|https://downloads.wordpress.org/plugin/sqlite-database-integration.3.1.0.zip|e260e3ada78676f1c0f4e09eb9ebcb9ecce4d4b85f0c6e5a70fb9a93f5b9847f'
YOAST_PKG='wordpress-seo.28.7.zip|https://downloads.wordpress.org/plugin/wordpress-seo.28.7.zip|148b0ae90d90e82fb871bb8524755331d92154d4e30978d752f078416275420e'
CF7_PKG='contact-form-7.6.2.2.zip|https://downloads.wordpress.org/plugin/contact-form-7.6.2.2.zip|13bfea4c2753051cd17953148f99d4e79d8c3ec63039129e54a2b9cbc4930f58'
WPCLI_PKG='wp-cli.phar|https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar|ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c'

log() { echo "wpfixture: $*" >&2; }

fetch() { # fetch "name|url|sha"
  IFS='|' read -r name url sha <<<"$1"
  mkdir -p "$DL"
  if [ ! -f "$DL/$name" ] || [ "$(sha256sum "$DL/$name" | cut -d' ' -f1)" != "$sha" ]; then
    log "downloading $name"
    curl -fsSL --retry 3 -o "$DL/$name.part" "$url"
    mv "$DL/$name.part" "$DL/$name"
  fi
  got=$(sha256sum "$DL/$name" | cut -d' ' -f1)
  [ "$got" = "$sha" ] || { log "sha256 mismatch for $name: got $got, want $sha"; rm -f "$DL/$name"; exit 1; }
}

unzip_to() { # unzip_to <zip> <dir>  (uses the bundled PHP, so no unzip binary is needed)
  "$PHP_BIN" -r '$z = new ZipArchive; if ($z->open($argv[1]) !== true) exit(1); $z->extractTo($argv[2]); $z->close();' "$1" "$2"
}

free_port() {
  for _ in $(seq 1 50); do
    p=$((20000 + RANDOM % 20000))
    (exec 3<>/dev/tcp/127.0.0.1/$p) 2>/dev/null || { echo $p; return; }
  done
  log "no free port found"; exit 1
}

php_flags=(-d memory_limit=512M -d upload_max_filesize=64M -d post_max_size=64M -d max_execution_time=300 -d variables_order=EGPCS -d display_errors=0 -d error_reporting=E_ALL\&~E_DEPRECATED)
for ca in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt; do
  [ -f "$ca" ] && php_flags+=(-d "openssl.cafile=$ca" -d "curl.cainfo=$ca") && break
done

running() { [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; }

down() {
  if running; then kill "$(cat "$PIDFILE")" || true; log "stopped"; fi
  rm -f "$PIDFILE"
}

install_tools() {
  for p in "$PHP_PKG" "$WP_PKG" "$SQLITE_PKG" "$YOAST_PKG" "$CF7_PKG" "$WPCLI_PKG"; do fetch "$p"; done
  if [ ! -x "$PHP_BIN" ]; then
    mkdir -p "$CACHE/php"
    tar -xzf "$DL/${PHP_PKG%%|*}" -C "$CACHE/php"
  fi
}

install_site() {
  [ -f "$WEBROOT/wp-config.php" ] && return
  log "installing WordPress"
  rm -rf "$WEBROOT"; mkdir -p "$SITE"
  unzip_to "$DL/${WP_PKG%%|*}" "$SITE"
  plugins=$WEBROOT/wp-content/plugins
  for p in "$SQLITE_PKG" "$YOAST_PKG" "$CF7_PKG"; do unzip_to "$DL/${p%%|*}" "$plugins"; done
  sed -e "s|{SQLITE_IMPLEMENTATION_FOLDER_PATH}|$plugins/sqlite-database-integration|" \
      -e "s|{SQLITE_PLUGIN}|sqlite-database-integration/load.php|" \
      "$plugins/sqlite-database-integration/db.copy" > "$WEBROOT/wp-content/db.php"
  mkdir -p "$WEBROOT/wp-content/database"
  cat > "$WEBROOT/wp-config.php" <<PHPEOF
<?php
define('DB_NAME', 'wordpress');
define('DB_USER', '');
define('DB_PASSWORD', '');
define('DB_HOST', '');
define('DB_CHARSET', 'utf8');
define('DB_COLLATE', '');
define('DB_ENGINE', 'sqlite');
define('AUTH_KEY', 'fixture'); define('SECURE_AUTH_KEY', 'fixture'); define('LOGGED_IN_KEY', 'fixture'); define('NONCE_KEY', 'fixture');
define('AUTH_SALT', 'fixture'); define('SECURE_AUTH_SALT', 'fixture'); define('LOGGED_IN_SALT', 'fixture'); define('NONCE_SALT', 'fixture');
\$table_prefix = 'wp_';
define('WP_ENVIRONMENT_TYPE', 'local'); // allows application passwords over http
define('DISABLE_WP_CRON', true);
define('WP_DEBUG', false);
define('AUTOMATIC_UPDATER_DISABLED', true);
if (!defined('ABSPATH')) define('ABSPATH', __DIR__ . '/');
require_once ABSPATH . 'wp-settings.php';
PHPEOF
  cp "$HERE/router.php" "$SITE/router.php"
}

serve() {
  running && return
  PORT=$(free_port)
  log "serving on 127.0.0.1:$PORT"
  PHP_CLI_SERVER_WORKERS=8 nohup "$PHP_BIN" "${php_flags[@]}" -S "127.0.0.1:$PORT" -t "$WEBROOT" "$SITE/router.php" \
    >"$SITE/php.log" 2>&1 &
  echo $! > "$PIDFILE"; echo "$PORT" > "$SITE/port"
  for _ in $(seq 1 50); do
    (exec 3<>/dev/tcp/127.0.0.1/$PORT) 2>/dev/null && return
    sleep 0.2
  done
  log "server did not start; see $SITE/php.log"; exit 1
}

seed() {
  PORT=$(cat "$SITE/port")
  export WP_URL="http://127.0.0.1:$PORT"
  export WP="$PHP_BIN ${php_flags[*]} $WPCLI --path=$WEBROOT --url=$WP_URL --allow-root"
  # The home URL is stored at install time; a new port needs it refreshed.
  if $WP core is-installed 2>/dev/null; then
    $WP option update home "$WP_URL" >/dev/null; $WP option update siteurl "$WP_URL" >/dev/null
  fi
  ENV_OUT=$ENVFILE HERE=$HERE bash "$HERE/seed.sh" >&2
  # seed.sh keeps the credentials of the first run; only the URL changes with the port.
  sed -i "s|^WP_FIXTURE_URL=.*|WP_FIXTURE_URL=$WP_URL|" "$ENVFILE"
}

case "${1:-up}" in
  up)
    install_tools; install_site; serve; seed
    cat "$ENVFILE" ;;
  down) down ;;
  reset) down; rm -rf "$SITE"; log "site removed" ;;
  env) running || { log "not running"; exit 1; }; cat "$ENVFILE" ;;
  wp)
    running || { log "not running"; exit 1; }
    shift
    PORT=$(cat "$SITE/port")
    exec "$PHP_BIN" "${php_flags[@]}" "$WPCLI" --path="$WEBROOT" --url="http://127.0.0.1:$PORT" --allow-root "$@" ;;
  *) sed -n 2,14p "$0"; exit 64 ;;
esac
