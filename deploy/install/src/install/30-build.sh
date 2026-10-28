
# Build stage: packages, the verified binary, the unit, the firewall. Safe to
# bake into an image: it creates no env file, secret, database role or Caddy
# site, and it never starts or enables hatamoto-api (the first-boot stage does).

APP_DIR=/opt/hatamoto
APP_USER=hatamoto
PGDG_KEY=/usr/share/keyrings/postgresql-archive-keyring.asc
BUILD_PACKAGES="ca-certificates curl jq openssl sudo iproute2 ufw unattended-upgrades postgresql-16 caddy"

# build_os prints "ID VERSION_ID CODENAME" from the os-release file, or dies.
build_os() {
  bo_file="$(dest /etc/os-release)"
  [ -r "$bo_file" ] || die "cannot read /etc/os-release: supported systems are Ubuntu 24.04 and Debian 12"
  # shellcheck source=/dev/null
  bo_line="$( . "$bo_file" && printf '%s %s %s' "${ID:-}" "${VERSION_ID:-}" "${VERSION_CODENAME:-}" )"
  case "$bo_line" in
    "ubuntu 24.04 "* | "debian 12 "*) printf '%s\n' "$bo_line" ;;
    *) die "unsupported system: ${bo_line% *}: supported systems are Ubuntu 24.04 and Debian 12 (amd64 or arm64)" ;;
  esac
}

# build_policy_deny keeps packages from starting services while they install
# (a started Postgres or Caddy would create state the image must not carry).
build_policy_deny() {
  bp_file="$(dest /usr/sbin/policy-rc.d)"
  BUILD_POLICY=""
  [ ! -e "$bp_file" ] || return 0
  mkdir -p "$(dirname "$bp_file")"
  printf '#!/bin/sh\nexit 101\n' > "$bp_file"
  chmod 0755 "$bp_file"
  BUILD_POLICY="$bp_file"
}

build_policy_restore() {
  [ -z "${BUILD_POLICY:-}" ] || rm -f "$BUILD_POLICY"
  BUILD_POLICY=""
}

# build_apt runs apt-get non-interactively; a failure removes the policy file and dies.
# A fresh cloud image holds the dpkg lock for its first minutes (apt-daily, cloud-init): wait for it.
build_apt() {
  if ! DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 "$@"; then
    build_policy_restore
    die "apt-get $1 failed"
  fi
}

# build_pgdg adds the PostgreSQL project's apt repository (Debian ships 15, not 16).
build_pgdg() {
  bg_list="$(dest /etc/apt/sources.list.d/pgdg.list)"
  [ ! -f "$bg_list" ] || return 0
  mkdir -p "$(dirname "$(dest "$PGDG_KEY")")" "$(dirname "$bg_list")"
  curl --fail --silent --show-error --retry 3 --proto '=https' --tlsv1.2 \
    --output "$(dest "$PGDG_KEY")" https://www.postgresql.org/media/keys/ACCC4CF8.asc ||
    { build_policy_restore; die "cannot fetch the PostgreSQL repository key"; }
  printf 'deb [signed-by=%s] https://apt.postgresql.org/pub/repos/apt %s-pgdg main\n' "$PGDG_KEY" "$1" > "$bg_list"
}

# build_check_pg_dump: backups need a pg_dump of the server's major version (16).
build_check_pg_dump() {
  need_cmd pg_dump
  bc_major="$(pg_dump --version | awk '{ for (i = 1; i <= NF; i++) if ($i ~ /^[0-9]+(\.[0-9]+)*$/) { split($i, p, "."); print p[1]; exit } }')"
  [ "$bc_major" = 16 ] || die "pg_dump is version ${bc_major:-unknown}, not 16: nightly backups would fail"
}

build_user_and_dirs() {
  id -u "$APP_USER" >/dev/null 2>&1 ||
    useradd --system --home "$APP_DIR" --shell /usr/sbin/nologin "$APP_USER" ||
    die "cannot create the $APP_USER user"
  install -d -m 0755 "$(dest "$APP_DIR")"
  install -d -o root -g root -m 0755 "$(dest "$APP_DIR/bin")"
  install -d -o "$APP_USER" -g "$APP_USER" -m 0750 "$(dest "$APP_DIR/data")" "$(dest "$APP_DIR/uploads")"
  install -d -o root -g "$APP_USER" -m 0750 "$(dest "$APP_DIR/config")"
}

# build_binary installs the verified binary. An existing install is kept unless
# a version was asked for: the supervisor owns updates after that.
build_binary() {
  bb_bin="$(dest "$APP_DIR/bin/hatamoto")"
  if [ -x "$bb_bin" ] && [ -z "$IN_VERSION" ]; then
    log "hatamoto is already installed, keeping it"
    return 0
  fi
  bb_version="$(trust_pick_version)"
  bb_dir="$(mktemp -d)"
  trust_fetch "$bb_version" "$bb_dir/hatamoto"
  install -m 0755 -o root -g root "$bb_dir/hatamoto" "$bb_bin"
  rm -rf "$bb_dir"
  log "installed hatamoto $bb_version"
}

# build_unit installs the shipped unit with the shipped installer, both embedded verbatim.
build_unit() {
  bu_unit="$(dest /etc/systemd/system/hatamoto-api.service)"
  mkdir -p "$(dirname "$bu_unit")"
  bu_dir="$(mktemp -d)"
  payload_unit > "$bu_unit.new"
  payload_install_unit > "$bu_dir/install-unit.sh"
  UNIT="$bu_unit" sh "$bu_dir/install-unit.sh" >/dev/null || { rm -rf "$bu_dir"; die "cannot install the hatamoto-api unit"; }
  rm -rf "$bu_dir"
}

build_firewall() {
  [ "$IN_FIREWALL" != 0 ] || { log "firewall left alone (HATAMOTO_FIREWALL=0)"; return 0; }
  {
    ufw default deny incoming &&
      ufw default allow outgoing &&
      ufw allow 22/tcp &&
      ufw allow 80/tcp &&
      ufw allow 443/tcp &&
      ufw --force enable
  } >/dev/null || die "cannot configure the firewall (set HATAMOTO_FIREWALL=0 if the provider firewall does the job)"
}

build_unattended() {
  bu_conf="$(dest /etc/apt/apt.conf.d/20auto-upgrades)"
  mkdir -p "$(dirname "$bu_conf")"
  printf 'APT::Periodic::Update-Package-Lists "1";\nAPT::Periodic::Unattended-Upgrade "1";\n' > "$bu_conf"
  systemctl enable unattended-upgrades >/dev/null 2>&1 || warn "cannot enable unattended-upgrades"
}

# build_firstboot_files installs the first-boot script and the unit that runs it once.
build_firstboot_files() {
  bf_lib="$(dest "$LIB_DIR")"
  bf_unit="$(dest /etc/systemd/system/hatamoto-firstboot.service)"
  mkdir -p "$bf_lib" "$(dirname "$bf_unit")"
  payload_firstboot > "$bf_lib/firstboot.sh"
  chmod 0755 "$bf_lib/firstboot.sh"
  # /etc/default/hatamoto-install may carry the HATAMOTO_* inputs for a sealed image.
  cat > "$bf_unit" <<HM_UNIT
[Unit]
Description=Hatamoto first boot
ConditionPathExists=!$STATE_DIR/firstboot.done
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=oneshot
EnvironmentFile=-/etc/default/hatamoto-install
ExecStart=/bin/sh $LIB_DIR/firstboot.sh first-boot
TimeoutStartSec=900

[Install]
WantedBy=multi-user.target
HM_UNIT
  systemctl daemon-reload
  systemctl enable hatamoto-firstboot.service >/dev/null 2>&1 || die "cannot enable hatamoto-firstboot.service"
}

stage_build() {
  sb_os="$(build_os)"
  sb_id="${sb_os%% *}"
  sb_codename="${sb_os##* }"
  arch >/dev/null
  need_cmd apt-get
  log "build stage on $sb_os"

  build_policy_deny
  build_apt update -qq
  build_apt install -y --no-install-recommends ca-certificates curl gnupg
  if [ "$sb_id" = debian ]; then
    build_pgdg "$sb_codename"
    build_apt update -qq
  fi
  # shellcheck disable=SC2086 # the list is words on purpose
  build_apt install -y --no-install-recommends -o Dpkg::Options::=--force-confold $BUILD_PACKAGES
  build_policy_restore
  build_check_pg_dump

  build_user_and_dirs
  build_binary
  build_unit
  build_firewall
  build_unattended
  build_firstboot_files
  log "build stage done"
}
