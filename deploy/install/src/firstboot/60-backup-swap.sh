
# Swap for small machines and the nightly backup timer.
#
# Retention is the CLI's job (docs/BACKUPS.md: HATAMOTO_BACKUP_KEEP prunes after
# every backup), so the unit does not delete files itself.

BACKUP_UNIT=hatamoto-backup
SWAP_MIN_KB=1048576
SWAP_MAX_KB=2097152

# setup_swap adds /swapfile when the machine has 2 GiB of RAM or less and no swap.
setup_swap() {
  ssw_mem="$(awk '/^MemTotal:/ { print $2 }' "$(dest /proc/meminfo)" 2>/dev/null)"
  case "$ssw_mem" in '' | *[!0-9]*) warn "cannot read the memory size: no swap added"; return 0 ;; esac
  [ "$ssw_mem" -le 2097152 ] || return 0
  if [ -f "$(dest /proc/swaps)" ] && [ "$(wc -l < "$(dest /proc/swaps)")" -gt 1 ]; then return 0; fi
  ssw_file="$(dest /swapfile)"
  [ ! -e "$ssw_file" ] || { log "swap file exists already"; return 0; }
  ssw_kb="$ssw_mem"
  [ "$ssw_kb" -ge "$SWAP_MIN_KB" ] || ssw_kb="$SWAP_MIN_KB"
  [ "$ssw_kb" -le "$SWAP_MAX_KB" ] || ssw_kb="$SWAP_MAX_KB"
  umask 077
  if ! fallocate -l "${ssw_kb}K" "$ssw_file" 2>/dev/null; then
    dd if=/dev/zero of="$ssw_file" bs=1024 count="$ssw_kb" 2>/dev/null || { rm -f "$ssw_file"; warn "cannot create the swap file"; return 0; }
  fi
  chmod 0600 "$ssw_file"
  if ! mkswap "$ssw_file" >/dev/null 2>&1 || ! swapon "$ssw_file" 2>/dev/null; then
    rm -f "$ssw_file"
    warn "swap cannot be enabled here (a container?): continuing without it"
    return 0
  fi
  ssw_fstab="$(dest /etc/fstab)"
  mkdir -p "$(dirname "$ssw_fstab")"
  grep -qs '^/swapfile[[:space:]]' "$ssw_fstab" || printf '/swapfile none swap sw 0 0\n' >> "$ssw_fstab"
  log "added $((ssw_kb / 1024)) MiB of swap"
}

# setup_backups installs the nightly backup timer when HATAMOTO_BACKUPS is 1, else removes it.
setup_backups() {
  sb_dir="$(dest /etc/systemd/system)"
  if [ "$IN_BACKUPS" != 1 ]; then
    if [ -f "$sb_dir/$BACKUP_UNIT.timer" ]; then
      systemctl disable --now "$BACKUP_UNIT.timer" >/dev/null 2>&1 || true
    fi
    rm -f "$sb_dir/$BACKUP_UNIT.timer" "$sb_dir/$BACKUP_UNIT.service"
    return 0
  fi
  mkdir -p "$sb_dir"
  cat > "$sb_dir/$BACKUP_UNIT.service" <<HM_SERVICE
[Unit]
Description=Hatamoto backup
After=postgresql.service

[Service]
Type=oneshot
User=$APP_USER
WorkingDirectory=$APP_DIR
EnvironmentFile=$APP_DIR/config/hatamoto.env
Environment=HATAMOTO_DATA=$APP_DIR/data
ExecStart=$APP_DIR/bin/hatamoto backup create
HM_SERVICE
  cat > "$sb_dir/$BACKUP_UNIT.timer" <<'HM_TIMER'
[Unit]
Description=Nightly Hatamoto backup

[Timer]
OnCalendar=*-*-* 03:30
RandomizedDelaySec=30min
Persistent=true

[Install]
WantedBy=timers.target
HM_TIMER
  systemctl daemon-reload
  systemctl enable --now "$BACKUP_UNIT.timer" >/dev/null 2>&1 || die "cannot enable $BACKUP_UNIT.timer"
}
