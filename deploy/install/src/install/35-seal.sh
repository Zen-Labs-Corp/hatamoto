
# Seal stage (build --seal only): strip host identity, history, caches and logs
# so a snapshot of this machine carries nothing of the build. Never part of
# `all`: cloud-init clean would erase the user-data run that is driving it.
#
# Commands that act on the live system (apt-get, passwd, cloud-init) only run
# on a real host; with HATAMOTO_INSTALL_ROOT set the files are edited directly,
# so a test can never touch the machine it runs on.

# seal_homes prints the home directories whose per-user files are scrubbed.
seal_homes() {
  printf '%s\n' "$(dest /root)"
  for sh_dir in "$(dest /home)"/*; do
    [ -d "$sh_dir" ] && printf '%s\n' "$sh_dir"
  done
  return 0
}

seal_lock_passwords() {
  sl_shadow="$(dest /etc/shadow)"
  [ -f "$sl_shadow" ] || return 0
  sl_users="root $(awk -F: '$3 >= 1000 && $3 < 65534 { print $1 }' "$(dest /etc/passwd)" 2>/dev/null)"
  for sl_user in $sl_users; do
    grep -q "^$sl_user:" "$sl_shadow" || continue
    sl_hash="$(awk -F: -v u="$sl_user" '$1 == u { print $2 }' "$sl_shadow")"
    case "$sl_hash" in "!"* | "*"*) continue ;; esac
    if [ -z "$R" ]; then
      passwd -l "$sl_user" >/dev/null
    else
      sed -i "s/^$sl_user:[^:]*:/$sl_user:!:/" "$sl_shadow"
    fi
  done
}

# seal_pg_install_exists succeeds when this machine's PostgreSQL already holds an
# install of Hatamoto (only answerable while the server runs; a test replaces it).
seal_pg_install_exists() {
  command -v psql >/dev/null 2>&1 || return 1
  [ -n "$(sudo -n -u postgres psql -X -tA -c "select 1 from pg_roles where rolname = 'hatamoto' union all select 1 from pg_database where datname = 'hatamoto'" 2>/dev/null)" ]
}

# seal_refuse_used dies when first boot already ran here: the snapshot would carry that
# install's database and password hash, which no scrub below removes.
seal_refuse_used() {
  for sru_f in "$(dest "$STATE_DIR")/firstboot.done" "$(dest "$APP_DIR/config/hatamoto.env")" \
    "$(dest /etc/systemd/system/multi-user.target.wants/hatamoto-api.service)"; do
    [ ! -e "$sru_f" ] || die "this machine already ran first boot ($sru_f exists): seal a fresh build, not an installed server"
  done
  ! seal_pg_install_exists || die "this machine's PostgreSQL already has a hatamoto role or database: seal a fresh build, not an installed server"
}

stage_seal() {
  seal_refuse_used
  log "sealing the image"
  if [ -z "$R" ]; then
    # Bring security updates in before the package lists go.
    build_apt_update || warn "apt-get update failed before sealing"
    DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 -y -qq -o Dpkg::Options::=--force-confold upgrade || warn "apt-get upgrade failed before sealing"
    apt-get -o DPkg::Lock::Timeout=600 clean
  fi

  # Host identity.
  for ss_f in "$(dest /etc/ssh)"/ssh_host_*; do
    [ -e "$ss_f" ] && rm -f "$ss_f"
  done
  mkdir -p "$(dest /etc)"
  : > "$(dest /etc/machine-id)"
  rm -f "$(dest /var/lib/dbus/machine-id)"
  # Shared by every clone otherwise: the RNG seed and the TLS key PostgreSQL uses
  # (first boot makes a new pair).
  rm -f "$(dest /var/lib/systemd/random-seed)" "$(dest /etc/ssl/private/ssl-cert-snakeoil.key)" "$(dest /etc/ssl/certs/ssl-cert-snakeoil.pem)"

  # Per-user traces: keys, known hosts, history.
  seal_homes | while IFS= read -r ss_home; do
    rm -f "$ss_home"/.ssh/authorized_keys "$ss_home"/.ssh/known_hosts "$ss_home"/.ssh/id_* \
      "$ss_home"/.bash_history "$ss_home"/.history "$ss_home"/.lesshst "$ss_home"/.viminfo
  done
  seal_lock_passwords

  # Packages and temp files.
  # ${:?} guards the globs: dest always yields a non-empty path, so this never expands to /*.
  seal_apt_lists="$(dest /var/lib/apt/lists)"
  rm -rf "${seal_apt_lists:?}"/* "$(dest /var/cache/apt)"/*.bin "$(dest /var/cache/apt/archives)"/*.deb
  if [ -d "$(dest /tmp)" ]; then find "$(dest /tmp)" -mindepth 1 -delete; fi
  if [ -d "$(dest /var/tmp)" ]; then find "$(dest /var/tmp)" -mindepth 1 -delete; fi

  # Anything that could be a secret of this install.
  rm -f "$(dest "$APP_DIR/config/hatamoto.env")" "$(dest "$APP_DIR/data/install_id")"
  rm -rf "$(dest "$APP_DIR/data/secrets")" "$(dest "$STATE_DIR")" \
    "$(dest /var/lib/caddy/.local/share/caddy)" "$(dest /var/lib/caddy/.config/caddy)"

  # cloud-init: a fresh instance must run its modules again.
  if [ -z "$R" ]; then
    if command -v cloud-init >/dev/null 2>&1; then cloud-init clean --logs >/dev/null 2>&1 || warn "cloud-init clean failed"; fi
  else
    rm -rf "$(dest /var/lib/cloud)"/instance "$(dest /var/lib/cloud)"/instances
  fi

  # Logs last: everything above may have written some.
  if [ -d "$(dest /var/log/journal)" ]; then find "$(dest /var/log/journal)" -type f -delete; fi
  if [ -d "$(dest /var/log)" ]; then
    find "$(dest /var/log)" -type f \( -name '*.gz' -o -name '*.xz' -o -name '*.old' -o -name '*.[0-9]' -o -name '*-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]' \) -delete
    find "$(dest /var/log)" -type f -exec sh -c ': > "$1"' _ {} \;
  fi
  [ -n "$R" ] || sync
  log "sealed: power off and take the snapshot now. Do not log in first, it would leave history and logs behind."
}
