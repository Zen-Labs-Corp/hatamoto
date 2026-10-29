
# Setup code clean-up.
#
# The setup code only matters until the owner account exists (the server ignores it
# once the site has users), but it would stay in the 0640 env file and in every
# backup of it. A timer removes that one line as soon as the user table is not
# empty. Nothing is restarted: the server reads its configuration at boot.

CLEAN_UNIT=hatamoto-setupcode-clean

# install_setupcode_clean writes the script and its service and timer, and starts the timer.
install_setupcode_clean() {
  isc_unit="$(dest /etc/systemd/system)"
  mkdir -p "$isc_unit" "$(dest "$LIB_DIR")"

  cat > "$(dest "$LIB_DIR/setupcode-clean.sh")" <<'HM_CLEAN'
#!/bin/sh
# Removes HATAMOTO_SETUP_CODE from the env file once the owner account exists.
# Run by hatamoto-setupcode-clean.timer; never prints the code.
set -eu
HATAMOTO_INSTALL_SOURCE_ONLY=1
export HATAMOTO_INSTALL_SOURCE_ONLY
. "${HATAMOTO_INSTALL_ROOT:-}/usr/local/lib/hatamoto/firstboot.sh"
umask 077
envfile="$(dest /opt/hatamoto/config/hatamoto.env)"
if [ ! -f "$envfile" ] || ! grep -q '^HATAMOTO_SETUP_CODE=' "$envfile"; then
  systemctl disable --now hatamoto-setupcode-clean.timer >/dev/null 2>&1 || true
  exit 0
fi
owners="$(cd / && sudo -n -u postgres psql -X -d hatamoto -tAc 'select count(*) from hatamoto_users' 2>/dev/null)" || exit 0
[ "$owners" != 0 ] || exit 0

# Same owner and mode as the live file (cp -p), only that one line gone, then an atomic rename.
tmp="$envfile.new"
cp -p "$envfile" "$tmp"
grep -v '^HATAMOTO_SETUP_CODE=' "$envfile" > "$tmp" || [ "$?" = 1 ]
mv -f "$tmp" "$envfile"
systemctl disable --now hatamoto-setupcode-clean.timer >/dev/null 2>&1 || true
log "the owner account exists: the setup code is removed from the env file"
HM_CLEAN
  chmod 0755 "$(dest "$LIB_DIR/setupcode-clean.sh")"

  cat > "$isc_unit/$CLEAN_UNIT.service" <<HM_SERVICE
[Unit]
Description=Remove the Hatamoto setup code once the owner account exists
After=postgresql.service

[Service]
Type=oneshot
ExecStart=/bin/sh $LIB_DIR/setupcode-clean.sh
HM_SERVICE
  cat > "$isc_unit/$CLEAN_UNIT.timer" <<'HM_TIMER'
[Unit]
Description=Check whether the Hatamoto owner account exists

[Timer]
OnActiveSec=1min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
HM_TIMER
  systemctl daemon-reload
  systemctl enable --now "$CLEAN_UNIT.timer" >/dev/null 2>&1 || die "cannot start $CLEAN_UNIT.timer"
}
