
# Login message and the DNS switch.
#
# The message is a script in /etc/update-motd.d: it reads the setup code from the
# env file and the admin address cached by first boot, and stays silent once the
# owner account exists. The DNS switch is a timer that moves the site from its
# sslip.io name to the owner's domain as soon as that domain points here.

DNS_UNIT=hatamoto-dns-switch

install_motd() {
  im_file="$(dest /etc/update-motd.d/99-hatamoto)"
  mkdir -p "$(dirname "$im_file")"
  cat > "$im_file" <<'HM_MOTD'
#!/bin/sh
# Hatamoto first-run message. Shown at login until the owner account exists.
lib="${HATAMOTO_INSTALL_ROOT:-}/usr/local/lib/hatamoto/firstboot.sh"
[ -r "$lib" ] || exit 0
HATAMOTO_INSTALL_SOURCE_ONLY=1
export HATAMOTO_INSTALL_SOURCE_ONLY
. "$lib"
envfile="$(dest /opt/hatamoto/config/hatamoto.env)"
linkfile="$(dest "$STATE_DIR/link")"
[ -r "$envfile" ] && [ -r "$linkfile" ] || exit 0
owners="$(cd / && sudo -n -u postgres psql -X -d hatamoto -tAc 'select count(*) from hatamoto_users' 2>/dev/null)" || exit 0
[ "$owners" = 0 ] || exit 0
code="$(fb_env_get "$envfile" HATAMOTO_SETUP_CODE)"
[ -n "$code" ] || exit 0
domain=""
ip=""
if [ -r "$(dest "$STATE_DIR/dns-pending")" ]; then
  read -r domain ip _ < "$(dest "$STATE_DIR/dns-pending")" || true
fi
fb_banner "$(head -n 1 "$linkfile")/setup#code=$code" "$code" "$domain" "$ip"
HM_MOTD
  chmod 0755 "$im_file"
}

# dns_remove drops the timer and its files.
dns_remove() {
  if [ -f "$(dest /etc/systemd/system/$DNS_UNIT.timer)" ]; then
    systemctl disable --now "$DNS_UNIT.timer" >/dev/null 2>&1 || true
  fi
  rm -f "$(dest /etc/systemd/system/$DNS_UNIT.timer)" "$(dest /etc/systemd/system/$DNS_UNIT.service)" \
    "$(dest "$LIB_DIR/dns-switch.sh")" "$(dest "$STATE_DIR/dns-pending")"
}

# set_base_url FILE URL prints the env file with PUBLIC_BASE_URL set to URL.
set_base_url() {
  if grep -q '^PUBLIC_BASE_URL=' "$1"; then
    sed "s#^PUBLIC_BASE_URL=.*#PUBLIC_BASE_URL=$2#" "$1"
  else
    cat "$1"
    printf 'PUBLIC_BASE_URL=%s\n' "$2"
  fi
}

# dns_apply DOMAIN TLS EMAIL moves the running site to DOMAIN: env, Caddyfile (the host
# being replaced keeps a permanent redirect), restart, readiness, refreshed admin link.
# Shared by the DNS-switch timer and set-domain. A Caddyfile that does not load is rolled back.
dns_apply() {
  da_domain="$1"
  da_tls="$2"
  da_email="$3"
  if [ "$da_tls" = off ]; then da_base="http://$da_domain"; else da_base="https://$da_domain"; fi
  da_env="$(dest /opt/hatamoto/config/hatamoto.env)"
  da_old="$(fb_env_get "$da_env" PUBLIC_BASE_URL | sed 's#^[A-Za-z]*://##; s#[/:].*$##')"
  fb_env_edit "$da_env" set_base_url "$da_base"
  log "PUBLIC_BASE_URL=$da_base"

  da_cf="$(dest /etc/caddy/Caddyfile)"
  mkdir -p "$(dirname "$da_cf")"
  fb_render_caddyfile "$da_domain" "$da_tls" "$da_email" "$da_old" > "$da_cf.new"
  chmod 0644 "$da_cf.new"
  rm -f "$da_cf.prev"
  if [ -f "$da_cf" ]; then cp -p "$da_cf" "$da_cf.prev"; fi
  mv -f "$da_cf.new" "$da_cf"
  if [ -n "$da_old" ] && [ "$da_old" != "$da_domain" ]; then
    log "Caddyfile: $da_domain (old host $da_old redirects here)"
  else
    log "Caddyfile: $da_domain"
  fi

  log "restarting hatamoto-api, waiting for /ready"
  systemctl restart hatamoto-api
  if ! systemctl reload caddy && ! systemctl restart caddy; then
    if [ -f "$da_cf.prev" ]; then
      mv -f "$da_cf.prev" "$da_cf"
      systemctl restart caddy || true
    fi
    die "caddy did not accept the new Caddyfile; the previous one is back: see journalctl -u caddy"
  fi
  rm -f "$da_cf.prev"
  fb_wait_ready
  da_link="$(dest "$STATE_DIR/link")"
  fb_admin_url > "$da_link.new"
  chmod 0600 "$da_link.new"
  mv -f "$da_link.new" "$da_link"
}

# fb_set_domain HOST [--email ADDR] points an installed site at HOST: now when DNS already
# points here, else through the DNS-switch timer while the current host keeps serving.
# TLS mode and email come from the saved inputs, else from the current Caddyfile.
fb_set_domain() {
  umask 077
  fsd_domain=""
  fsd_email=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --email)
        [ $# -ge 2 ] || die "--email needs an address"
        fsd_email="$2"
        shift 2
        ;;
      -*) die "unknown option: $1" ;;
      *)
        [ -z "$fsd_domain" ] || die "usage: set-domain HOST [--email ADDR]"
        fsd_domain="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
        shift
        ;;
    esac
  done
  [ -n "$fsd_domain" ] || die "usage: set-domain HOST [--email ADDR]"
  fb_valid_domain "$fsd_domain" || die "not a valid host name: $fsd_domain"
  [ -z "$fsd_email" ] || fb_valid_email "$fsd_email" || die "not an email address: $fsd_email"
  [ -f "$(dest "$STATE_DIR/firstboot.done")" ] || die "first boot has not finished here: run install.sh first"
  [ -x "$(dest "$APP_DIR/bin/hatamoto")" ] || die "Hatamoto is not installed here: run install.sh first"

  fsd_saved="$(dest /etc/default/hatamoto-install)"
  fsd_cf="$(dest /etc/caddy/Caddyfile)"
  fsd_tls=""
  [ ! -r "$fsd_saved" ] || fsd_tls="$(fb_env_get "$fsd_saved" HATAMOTO_TLS)"
  if [ -z "$fsd_tls" ]; then
    if grep -q '^[[:space:]]*tls internal' "$fsd_cf" 2>/dev/null; then
      fsd_tls=internal
    elif grep -q '^http://' "$fsd_cf" 2>/dev/null; then
      fsd_tls=off
    else
      fsd_tls=auto
    fi
  fi
  case "$fsd_tls" in auto | internal | off) ;; *) die "saved HATAMOTO_TLS must be auto, internal or off" ;; esac
  if [ -z "$fsd_email" ] && [ -r "$fsd_saved" ]; then fsd_email="$(fb_env_get "$fsd_saved" HATAMOTO_ADMIN_EMAIL)"; fi
  if [ -z "$fsd_email" ]; then fsd_email="$(sed -n 's/^[[:space:]]*email[[:space:]][[:space:]]*//p' "$fsd_cf" 2>/dev/null | head -n 1)"; fi
  if [ -z "$IN_PUBLIC_IP" ] && [ -r "$fsd_saved" ]; then IN_PUBLIC_IP="$(fb_env_get "$fsd_saved" HATAMOTO_PUBLIC_IP)"; fi

  fsd_cur="$(fb_env_get "$(dest "$APP_DIR/config/hatamoto.env")" PUBLIC_BASE_URL | sed 's#^[A-Za-z]*://##; s#[/:].*$##')"
  if [ "$fsd_cur" = "$fsd_domain" ]; then
    log "the site already answers on $fsd_domain"
    return 0
  fi
  fsd_ip="$(fb_public_ip)"
  if fb_dns_matches "$fsd_domain" "$fsd_ip"; then
    log "checking DNS: $fsd_domain -> $fsd_ip ok"
    dns_apply "$fsd_domain" "$fsd_tls" "$fsd_email"
    dns_remove
    if [ "$fsd_tls" = off ]; then fsd_url="http://$fsd_domain"; else fsd_url="https://$fsd_domain"; fi
    log "the site now answers on $fsd_url"
    printf '  Admin: %s\n' "$(head -n 1 "$(dest "$STATE_DIR/link")")"
  else
    IN_TLS="$fsd_tls"
    IN_EMAIL="$fsd_email"
    FB_PENDING_DOMAIN="$fsd_domain"
    FB_IP="$fsd_ip"
    install_dns_switch
    log "Domain: $fsd_domain (waiting for DNS -> $fsd_ip; the site keeps answering on ${fsd_cur:-its current host} and will switch automatically)"
  fi
}

# install_dns_switch sets up the timer when first boot served the sslip.io name
# because the domain did not point here yet (FB_PENDING_DOMAIN, FB_IP).
install_dns_switch() {
  if [ -z "$FB_PENDING_DOMAIN" ]; then
    dns_remove
    return 0
  fi
  fb_valid_domain "$FB_PENDING_DOMAIN" || die "not a valid host name: $FB_PENDING_DOMAIN"
  fb_valid_ip "$FB_IP" || die "not an IPv4 address: $FB_IP"
  ids_state="$(dest "$STATE_DIR")"
  ids_unit="$(dest /etc/systemd/system)"
  install -d -m 0700 "$ids_state"
  mkdir -p "$ids_unit" "$(dest "$LIB_DIR")"
  printf '%s %s %s %s\n' "$FB_PENDING_DOMAIN" "$FB_IP" "$IN_TLS" "${IN_EMAIL:--}" > "$ids_state/dns-pending"

  cat > "$(dest "$LIB_DIR/dns-switch.sh")" <<'HM_SWITCH'
#!/bin/sh
# Moves the site to the owner's domain once it points at this machine. Run by
# hatamoto-dns-switch.timer; prints nothing a setup code could be read from.
set -eu
HATAMOTO_INSTALL_SOURCE_ONLY=1
export HATAMOTO_INSTALL_SOURCE_ONLY
. "${HATAMOTO_INSTALL_ROOT:-}/usr/local/lib/hatamoto/firstboot.sh"
umask 077
pending="$(dest "$STATE_DIR/dns-pending")"
if [ ! -f "$pending" ]; then
  systemctl disable --now hatamoto-dns-switch.timer >/dev/null 2>&1 || true
  exit 0
fi
read -r domain ip tls email < "$pending"
[ "$email" != - ] || email=""
fb_valid_domain "$domain" || die "bad domain in $pending"
fb_dns_matches "$domain" "$ip" || exit 0

log "$domain now points here: switching the site to it"
dns_apply "$domain" "$tls" "$email"

rm -f "$pending"
systemctl disable --now hatamoto-dns-switch.timer >/dev/null 2>&1 || true
log "the site now answers on $domain"
HM_SWITCH
  chmod 0755 "$(dest "$LIB_DIR/dns-switch.sh")"

  cat > "$ids_unit/$DNS_UNIT.service" <<HM_SERVICE
[Unit]
Description=Move the Hatamoto site to its domain once DNS points here
After=network-online.target

[Service]
Type=oneshot
ExecStart=/bin/sh $LIB_DIR/dns-switch.sh
HM_SERVICE
  cat > "$ids_unit/$DNS_UNIT.timer" <<'HM_TIMER'
[Unit]
Description=Check whether the Hatamoto domain points here

[Timer]
OnActiveSec=1min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
HM_TIMER
  systemctl daemon-reload
  systemctl enable --now "$DNS_UNIT.timer" >/dev/null 2>&1 || die "cannot start $DNS_UNIT.timer"
}
