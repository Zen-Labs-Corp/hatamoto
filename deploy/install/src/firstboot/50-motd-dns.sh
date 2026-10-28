
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
if [ "$tls" = off ]; then base="http://$domain"; else base="https://$domain"; fi
envfile="$(dest /opt/hatamoto/config/hatamoto.env)"
tmp="$envfile.new"
cp -p "$envfile" "$tmp"
if grep -q '^PUBLIC_BASE_URL=' "$envfile"; then
  sed "s#^PUBLIC_BASE_URL=.*#PUBLIC_BASE_URL=$base#" "$envfile" > "$tmp"
else
  { cat "$envfile"; printf 'PUBLIC_BASE_URL=%s\n' "$base"; } > "$tmp"
fi
mv -f "$tmp" "$envfile"

caddyfile="$(dest /etc/caddy/Caddyfile)"
fb_render_caddyfile "$domain" "$tls" "$email" > "$caddyfile.new"
chmod 0644 "$caddyfile.new"
mv -f "$caddyfile.new" "$caddyfile"

systemctl restart hatamoto-api
systemctl reload caddy || systemctl restart caddy
fb_wait_ready
fb_admin_url > "$(dest "$STATE_DIR/link").new"
chmod 0600 "$(dest "$STATE_DIR/link").new"
mv -f "$(dest "$STATE_DIR/link").new" "$(dest "$STATE_DIR/link")"

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
