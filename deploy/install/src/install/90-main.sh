# Entry point. Everything above is a function definition, so a download cut
# short never runs half a script: main "$@" is the very last line.

# run_part calls a stage defined by another part, or says it is not in this build.
run_part() {
  command -v "$1" >/dev/null 2>&1 || die "stage not available in this build: $1"
  "$@"
}

# run_firstboot runs the installed first-boot script (a pipe cannot be re-read).
run_firstboot() {
  [ -f "$(dest "$LIB_DIR")/firstboot.sh" ] || die "first-boot script is not installed: run the build stage first"
  sh "$(dest "$LIB_DIR")/firstboot.sh" first-boot
}

# run_set_domain refreshes the on-box first-boot script from this installer, then lets it
# move the site. A core older than the installer minimum is refused.
run_set_domain() {
  [ $# -ge 1 ] || die "usage: install.sh set-domain HOST [--email ADDR]"
  [ -x "$(dest "$APP_DIR/bin/hatamoto")" ] || die "Hatamoto is not installed here: run install.sh first"
  sd_version="$("$(dest "$APP_DIR/bin/hatamoto")" version 2>/dev/null | grep -Eo 'v[0-9]+\.[0-9]+\.[0-9]+' | head -n 1 || true)"
  if [ -n "$sd_version" ] && trust_version_lt "$sd_version" "$INSTALLER_MIN_VERSION"; then
    die "Hatamoto $sd_version is older than $INSTALLER_MIN_VERSION: update Hatamoto first"
  fi
  sd_lib="$(dest "$LIB_DIR")"
  mkdir -p "$sd_lib"
  payload_firstboot > "$sd_lib/firstboot.sh"
  chmod 0755 "$sd_lib/firstboot.sh"
  sh "$sd_lib/firstboot.sh" set-domain "$@"
}

usage() {
  cat <<'HM_USAGE'
usage: install.sh [all | build [--seal] | first-boot | set-domain HOST [--email ADDR] | download [--channel stable|edge] VERSION OUTFILE | version]
  all (default)  build stage, then first-boot stage; never seals
  build          install packages, the verified binary and the unit
  build --seal   as build, then remove host identity and caches for an image
  first-boot     create secrets, database and site, start the service
  set-domain     point the installed site at another host name
  download       verify and fetch one release binary from a channel (default stable; used by the tests and hosting tooling)
  version        print the installer version
HM_USAGE
}

main() {
  [ -z "${HATAMOTO_INSTALL_SOURCE_ONLY:-}" ] || return 0
  cmd="${1:-all}"
  [ $# -eq 0 ] || shift
  case "$cmd" in
    all)
      [ $# -eq 0 ] || die "all takes no arguments"
      need_root
      run_part stage_build
      run_firstboot
      ;;
    build)
      need_root
      seal=0
      case "${1:-}" in
        "") ;;
        --seal) seal=1 ;;
        *) die "unknown argument: $1" ;;
      esac
      run_part stage_build
      if [ "$seal" = 1 ]; then run_part stage_seal; fi
      ;;
    first-boot)
      need_root
      run_firstboot
      ;;
    set-domain)
      need_root
      run_set_domain "$@"
      ;;
    download)
      dl_set=0
      dl_chan=""
      if [ "${1:-}" = "--channel" ]; then
        [ $# -ge 2 ] || die "usage: install.sh download [--channel stable|edge] VERSION OUTFILE"
        dl_set=1
        dl_chan="$2"
        shift 2
      fi
      [ $# -eq 2 ] || die "usage: install.sh download [--channel stable|edge] VERSION OUTFILE"
      [ "$dl_set" = 0 ] || run_part trust_set_channel "$dl_chan"
      run_part trust_fetch "$1" "$2"
      ;;
    version)
      echo "hatamoto-install 1"
      ;;
    -h | --help | help)
      usage
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
}

main "$@"
