
# First-boot stage: runs once per instance (idempotent, resumes after a failure).
# Creates every secret, the database, the env file and the Caddy site, starts
# the services and prints the setup link. Nothing here runs at build time.

APP_DIR=/opt/hatamoto
APP_USER=hatamoto
APP_PORT=8090
FB_ALPHABET='ABCDEFGHJKMNPQRSTUVWXYZ23456789'
FB_TMP=""
FB_IP=""
FB_HOST=""
FB_PENDING_DOMAIN=""

fb_sleep() { sleep "${HATAMOTO_FIRSTBOOT_POLL:-1}"; }

# fb_valid_ip IP succeeds for a dotted IPv4 address.
fb_valid_ip() {
  printf '%s\n' "$1" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}$' || return 1
  fvi_ok=0
  for fvi_o in $(printf '%s' "$1" | tr '.' ' '); do
    [ "$fvi_o" -le 255 ] || fvi_ok=1
  done
  return "$fvi_ok"
}

# fb_valid_domain NAME: lower-case letters, digits, dots and dashes; no leading
# or trailing dash or dot, no empty label, at least one dot.
fb_valid_domain() {
  [ "${#1}" -le 253 ] || return 1
  printf '%s\n' "$1" | grep -Eq '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'
}

# fb_sslip_name IP prints the dashed sslip.io name of an address.
fb_sslip_name() {
  fb_valid_ip "$1" || die "not an IPv4 address: $1"
  printf '%s.sslip.io\n' "$(printf '%s' "$1" | tr '.' '-')"
}

# fb_gen_hex BYTES prints that many random bytes as lower-case hex.
fb_gen_hex() {
  od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'
  echo
}

# fb_check_code CODE succeeds when the setup code is acceptable to the server
# (backend/internal/config/admin.go): letters, digits and dashes only, at least
# 12 characters once normalised, at least 4 distinct, not one short unit repeated.
fb_check_code() {
  printf '%s\n' "$1" | grep -Eq '^[A-Za-z0-9-]+$' || { echo "use letters, digits and dashes only" >&2; return 1; }
  fcc_norm="$(printf '%s' "$1" | tr 'a-z' 'A-Z' | tr -d '-')"
  [ "${#fcc_norm}" -ge 12 ] || { echo "it needs at least 12 letters or digits (dashes do not count)" >&2; return 1; }
  fcc_distinct="$(printf '%s' "$fcc_norm" | fold -w1 | sort -u | wc -l | tr -d ' ')"
  [ "$fcc_distinct" -ge 4 ] || { echo "it needs at least 4 different characters" >&2; return 1; }
  fcc_rep="$(awk -v c="$fcc_norm" 'BEGIN { n = length(c); for (u = 1; u <= n / 2; u++) if (n % u == 0) { s = substr(c, 1, u); r = ""; for (i = 0; i < n / u; i++) r = r s; if (r == c) { print "rep"; exit } } }')"
  [ -z "$fcc_rep" ] || { echo "it must not be one short piece repeated" >&2; return 1; }
}

# fb_gen_code prints a fresh setup code like K7M2-RT9P-QW4X.
fb_gen_code() {
  while :; do
    fgc_raw="$(LC_ALL=C tr -dc "$FB_ALPHABET" < /dev/urandom | head -c 12)"
    [ "${#fgc_raw}" -eq 12 ] || continue
    fgc_code="$(printf '%s' "$fgc_raw" | cut -c1-4)-$(printf '%s' "$fgc_raw" | cut -c5-8)-$(printf '%s' "$fgc_raw" | cut -c9-12)"
    fb_check_code "$fgc_code" 2>/dev/null || continue
    printf '%s\n' "$fgc_code"
    return 0
  done
}

# fb_render_env DBPASSWORD SECRET CODE BASE_URL prints the service environment file.
# Plain KEY=value lines (systemd reads them); the password is hex so it needs no URL encoding.
# HATAMOTO_UPDATES is left unset on purpose: the default is automatic signed updates.
fb_render_env() {
  cat <<HM_ENV
DATABASE_URL=postgres://$APP_USER:$1@127.0.0.1:5432/$APP_USER?sslmode=disable
ENVIRONMENT=production
PUBLIC_BASE_URL=$4
API_HOST=127.0.0.1
MEDIA_UPLOAD_DIR=$APP_DIR/uploads
HATAMOTO_CONFIG_FILE=$APP_DIR/config/hatamoto.env
HATAMOTO_SETUP_CODE=$3
HATAMOTO_SECRET=$2
HM_ENV
}

# fb_render_caddyfile HOST TLS EMAIL prints the Caddyfile: one site in front of the app.
# TLS is auto (public certificate), internal (Caddy's own CA) or off (plain http).
fb_render_caddyfile() {
  printf '# Written by the Hatamoto installer.\n'
  if [ -n "$3" ] && [ "$2" != off ]; then
    printf '{\n\temail %s\n}\n' "$3"
  fi
  case "$2" in
    off) printf 'http://%s {\n' "$1" ;;
    *) printf '%s {\n' "$1" ;;
  esac
  [ "$2" != internal ] || printf '\ttls internal\n'
  printf '\treverse_proxy 127.0.0.1:%s\n}\n' "$APP_PORT"
}

# fb_banner LINK CODE [DOMAIN IP] prints the message the owner sees on the
# console and at login. DOMAIN and IP add the "waiting for DNS" line.
fb_banner() {
  printf '\n  Hatamoto is ready.\n\n'
  printf '  Open this link to create your owner account\n'
  printf '  (the setup code is already filled in):\n\n'
  printf '    %s\n\n' "$1"
  printf '  Setup code: %s\n' "$2"
  if [ -n "${3:-}" ]; then
    printf '  Domain:     %s (waiting for DNS -> %s;\n' "$3" "${4:-}"
    printf '              will switch automatically)\n'
  fi
  printf '  Updates:    automatic (stable channel)\n'
  printf '  Status:     systemctl status hatamoto-api\n\n'
  printf '  This message disappears once the owner account exists.\n\n'
}

# fb_public_ip prints this machine's public IPv4 address.
fb_public_ip() {
  if [ -n "$IN_PUBLIC_IP" ]; then
    fb_valid_ip "$IN_PUBLIC_IP" || die "HATAMOTO_PUBLIC_IP is not an IPv4 address: $IN_PUBLIC_IP"
    printf '%s\n' "$IN_PUBLIC_IP"
    return 0
  fi
  fpi_ip="$(curl -4 --silent --fail --max-time 8 --retry 2 --proto '=https' --tlsv1.2 https://api.ipify.org 2>/dev/null || true)"
  if ! fb_valid_ip "$fpi_ip"; then
    fpi_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{ for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit } }')"
  fi
  fb_valid_ip "$fpi_ip" || die "cannot find the public IP address: set HATAMOTO_PUBLIC_IP"
  printf '%s\n' "$fpi_ip"
}

# fb_dns_matches DOMAIN IP succeeds when DOMAIN has an A record equal to IP.
fb_dns_matches() {
  getent ahostsv4 "$1" 2>/dev/null | awk -v ip="$2" '$1 == ip { found = 1 } END { exit !found }'
}

# fb_env_get FILE KEY prints the value of KEY in a plain env file.
fb_env_get() {
  sed -n "s/^$2=//p" "$1" | head -n 1
}

# fb_psql runs psql as the postgres superuser; SQL comes on stdin so no secret is in a process list.
fb_psql() {
  sudo -u postgres psql -X -q -v ON_ERROR_STOP=1 "$@"
}

# fb_as_app CMD... runs the hatamoto binary as the service user from the app
# directory with the service environment (never as root).
fb_as_app() {
  sudo -u "$APP_USER" sh -c 'envfile=$1; app=$2; shift 2; set -a; . "$envfile"; set +a; cd "$app" && HATAMOTO_DATA="$app/data" exec "$app/bin/hatamoto" "$@"' sh \
    "$(dest "$APP_DIR/config/hatamoto.env")" "$(dest "$APP_DIR")" "$@"
}

# fb_database creates the login role and database, or resets the role's password on a rerun.
# The role needs CREATEROLE: plugins get their own Postgres roles (see "Plugin
# database access" in the configuration reference) and the server creates them
# through the application role. It is not a superuser.
fb_database() {
  fd_pw="$1"
  fd_ready=0
  fd_i=0
  while [ "$fd_i" -lt 60 ]; do
    if [ "$(fb_psql -tA -c 'select 1' 2>/dev/null)" = 1 ]; then fd_ready=1; break; fi
    fd_i=$((fd_i + 1))
    fb_sleep
  done
  [ "$fd_ready" = 1 ] || die "PostgreSQL does not answer: check systemctl status postgresql"
  if [ "$(fb_psql -tA -c "select 1 from pg_roles where rolname = '$APP_USER'")" = 1 ]; then
    printf "ALTER ROLE %s WITH LOGIN CREATEROLE PASSWORD '%s';\n" "$APP_USER" "$fd_pw" | fb_psql
  else
    printf "CREATE ROLE %s WITH LOGIN CREATEROLE PASSWORD '%s';\n" "$APP_USER" "$fd_pw" | fb_psql
  fi
  if [ "$(fb_psql -tA -c "select 1 from pg_database where datname = '$APP_USER'")" != 1 ]; then
    printf 'CREATE DATABASE %s OWNER %s;\n' "$APP_USER" "$APP_USER" | fb_psql
  fi
}

# fb_wait_ready waits up to 120 s for the app's own readiness probe.
fb_wait_ready() {
  fwr_i=0
  while [ "$fwr_i" -lt 120 ]; do
    if [ "$(curl -fsS --max-time 3 "http://127.0.0.1:$APP_PORT/ready" 2>/dev/null || true)" = ok ]; then return 0; fi
    fwr_i=$((fwr_i + 1))
    fb_sleep
  done
  die "hatamoto did not become ready in 120 s: see journalctl -u hatamoto-api"
}

# fb_admin_url prints the private admin address (the first line must be a URL: before the
# server's first start the command prints a sentence instead, so retry).
fb_admin_url() {
  fau_i=0
  while [ "$fau_i" -lt 30 ]; do
    fau_line="$(fb_as_app admin-url 2>/dev/null | head -n 1 || true)"
    case "$fau_line" in http://* | https://*) printf '%s\n' "$fau_line"; return 0 ;; esac
    fau_i=$((fau_i + 1))
    fb_sleep
  done
  die "hatamoto did not report its admin address: run 'hatamoto admin-url' as the $APP_USER user"
}

# fb_wait_proxy checks /health through Caddy. A certificate that is not issued yet
# is not an error: Caddy keeps retrying, so this only reports.
fb_wait_proxy() {
  fwp_host="$1"
  fwp_i=0
  while [ "$fwp_i" -lt 60 ]; do
    case "$IN_TLS" in
      off) fwp_out="$(curl -fsS --max-time 5 --resolve "$fwp_host:80:127.0.0.1" "http://$fwp_host/health" 2>/dev/null || true)" ;;
      internal) fwp_out="$(curl -fsSk --max-time 5 --resolve "$fwp_host:443:127.0.0.1" "https://$fwp_host/health" 2>/dev/null || true)" ;;
      *) fwp_out="$(curl -fsS --max-time 5 --resolve "$fwp_host:443:127.0.0.1" "https://$fwp_host/health" 2>/dev/null || true)" ;;
    esac
    [ -z "$fwp_out" ] || return 0
    fwp_i=$((fwp_i + 1))
    fb_sleep
  done
  return 1
}

# fb_ensure_host_keys: a sealed image has none; sshd needs them before anyone can log in.
fb_ensure_host_keys() {
  command -v ssh-keygen >/dev/null 2>&1 || return 0
  for feh_k in "$(dest /etc/ssh)"/ssh_host_*_key; do
    [ ! -e "$feh_k" ] || return 0
  done
  mkdir -p "$(dest /etc/ssh)"
  if [ -n "$R" ]; then ssh-keygen -A -f "$R" >/dev/null 2>&1 || true; else ssh-keygen -A >/dev/null 2>&1 || true; fi
  systemctl restart ssh >/dev/null 2>&1 || systemctl restart sshd >/dev/null 2>&1 || true
}

# fb_stage NAME runs a stage defined by another part.
fb_stage() {
  command -v "$1" >/dev/null 2>&1 || die "stage not available in this build: $1"
  "$1"
}

stage_firstboot() {
  set +x
  umask 077
  cd /
  fs_done="$(dest "$STATE_DIR")/firstboot.done"
  if [ -f "$fs_done" ]; then
    log "first boot already done"
    return 0
  fi
  [ -x "$(dest "$APP_DIR/bin/hatamoto")" ] || die "hatamoto is not installed: run the build stage first (install.sh build)"
  need_cmd sudo
  need_cmd curl
  case "$IN_TLS" in auto | internal | off) ;; *) die "HATAMOTO_TLS must be auto, internal or off" ;; esac

  FB_TMP="$(mktemp -d)"
  trap 'rm -rf "$FB_TMP"' EXIT
  install -d -m 0700 "$(dest "$STATE_DIR")"
  fb_ensure_host_keys

  # Where the site lives: the domain when it already points here, else the sslip.io name.
  fs_ip="$(fb_public_ip)"
  fs_host="$(fb_sslip_name "$fs_ip")"
  fs_pending=""
  if [ -n "$IN_DOMAIN" ]; then
    fs_domain="$(printf '%s' "$IN_DOMAIN" | tr 'A-Z' 'a-z')"
    fb_valid_domain "$fs_domain" || die "HATAMOTO_DOMAIN is not a valid host name: $IN_DOMAIN"
    if fb_dns_matches "$fs_domain" "$fs_ip"; then
      fs_host="$fs_domain"
    else
      fs_pending="$fs_domain"
      warn "$fs_domain does not point to $fs_ip yet: serving on $fs_host and switching automatically once it does"
    fi
  fi
  # The parts below (and the DNS switch) read these.
  FB_IP="$fs_ip"
  FB_HOST="$fs_host"
  FB_PENDING_DOMAIN="$fs_pending"
  if [ "$IN_TLS" = off ]; then fs_base="http://$fs_host"; else fs_base="https://$fs_host"; fi

  # Secrets: a rerun after a failure keeps the ones already written.
  fs_envfile="$(dest "$APP_DIR/config/hatamoto.env")"
  if [ -f "$fs_envfile" ] && [ -n "$(fb_env_get "$fs_envfile" HATAMOTO_SECRET)" ]; then
    fs_dbpw="$(fb_env_get "$fs_envfile" DATABASE_URL | sed -n 's#^postgres://[^:]*:\([^@]*\)@.*#\1#p')"
    fs_secret="$(fb_env_get "$fs_envfile" HATAMOTO_SECRET)"
    fs_code="$(fb_env_get "$fs_envfile" HATAMOTO_SETUP_CODE)"
    [ -n "$fs_dbpw" ] && [ -n "$fs_code" ] || die "the existing env file is incomplete: remove $fs_envfile and run again"
    log "resuming a first boot that did not finish"
  else
    fs_dbpw="$(fb_gen_hex 24)"
    fs_secret="$(fb_gen_hex 32)"
    if [ -n "$IN_SETUP_CODE" ]; then
      fb_check_code "$IN_SETUP_CODE" || die "HATAMOTO_SETUP_CODE is not acceptable"
      fs_code="$IN_SETUP_CODE"
    else
      fs_code="$(fb_gen_code)"
    fi
  fi

  systemctl enable --now postgresql >/dev/null 2>&1 || die "cannot start postgresql"
  fb_database "$fs_dbpw"

  fb_render_env "$fs_dbpw" "$fs_secret" "$fs_code" "$fs_base" > "$FB_TMP/hatamoto.env"
  install -m 0640 -o root -g "$APP_USER" "$FB_TMP/hatamoto.env" "$fs_envfile"
  fb_as_app migrate >/dev/null || die "database migration failed: see the output above"

  fs_caddy="$(dest /etc/caddy/Caddyfile)"
  mkdir -p "$(dirname "$fs_caddy")"
  if [ -f "$fs_caddy" ] && ! grep -q '^# Written by the Hatamoto installer' "$fs_caddy" && [ ! -e "$fs_caddy.orig" ]; then
    cp "$fs_caddy" "$fs_caddy.orig"
  fi
  fb_render_caddyfile "$fs_host" "$IN_TLS" "$IN_EMAIL" > "$FB_TMP/Caddyfile"
  install -m 0644 "$FB_TMP/Caddyfile" "$fs_caddy"

  # Restart, not just start: on a sealed image or a resumed run both services may
  # already be running with the package default config / the old env file.
  systemctl enable caddy >/dev/null 2>&1 || die "cannot enable caddy"
  systemctl restart caddy >/dev/null 2>&1 || die "cannot start caddy: see journalctl -u caddy"
  systemctl enable hatamoto-api >/dev/null 2>&1 || die "cannot enable hatamoto-api"
  systemctl restart hatamoto-api >/dev/null 2>&1 || die "cannot start hatamoto-api: see journalctl -u hatamoto-api"
  fb_wait_ready

  fs_admin="$(fb_admin_url)"
  printf '%s\n' "$fs_admin" > "$FB_TMP/link"
  install -m 0600 -o root "$FB_TMP/link" "$(dest "$STATE_DIR")/link"
  fs_link="$fs_admin/setup#code=$fs_code"

  if ! fb_wait_proxy "$fs_host"; then
    # With a public certificate the issuance may still be pending; with Caddy's own CA or plain http nothing can be.
    [ "$IN_TLS" = auto ] || die "no answer through Caddy: see journalctl -u caddy"
    warn "no answer through Caddy yet: the certificate may still be pending (Caddy keeps retrying); see journalctl -u caddy"
  fi

  # Housekeeping parts, then the done marker last.
  fb_stage install_motd
  fb_stage install_dns_switch
  fb_stage setup_swap
  fb_stage setup_backups

  fb_banner "$fs_link" "$fs_code" "$fs_pending" "$fs_ip"
  if [ -z "$R" ] && [ -w /dev/console ]; then
    fb_banner "$fs_link" "$fs_code" "$fs_pending" "$fs_ip" > /dev/console 2>/dev/null || true
  fi
  : > "$fs_done"
  log "first boot done"
}
