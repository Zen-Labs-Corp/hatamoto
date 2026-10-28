#!/bin/sh
# Hatamoto VPS installer. Assembled by deploy/install/build-install.sh: do not
# edit the assembled file, edit the parts under deploy/install/src.
#
# Inputs (environment, all optional):
#   HATAMOTO_DOMAIN         public host name; empty serves on the sslip.io name of the public IP
#   HATAMOTO_VERSION        release to install (default: newest fully ramped stable release)
#   HATAMOTO_ADMIN_EMAIL    contact address for the certificate authority
#   HATAMOTO_SETUP_CODE     pin the setup code (default: generated at first boot)
#   HATAMOTO_BACKUPS        1 (default) nightly local backups, 0 off
#   HATAMOTO_PUBLIC_IP      public IPv4 address (default: detected)
#   HATAMOTO_TLS            auto (default), internal or off
#   HATAMOTO_FIREWALL       1 (default) configure ufw, 0 skip
#   HATAMOTO_RELEASE_BASE   release site (signatures still bind)
set -eu

# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
IN_DOMAIN="${HATAMOTO_DOMAIN:-}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
IN_VERSION="${HATAMOTO_VERSION:-}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
IN_EMAIL="${HATAMOTO_ADMIN_EMAIL:-}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
IN_SETUP_CODE="${HATAMOTO_SETUP_CODE:-}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
IN_BACKUPS="${HATAMOTO_BACKUPS:-1}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
IN_PUBLIC_IP="${HATAMOTO_PUBLIC_IP:-}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
IN_TLS="${HATAMOTO_TLS:-auto}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
IN_FIREWALL="${HATAMOTO_FIREWALL:-1}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
RELEASE_BASE="${HATAMOTO_RELEASE_BASE:-https://hatamoto.dev/releases}"

# R prefixes every path the installer writes; empty on a real host.
R="${HATAMOTO_INSTALL_ROOT:-}"
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
LIB_DIR=/usr/local/lib/hatamoto
# shellcheck disable=SC2034 # part contract: used by the install or firstboot parts
STATE_DIR=/var/lib/hatamoto-install

log() { printf '%s\n' "hatamoto: $*"; }
warn() { printf '%s\n' "hatamoto: warning: $*" >&2; }
die() { printf '%s\n' "hatamoto: error: $*" >&2; exit 1; }

need_root() {
  [ -n "$R" ] || [ "$(id -u)" = 0 ] || die "run as root (for example: curl -fsSL https://hatamoto.dev/install.sh | sudo sh)"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

# dest prints an absolute path under the install root.
dest() {
  printf '%s%s\n' "$R" "$1"
}

# arch prints amd64 or arm64.
arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) die "unsupported CPU architecture: $(uname -m) (amd64 and arm64 only)" ;;
  esac
}
